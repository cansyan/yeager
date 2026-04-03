package main

import (
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/cansyan/yeager/logger"
	"github.com/cansyan/yeager/transport"
)

func main() {
	var flags struct {
		config  string
		version bool
		verbose bool
	}
	flag.StringVar(&flags.config, "c", "", "path to config file")
	flag.BoolVar(&flags.verbose, "v", false, "verbose logging")
	flag.Parse()

	if flags.verbose {
		logger.Debug.SetOutput(os.Stderr)
	}

	if flags.config == "" {
		flag.Usage()
		return
	}
	bs, err := os.ReadFile(flags.config)
	if err != nil {
		logger.Error.Printf("read config: %s", err)
		return
	}
	var conf Config
	if err = json.Unmarshal(bs, &conf); err != nil {
		logger.Error.Printf("load config: %s", err)
		return
	}

	if len(conf.Listen) == 0 || len(conf.Proxy) == 0 {
		logger.Error.Print("invalid config")
		return
	}

	var dialer transport.Dialer
	getDialer := func() (transport.Dialer, error) {
		if dialer != nil {
			return dialer, nil
		}
		if len(conf.Proxy) == 0 {
			return nil, errors.New("missing transport config")
		}
		d, err := newDialerGroup(conf.Proxy, conf.Bypass, conf.Block, conf.URLTest)
		if err != nil {
			return nil, err
		}
		dialer = d
		return dialer, nil
	}

	for _, proxyURL := range conf.Listen {
		u, err := url.Parse(proxyURL)
		if err != nil {
			logger.Error.Print(err)
			return
		}
		switch u.Scheme {
		case "http":
			dialer, err := getDialer()
			if err != nil {
				logger.Error.Print(err)
				return
			}
			listener, err := net.Listen("tcp", u.Host)
			if err != nil {
				logger.Error.Print(err)
				return
			}
			s := &http.Server{Handler: NewProxyHandler(dialer)}
			go func() {
				err := s.Serve(listener)
				if err != nil && err != http.ErrServerClosed {
					logger.Error.Printf("serve http: %s", err)
				}
			}()
			defer s.Close()
		case "socks5":
			dialer, err := getDialer()
			if err != nil {
				logger.Error.Print(err)
				return
			}
			listener, err := net.Listen("tcp", u.Host)
			if err != nil {
				logger.Error.Print(err)
				return
			}
			s := NewSOCKS5Server(dialer)
			go func() {
				err := s.Serve(listener)
				if err != nil {
					logger.Error.Printf("serve socks5: %s", err)
				}
			}()
			defer s.Close()
		default:
			logger.Error.Print("unsupported protocol: " + u.Scheme)
			return
		}
		logger.Info.Printf("listen %s", proxyURL)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	logger.Info.Printf("received %s", sig)
}
