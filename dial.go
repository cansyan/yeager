package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cansyan/yeager/proxy"
)

// proxyGroup implements ContextDialer and performs periodic health checks.
// proxyGroup should be Close after use.
type proxyGroup struct {
	urls    []*url.URL
	dialers []proxy.ContextDialer
	ticker  *time.Ticker
	mu      sync.RWMutex // guards idx
	idx     int          // current dialer index

	fails atomic.Int64

	bypass *hostMatcher
	block  *hostMatcher
}

const (
	defaultProbeInterval = 30
	defaultProbeTimeout  = 5
)

func newProxyGroup(c Config) (*proxyGroup, error) {
	if len(c.Proxy) == 0 {
		return nil, errors.New("missing proxy url")
	}

	pc := c.Probe
	if pc.Timeout == 0 {
		pc.Timeout = defaultProbeTimeout
	}
	if pc.Interval == 0 {
		pc.Interval = defaultProbeInterval
	}

	g := new(proxyGroup)
	if c.Block != "" {
		g.block = parseHostMatcher(c.Block)
	}
	if c.Bypass != "" {
		g.bypass = parseHostMatcher(c.Bypass)
	}

	g.urls = make([]*url.URL, len(c.Proxy))
	g.dialers = make([]proxy.ContextDialer, len(c.Proxy))

	for i, s := range c.Proxy {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return nil, errors.New("invalid proxy url: " + err.Error())
		}
		g.urls[i] = u
		d, err := proxy.FromURL(u)
		if err != nil {
			return nil, err
		}
		g.dialers[i] = d
	}
	if len(g.dialers) == 1 {
		return g, nil
	}

	g.Select(pc)

	g.ticker = time.NewTicker(time.Duration(pc.Interval) * time.Second)
	go func() {
		for range g.ticker.C {
			// avoid unnecessary probe if the current server is healthy
			fails := g.fails.Swap(0)
			if fails <= int64(pc.Interval)/defaultProbeTimeout {
				continue
			}
			g.Select(pc)
		}
	}()
	return g, nil
}

func (g *proxyGroup) probe(i int, kind string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// default tcp test
	if kind == "" || kind == "tcp" {
		addr, err := proxy.GetCachedAddr(g.urls[i].Host).Address(ctx)
		if err != nil {
			return err
		}
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}

	// url test
	const testURL, testHost = "http://www.gstatic.com/generate_204", "www.gstatic.com:80"
	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return err
	}
	conn, err := g.dialers[i].DialContext(ctx, "tcp", testHost)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = req.Write(conn); err != nil {
		return err
	}

	ch := make(chan error, 1)
	go func() {
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			ch <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			ch <- errors.New("unexpected status: " + resp.Status)
			return
		}
		io.Copy(io.Discard, resp.Body)
		ch <- nil
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-ch:
		return err
	}
}

func (g *proxyGroup) Select(probe probeConfig) {
	g.mu.RLock()
	current := g.idx
	g.mu.RUnlock()

	var best int
	var bestIdx int
	results := make(chan [2]int, len(g.urls))

	for i, u := range g.urls {
		go func(i int, u *url.URL) {
			start := time.Now()
			err := g.probe(i, probe.Type, time.Duration(probe.Timeout)*time.Second)
			duration := time.Since(start)
			results <- [2]int{i, int(duration.Milliseconds())}
			if err != nil {
				debugf("probe %s, err: %s", u.Host, err)
				return
			}
			debugf("probe %s, rtt %dms", u.Host, duration.Milliseconds())
		}(i, u)
	}

	for range g.urls {
		r := <-results
		i, latency := r[0], r[1]
		if best == 0 || latency < best {
			best = latency
			bestIdx = i
		}
	}

	if current == bestIdx {
		return
	}

	g.mu.Lock()
	g.idx = bestIdx
	g.mu.Unlock()
	log.Printf("select server %s", g.urls[bestIdx].Host)
}

// implements interface proxy.ContextDialer
func (g *proxyGroup) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
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

	g.mu.RLock()
	i := g.idx
	g.mu.RUnlock()
	d := g.dialers[i]
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		g.fails.Add(1)
	}
	debugf("connect to %s", addr)
	return conn, err
}

func (g *proxyGroup) Close() error {
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
