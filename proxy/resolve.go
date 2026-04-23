package proxy

import (
	"context"
	"net"
	"sync"
	"time"
)

const resolvedAddrTTL = 30 * time.Minute

type ResolvedAddr struct {
	original string
	host     string
	port     string
	ttl      time.Duration
	now      func() time.Time
	lookup   func(context.Context, string) ([]net.IPAddr, error)

	mu         sync.Mutex
	resolved   string
	expiresAt  time.Time
	refreshing bool
}

func NewResolvedAddr(address string) *ResolvedAddr {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" || net.ParseIP(host) != nil {
		return &ResolvedAddr{original: address}
	}
	return &ResolvedAddr{
		original: address,
		host:     host,
		port:     port,
		ttl:      resolvedAddrTTL,
		now:      time.Now,
		lookup:   net.DefaultResolver.LookupIPAddr,
	}
}

func (a *ResolvedAddr) Address(ctx context.Context) (string, error) {
	if a.host == "" {
		return a.original, nil
	}

	a.mu.Lock()
	now := a.now()
	// Fresh cache entry: reuse it directly and skip DNS.
	if a.resolved != "" && now.Before(a.expiresAt) {
		resolved := a.resolved
		a.mu.Unlock()
		return resolved, nil
	}

	// At this point the cached entry is stale or missing.
	stale := a.resolved
	// If another goroutine is already refreshing this hostname, keep using the
	// stale IP instead of sending more concurrent DNS lookups.
	if stale != "" && a.refreshing {
		a.mu.Unlock()
		return stale, nil
	}
	// Mark that a refresh is in flight only when we already have a stale IP to
	// fall back to. For the very first lookup there is no cached value to serve.
	if stale != "" {
		a.refreshing = true
	}
	host := a.host
	port := a.port
	ttl := a.ttl
	lookup := a.lookup
	a.mu.Unlock()

	ips, err := lookup(ctx, host)
	if err == nil {
		for _, ip := range ips {
			if ip.IP == nil {
				continue
			}
			resolved := net.JoinHostPort(ip.IP.String(), port)
			a.mu.Lock()
			// Successful refresh: replace the cached IP and move the expiry window
			// forward from "now".
			a.resolved = resolved
			a.expiresAt = a.now().Add(ttl)
			if stale != "" {
				a.refreshing = false
			}
			a.mu.Unlock()
			return resolved, nil
		}
	}

	a.mu.Lock()
	resolved := a.resolved
	if stale != "" {
		// Refresh attempt finished, even if it failed. Future calls may try DNS
		// again after this one returns.
		a.refreshing = false
	}
	a.mu.Unlock()
	// If lookup failed but we still have a previously resolved IP, keep using it
	// rather than failing immediately on transient DNS issues.
	if resolved != "" {
		return resolved, nil
	}
	if stale != "" {
		return stale, nil
	}
	// No cached IP exists yet, so fall back to the original host:port and let
	// the underlying dialer perform normal name resolution.
	return a.original, nil
}

var (
	addrCache = make(map[string]*ResolvedAddr)
	cacheMu   sync.Mutex
)

// GetCachedAddr returns a cached ResolvedAddr or creates a new one.
func GetCachedAddr(address string) *ResolvedAddr {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if ra, ok := addrCache[address]; ok {
		return ra
	}

	ra := NewResolvedAddr(address)
	addrCache[address] = ra
	return ra
}
