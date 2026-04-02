package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chenen3/yeager/config"
	"github.com/chenen3/yeager/logger"
	"github.com/chenen3/yeager/proxy"
	"github.com/chenen3/yeager/transport"
	"github.com/chenen3/yeager/transport/grpc"
	"github.com/chenen3/yeager/transport/http2"
	"github.com/chenen3/yeager/transport/https"
	"github.com/chenen3/yeager/transport/shadowsocks"
	"github.com/chenen3/yeager/transport/vmess"
)

// start the service specified by config.
// The caller should call stop when finished.
func start(cfg config.Config) (stop func(), err error) {
	var onStop []func() error
	defer func() {
		if err != nil {
			for _, f := range onStop {
				if e := f(); e != nil {
					logger.Error.Print(e)
				}
			}
		}
	}()

	if len(cfg.Transport) == 0 && len(cfg.Listen) == 0 {
		return nil, errors.New("missing client and server config")
	}

	var dialer transport.Dialer
	getDialer := func() (transport.Dialer, error) {
		if dialer != nil {
			return dialer, nil
		}
		if len(cfg.Transport) == 0 {
			return nil, errors.New("missing transport config")
		}
		d, err := newDialerGroup(cfg.Transport, cfg.Bypass, cfg.Block)
		if err != nil {
			return nil, err
		}
		if v, ok := d.(io.Closer); ok {
			onStop = append(onStop, v.Close)
		}
		dialer = d
		return dialer, nil
	}

	for _, c := range cfg.Listen {
		switch c.Protocol {
		case config.ProtoHTTP:
			dialer, err := getDialer()
			if err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", c.Address)
			if err != nil {
				return nil, err
			}
			s := &http.Server{Handler: proxy.NewHTTPHandler(dialer)}
			go func() {
				err := s.Serve(listener)
				if err != nil && err != http.ErrServerClosed {
					logger.Error.Printf("serve http: %s", err)
				}
			}()
			onStop = append(onStop, s.Close)
		case config.ProtoSOCKS5:
			dialer, err := getDialer()
			if err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", c.Address)
			if err != nil {
				return nil, err
			}
			s := proxy.NewSOCKS5Server(dialer)
			go func() {
				err := s.Serve(listener)
				if err != nil {
					logger.Error.Printf("serve socks5: %s", err)
				}
			}()
			onStop = append(onStop, s.Close)
		case config.ProtoGRPC:
			tlsConf, err := c.ServerTLS()
			if err != nil {
				return nil, err
			}
			s, err := grpc.NewServer(c.Address, tlsConf)
			if err != nil {
				return nil, err
			}
			onStop = append(onStop, func() error {
				s.Stop()
				return nil
			})
		case config.ProtoHTTP2:
			tlsConf, err := c.ServerTLS()
			if err != nil {
				return nil, err
			}
			s, err := http2.NewServer(c.Address, tlsConf, c.Username, c.Password)
			if err != nil {
				return nil, err
			}
			onStop = append(onStop, s.Close)
		default:
			return nil, errors.New("unknown protocol: " + c.Protocol)
		}
		logger.Info.Printf("listen %s %s", c.Protocol, c.Address)
	}

	stop = func() {
		for _, f := range onStop {
			if e := f(); e != nil {
				logger.Error.Print(e)
			}
		}
	}
	return stop, nil
}

func newStreamDialer(c config.ServerConfig) (transport.Dialer, error) {
	var dialer transport.Dialer
	switch c.Protocol {
	case config.ProtoGRPC:
		tlsConf, err := c.ClientTLS()
		if err != nil {
			return nil, err
		}
		dialer = grpc.NewStreamDialer(c.Address, tlsConf)
	case config.ProtoHTTP2:
		if c.Username != "" {
			dialer = http2.NewStreamDialer(c.Address, nil, c.Username, c.Password)
		} else {
			tlsConf, err := c.ClientTLS()
			if err != nil {
				return nil, err
			}
			dialer = http2.NewStreamDialer(c.Address, tlsConf, "", "")
		}
	case config.ProtoShadowsocks:
		d, err := shadowsocks.NewDialer(c.Address, c.Cipher, c.Secret)
		if err != nil {
			return nil, err
		}
		dialer = d

	case config.ProtoVMess:
		d, err := vmess.NewDialer(c.Address, c.Secret, c.Cipher, 0)
		if err != nil {
			return nil, err
		}
		dialer = d

	case config.ProtoHTTP:
		dialer = https.NewDialer(c.Address)
	default:
		return nil, errors.New("unsupported transport protocol: " + c.Protocol)
	}
	return dialer, nil
}

type dialerGroup struct {
	transports []config.ServerConfig
	mu         sync.RWMutex
	dialer     transport.Dialer
	bypass     *hostMatcher
	block      *hostMatcher
	ticker     *time.Ticker
}

// newDialerGroup returns a new stream dialer.
// Given multiple transport config, it creates a dialer group to
// perform periodic health checks and switch server if necessary.
func newDialerGroup(transports []config.ServerConfig, bypass, block string) (transport.Dialer, error) {
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
	g.ticker = time.NewTicker(30 * time.Second)
	go func() {
		g.pick()
		for range g.ticker.C {
			g.pick()
		}
	}()
	return g, nil
}

func (g *dialerGroup) pick() {
	var winner transport.Dialer
	var winnerCfg config.ServerConfig
	var min time.Duration
	for _, t := range g.transports {
		d, err := newStreamDialer(t)
		if err != nil {
			logger.Error.Printf("new stream dialer: %s", err)
			continue
		}

		du, err := testConnection(d)
		if err != nil {
			logger.Debug.Printf("test connection through %s: %s", t.Address, err)
			continue
		}

		if winner == nil {
			min = du
			winner = d
			winnerCfg = t
		} else if du < min {
			if v, ok := winner.(io.Closer); ok {
				v.Close()
			}
			min = du
			winner = d
			winnerCfg = t
		} else {
			if v, ok := d.(io.Closer); ok {
				v.Close()
			}
		}
		logger.Debug.Printf("test connection through %s %dms", t.Address, du.Milliseconds())
	}
	if winner == nil {
		logger.Error.Println("unable to find a valid transport")
		return
	}

	if v, ok := g.dialer.(io.Closer); ok {
		v.Close()
	}
	g.mu.Lock()
	g.dialer = winner
	g.mu.Unlock()
	logger.Info.Printf("selected transport: %s %s", winnerCfg.Protocol, winnerCfg.Address)
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
		logger.Debug.Printf("connected to %s, bypass proxy", address)
		return conn.(*net.TCPConn), nil
	}
	if g.dialer == nil {
		return nil, errors.New("no valid dialer")
	}
	stream, err := g.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	logger.Debug.Printf("connected to %s", address)
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

func testConnection(d transport.Dialer) (time.Duration, error) {
	client := &http.Client{
		Transport: &http.Transport{DialContext: d.DialContext},
		Timeout:   5 * time.Second,
	}
	defer client.CloseIdleConnections()
	start := time.Now()
	resp, err := client.Get("http://www.gstatic.com/generate_204")
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
