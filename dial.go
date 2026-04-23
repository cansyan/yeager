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
	"time"

	"github.com/cansyan/yeager/proxy"
)

// proxyGroup implements ContextDialer and performs periodic health checks.
type proxyGroup struct {
	urls    []*url.URL
	dialers []proxy.ContextDialer
	ticker  *time.Ticker
	mu      sync.RWMutex // guards idx
	idx     int          // current dialer index

	bypass *hostMatcher
	block  *hostMatcher
}

func newProxyGroup(urls []*url.URL, bypass, block string, probe probeConfig) (*proxyGroup, error) {
	if len(urls) == 0 {
		return nil, errors.New("missing proxy config")
	}

	g := new(proxyGroup)
	if block != "" {
		g.block = parseHostMatcher(block)
	}
	if bypass != "" {
		g.bypass = parseHostMatcher(bypass)
	}

	g.urls = urls
	g.dialers = make([]proxy.ContextDialer, len(urls))
	for i, u := range urls {
		d, err := proxy.FromURL(u)
		if err != nil {
			return nil, err
		}
		g.dialers[i] = d
	}
	if len(g.dialers) == 1 {
		return g, nil
	}

	if err := g.Select(probe); err != nil {
		return nil, err
	}
	interval := probe.Interval
	if interval == 0 {
		interval = 60
	}
	g.ticker = time.NewTicker(time.Duration(interval) * time.Second)
	go func() {
		for range g.ticker.C {
			if err := g.Select(probe); err != nil {
				log.Printf("select transport: %s", err)
			}
		}
	}()
	return g, nil
}

func (g *proxyGroup) probe(i int, kind string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// default tcp test
	if kind != "urltest" {
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

	ch := make(chan *http.Response, 1)
	go func() {
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			debugf("read resp: %s", err)
			return
		}
		ch <- resp
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-ch:
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return errors.New("unexpected status: " + resp.Status)
		}
		return nil
	}
}

func (g *proxyGroup) Select(probe probeConfig) error {
	timeout := 3 * time.Second
	if probe.Timeout > 0 {
		timeout = time.Duration(probe.Timeout) * time.Second
	}

	var winner = -1
	var minLatency time.Duration
	for i, u := range g.urls {
		start := time.Now()
		if err := g.probe(i, probe.Type, timeout); err != nil {
			debugf("probe %s: %s", u.Host, err)
			continue
		}
		duration := time.Since(start)
		if minLatency == 0 || duration < minLatency {
			minLatency = duration
			winner = i
		}
		debugf("probe %s in %dms", u.Host, duration.Milliseconds())
	}
	if winner == -1 {
		return errors.New("no available proxy")
	}

	g.mu.RLock()
	if g.idx == winner {
		g.mu.RUnlock()
		return nil
	}
	g.mu.RUnlock()

	g.mu.Lock()
	g.idx = winner
	g.mu.Unlock()
	log.Printf("select server: %s", g.urls[winner].Host)
	return nil
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
	debugf("connect to %s", addr)
	g.mu.RLock()
	dialer := g.dialers[g.idx]
	g.mu.RUnlock()
	return dialer.DialContext(ctx, network, addr)
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
