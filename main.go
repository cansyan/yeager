package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cansyan/yeager/logger"
)

func main() {
	var flags struct {
		config  string
		listen  string
		proxy   string
		verbose bool
		help    bool
	}
	flag.StringVar(&flags.config, "c", "", "path to config file")
	flag.BoolVar(&flags.verbose, "v", false, "verbose logging")
	flag.StringVar(&flags.listen, "listen", "", "socks5 url")
	flag.StringVar(&flags.proxy, "proxy", "", "ss://method:password@host:port")
	flag.BoolVar(&flags.help, "help", false, "")
	flag.Parse()

	if flags.help {
		flag.Usage()
		return
	}
	if flags.verbose {
		logger.Debug.SetOutput(os.Stderr)
	}
	if flags.config == "" && flags.listen == "" {
		flag.Usage()
		return
	}

	var conf Config
	if flags.config != "" {
		bs, err := os.ReadFile(flags.config)
		if err != nil {
			logger.Error.Printf("read config: %s", err)
			return
		}
		if err = json.Unmarshal(bs, &conf); err != nil {
			logger.Error.Printf("load config: %s", err)
			return
		}
	}
	if flags.listen != "" {
		s := strings.Split(flags.listen, ",")
		conf.Listen = append(conf.Listen, s...)
	}
	if flags.proxy != "" {
		u, err := url.Parse(flags.proxy)
		if err != nil {
			fmt.Printf("parse proxy url: %s", err)
			return
		}

		proxyConf := ServerConfig{
			Protocol: u.Scheme,
			Address:  u.Host,
			Cipher:   u.User.Username(),
		}
		if pass, ok := u.User.Password(); !ok {
			fmt.Printf("missing password")
			return
		} else {
			proxyConf.Secret = pass
		}
		conf.Proxy = append(conf.Proxy, proxyConf)
	}

	if len(conf.Listen) == 0 || len(conf.Proxy) == 0 {
		logger.Error.Print("invalid config")
		return
	}

	dialer, err := newDialerGroup(conf.Proxy, conf.Bypass, conf.Block, conf.URLTest)
	if err != nil {
		logger.Error.Printf("init dialer: %s", err)
		return
	}
	defer dialer.Close()

	for _, proxyURL := range conf.Listen {
		u, err := url.Parse(proxyURL)
		if err != nil {
			logger.Error.Print(err)
			return
		}
		switch u.Scheme {
		case "http":
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
