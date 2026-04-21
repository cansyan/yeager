package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cansyan/yeager/proxy"
)

// proxyGroup implements ContextDialer and performs periodic health checks.
type proxyGroup struct {
	proxies []*url.URL

	mu       sync.RWMutex
	ticker   *time.Ticker
	dialerID string
	dialer   proxy.ContextDialer

	bypass *hostMatcher
	block  *hostMatcher
}

func newProxyGroup(proxies []*url.URL, bypass, block string, probe probe) (*proxyGroup, error) {
	if len(proxies) == 0 {
		return nil, errors.New("missing proxy config")
	}

	g := new(proxyGroup)
	if block != "" {
		g.block = parseHostMatcher(block)
	}
	if bypass != "" {
		g.bypass = parseHostMatcher(bypass)
	}

	if len(proxies) == 1 {
		d, err := proxy.FromURL(proxies[0])
		if err != nil {
			return nil, err
		}
		g.dialer = d
		return g, nil
	}

	g.proxies = proxies
	if err := g.Select(probe.Timeout); err != nil {
		return nil, err
	}
	interval := probe.Interval
	if interval == 0 {
		interval = 60
	}
	g.ticker = time.NewTicker(time.Duration(interval) * time.Second)
	go func() {
		for range g.ticker.C {
			if err := g.Select(probe.Timeout); err != nil {
				log.Printf("select transport: %s", err)
			}
		}
	}()
	return g, nil
}

func (g *proxyGroup) Select(timeout int) error {
	if timeout <= 0 {
		timeout = 3
	}
	var winner proxy.ContextDialer
	var winnerURL *url.URL
	var minLatency time.Duration
	for _, u := range g.proxies {
		d, err := proxy.FromURL(u)
		if err != nil {
			log.Print(err)
			continue
		}

		start := time.Now()
		conn, err := net.DialTimeout("tcp", u.Host, time.Duration(timeout)*time.Second)
		if err != nil {
			debugf("probe %s: %s", u.Host, err)
			continue
		}
		conn.Close()
		duration := time.Since(start)

		if minLatency == 0 || duration < minLatency {
			minLatency = duration
			winner = d
			winnerURL = u
		}
		debugf("probe %s %dms", u.Host, duration.Milliseconds())
	}
	if winner == nil {
		return errors.New("unable to find a valid server")
	}

	if g.dialerID == winnerURL.String() {
		return nil
	}

	g.mu.Lock()
	g.dialer = winner
	g.dialerID = winnerURL.String()
	g.mu.Unlock()
	var name string
	if q := winnerURL.Query(); q != nil {
		if n := q.Get("name"); n != "" {
			name = n
		}
	}
	log.Printf("select server: %s %s", winnerURL.Host, name)
	return nil
}

// implements interface ContextDialer
func (g *proxyGroup) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.block != nil && g.block.match(addr) {
		return nil, errors.New("blocked host")
	}
	if g.bypass != nil && g.bypass.match(addr) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		debugf("bypass %s", addr)
		return conn, nil
	}
	debugf("connect to %s", addr)
	return g.dialer.DialContext(ctx, network, addr)
}

func (g *proxyGroup) Close() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.ticker != nil {
		g.ticker.Stop()
	}
	return nil
}

type hostMatcher struct {
	ipMatchers     []matcher
	domainMatchers []matcher
}

func parseHostMatcher(s string) *hostMatcher {
	if s == "" {
		return nil
	}
	var h hostMatcher
	for _, host := range strings.Split(s, ",") {
		host = strings.ToLower(strings.TrimSpace(host))
		if len(host) == 0 {
			continue
		}

		if host == "*" {
			h.ipMatchers = []matcher{allMatch{}}
			h.domainMatchers = []matcher{allMatch{}}
			break
		}

		// IP/CIDR
		if _, pnet, err := net.ParseCIDR(host); err == nil {
			h.ipMatchers = append(h.ipMatchers, cidrMatch{cidr: pnet})
			continue
		}

		// IP
		if pip := net.ParseIP(host); pip != nil {
			h.ipMatchers = append(h.ipMatchers, ipMatch{ip: pip})
			continue
		}

		// domain name
		phost := strings.TrimPrefix(host, "*.")
		h.domainMatchers = append(h.domainMatchers, domainMatch{host: phost})
	}
	return &h
}

func (h *hostMatcher) match(addr string) bool {
	if len(addr) == 0 || len(h.ipMatchers)+len(h.domainMatchers) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)

	if ip != nil {
		for _, m := range h.ipMatchers {
			if m.match("", ip) {
				return true
			}
		}
		return false
	}

	for _, m := range h.domainMatchers {
		if m.match(host, nil) {
			return true
		}
	}
	return false
}

type matcher interface {
	// match returns true if the host or ip are allowed
	match(host string, ip net.IP) bool
}

// allMatch matches on all possible inputs
type allMatch struct{}

func (a allMatch) match(host string, ip net.IP) bool {
	return true
}

type cidrMatch struct {
	cidr *net.IPNet
}

func (m cidrMatch) match(host string, ip net.IP) bool {
	return m.cidr.Contains(ip)
}

type ipMatch struct {
	ip net.IP
}

func (m ipMatch) match(host string, ip net.IP) bool {
	return m.ip.Equal(ip)
}

// domainMatch matches a domain name and all subdomains.
// For example "foo.com" matches "foo.com" and "bar.foo.com", but not "xfoo.com"
type domainMatch struct {
	host string
}

func (m domainMatch) match(host string, ip net.IP) bool {
	before, found := strings.CutSuffix(host, m.host)
	if !found {
		return false
	}
	return before == "" || before[len(before)-1] == '.'
}
