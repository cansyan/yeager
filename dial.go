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
	urls     []*url.URL
	dialers  []proxy.ContextDialer
	mu       sync.RWMutex // guards idx
	idx      int          // current dialer index
	stats    []*ServerStat
	probeCfg probeConfig

	muSelect   sync.Mutex // guard selectNow()
	lastSelect atomic.Int64

	bypass *hostMatcher
	block  *hostMatcher
}

const (
	defaultProbeInterval = 30
	defaultProbeTimeout  = 3
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
	g.probeCfg = pc
	if c.Block != "" {
		g.block = parseHostMatcher(c.Block)
	}
	if c.Bypass != "" {
		g.bypass = parseHostMatcher(c.Bypass)
	}

	g.urls = make([]*url.URL, len(c.Proxy))
	g.dialers = make([]proxy.ContextDialer, len(c.Proxy))
	g.stats = make([]*ServerStat, len(c.Proxy))

	rttMax := pc.Timeout * 1000
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
		g.stats[i] = newServerStat(rttMax)
	}
	if len(g.dialers) == 1 {
		return g, nil
	}

	if err := g.trySelect(); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *proxyGroup) probe(i int, kind string, timeout time.Duration) (int, error) {
	// warm up DNS lookup
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	addr, err := proxy.GetCachedAddr(g.urls[i].Host).Address(ctx)
	if err != nil {
		return 0, err
	}

	// default tcp test
	start := time.Now()
	if kind == "" || kind == "tcp" {
		var d net.Dialer
		conn, derr := d.DialContext(ctx, "tcp", addr)
		if derr != nil {
			return 0, derr
		}
		conn.Close()
		return int(time.Since(start).Milliseconds()), nil
	}

	// url test
	const testURL, testHost = "http://www.gstatic.com/generate_204", "www.gstatic.com:80"
	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return 0, err
	}
	conn, err := g.dialers[i].DialContext(ctx, "tcp", testHost)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if err = req.Write(conn); err != nil {
		return 0, err
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
		return 0, ctx.Err()
	case err := <-ch:
		if err != nil {
			return 0, err
		}
		return int(time.Since(start).Milliseconds()), nil
	}
}

type probeResult struct {
	i     int
	score int
	err   error
}

func (g *proxyGroup) Select(probe probeConfig) error {
	g.mu.RLock()
	current := g.idx
	g.mu.RUnlock()

	bestIdx := -1
	bestScore := 0
	results := make(chan probeResult, len(g.urls))

	for i, u := range g.urls {
		go func(i int, u *url.URL) {
			latency, err := g.probe(i, probe.Type, time.Duration(probe.Timeout)*time.Second)
			g.stats[i].Put(latency, err != nil)
			score := g.stats[i].Score()
			debugf("probe %s in %dms, score: %d, err: %v", u.Host, latency, score, err)
			results <- probeResult{i: i, score: score, err: err}
		}(i, u)
	}

	for range g.urls {
		r := <-results
		if r.err != nil {
			continue
		}
		if bestScore == 0 || r.score < bestScore {
			bestScore = r.score
			bestIdx = r.i
		}
	}

	if bestIdx == -1 {
		return errors.New("no reachable server")
	}
	g.lastSelect.Store(time.Now().Unix())
	if current == bestIdx {
		return nil
	}

	g.mu.Lock()
	g.idx = bestIdx
	g.mu.Unlock()
	log.Printf("select server: %s", g.urls[bestIdx].Host)
	return nil
}

func (g *proxyGroup) trySelect() error {
	g.muSelect.Lock()
	defer g.muSelect.Unlock()

	if !g.needRefresh() {
		return nil
	}

	return g.Select(g.probeCfg)
}

func (g *proxyGroup) needRefresh() bool {
	lastSelect := g.lastSelect.Load()
	if lastSelect == 0 {
		return true
	}
	return time.Since(time.Unix(lastSelect, 0)) >= time.Duration(g.probeCfg.Interval)*time.Second
}

// DialContext implements interface proxy.ContextDialer.
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

	if len(g.dialers) == 1 {
		return g.dialers[0].DialContext(ctx, network, addr)
	}

	// Refresh selection in the background to avoid adding probe latency to the
	// current dial. The tradeoff is that the first request after a refresh
	// interval may still use the previous backend once before failover applies.
	if g.needRefresh() {
		go g.trySelect()
	}

	g.mu.RLock()
	i := g.idx
	g.mu.RUnlock()
	d := g.dialers[i]
	debugf("connect to %s", addr)
	return d.DialContext(ctx, network, addr)
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
