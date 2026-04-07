package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cansyan/yeager/transport"
	"github.com/cansyan/yeager/transport/shadowsocks"
	"github.com/cansyan/yeager/transport/vmess"
)

func newStreamDialer(c ServerConfig) (transport.Dialer, error) {
	var dialer transport.Dialer
	switch c.Protocol {
	case ProtoShadowsocks:
		d, err := shadowsocks.NewDialer(c.Address, c.Cipher, c.Secret)
		if err != nil {
			return nil, err
		}
		dialer = d
	case ProtoVMess:
		d, err := vmess.NewDialer(c.Address, c.Secret, c.Cipher, 0)
		if err != nil {
			return nil, err
		}
		dialer = d
	default:
		return nil, errors.New("unsupported protocol: " + c.Protocol)
	}
	return dialer, nil
}

type dialerGroup struct {
	transports []ServerConfig

	mu     sync.RWMutex
	ticker *time.Ticker
	best   ServerConfig
	dialer transport.Dialer

	bypass  *hostMatcher
	block   *hostMatcher
	urltest urltest
}

// newDialerGroup returns a new stream dialer.
// Given multiple transport config, it creates a dialer group to
// perform periodic health checks and switch server if necessary.
func newDialerGroup(transports []ServerConfig, bypass, block string, urltest urltest) (*dialerGroup, error) {
	if len(transports) == 0 {
		return nil, errors.New("missing transport config")
	}

	g := new(dialerGroup)
	if block != "" {
		g.block = parseHostMatcher(block)
	}
	if bypass != "" {
		g.bypass = parseHostMatcher(bypass)
	}

	if len(transports) == 1 {
		d, err := newStreamDialer(transports[0])
		if err != nil {
			return nil, err
		}
		g.dialer = d
		return g, nil
	}

	g.transports = transports
	g.urltest = urltest
	if err := g.Select(); err != nil {
		return nil, err
	}
	interval := urltest.Interval
	if interval == 0 {
		interval = 60
	}
	g.ticker = time.NewTicker(time.Duration(interval) * time.Second)
	go func() {
		for range g.ticker.C {
			if err := g.Select(); err != nil {
				log.Printf("select transport: %s", err)
			}
		}
	}()
	return g, nil
}

func (g *dialerGroup) Select() error {
	var winner transport.Dialer
	var winnerCfg ServerConfig
	var min time.Duration
	for _, t := range g.transports {
		d, err := newStreamDialer(t)
		if err != nil {
			log.Printf("new stream dialer: %s", err)
			continue
		}

		du, err := testURL(d, g.urltest.URL, time.Duration(g.urltest.Timeout)*time.Second)
		if err != nil {
			debugf("url test %s: %s", t.Address, err)
			continue
		}

		if winner == nil || du < min {
			min = du
			winner = d
			winnerCfg = t
		}
		debugf("url test %s %dms", t.Address, du.Milliseconds())
	}
	if winner == nil {
		return errors.New("unable to find a valid transport")
	}

	if g.best.Protocol == winnerCfg.Protocol && g.best.Address == winnerCfg.Address {
		return nil
	}

	g.mu.Lock()
	g.dialer = winner
	g.best = winnerCfg
	g.mu.Unlock()
	log.Printf("selected transport: %s %s", winnerCfg.Protocol, winnerCfg.Address)
	return nil
}

// implements interface transport.StreamDialer
func (g *dialerGroup) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.block != nil && g.block.match(address) {
		return nil, errors.New("host was blocked")
	}
	if g.bypass != nil && g.bypass.match(address) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		debugf("bypass %s", address)
		return conn.(*net.TCPConn), nil
	}
	if g.dialer == nil {
		return nil, errors.New("no valid dialer")
	}
	stream, err := g.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	debugf("connected to %s", address)
	return stream, nil
}

func (g *dialerGroup) Close() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.ticker != nil {
		g.ticker.Stop()
	}
	if v, ok := g.dialer.(io.Closer); ok {
		return v.Close()
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

// matcher represents the matching rule for a given value in the NO_PROXY list
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

func testURL(d transport.Dialer, url string, timeout time.Duration) (time.Duration, error) {
	if url == "" {
		url = "http://www.gstatic.com/generate_204"
	}
	if timeout == 0 {
		timeout = 3 * time.Second
	}

	client := &http.Client{
		Transport: &http.Transport{DialContext: d.DialContext},
		Timeout:   timeout,
	}
	defer client.CloseIdleConnections()
	start := time.Now()

	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusNoContent {
		return 0, errors.New("unexpected status code: " + resp.Status)
	}
	io.Copy(io.Discard, resp.Body)
	return elapsed, nil
}
