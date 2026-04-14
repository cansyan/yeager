package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var verbose bool

func debugf(format string, a ...any) {
	if !verbose {
		return
	}
	log.Default().Output(2, fmt.Sprintf(format, a...))
}

func main() {
	var flags struct {
		config string
		listen string
		proxy  string
	}
	flag.StringVar(&flags.config, "c", "", "config file")
	flag.BoolVar(&verbose, "v", false, "verbose logging")
	flag.StringVar(&flags.listen, "listen", "", "socks://host:port")
	flag.StringVar(&flags.proxy, "proxy", "", "ss://method:password@host:port")
	flag.Parse()

	if flags.config == "" && flags.listen == "" {
		flag.Usage()
		return
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var conf Config
	if flags.config != "" {
		bs, err := os.ReadFile(flags.config)
		if err != nil {
			log.Printf("read config: %s", err)
			return
		}
		if err = json.Unmarshal(bs, &conf); err != nil {
			log.Printf("load config: %s", err)
			return
		}
	}
	if flags.listen != "" {
		s := strings.Split(flags.listen, ",")
		conf.Listen = append(conf.Listen, s...)
	}
	if flags.proxy != "" {
		conf.Proxy = append(conf.Proxy, flags.proxy)
	}

	if len(conf.Listen) == 0 || len(conf.Proxy) == 0 {
		log.Print("invalid config")
		return
	}

	proxyUrls := make([]*url.URL, 0, len(conf.Proxy))
	for _, proxyURL := range conf.Proxy {
		u, err := url.Parse(proxyURL)
		if err != nil {
			log.Printf("invalid proxy url: %s", err)
			return
		}
		if u.Host == "" {
			log.Printf("missing proxy address: %s", proxyURL)
			return
		}
		proxyUrls = append(proxyUrls, u)
	}

	dialer, err := newDialerGroup(proxyUrls, conf.Bypass, conf.Block, conf.URLTest)
	if err != nil {
		log.Printf("init dialer: %s", err)
		return
	}
	defer dialer.Close()

	for _, proxyURL := range conf.Listen {
		u, err := url.Parse(proxyURL)
		if err != nil {
			log.Print(err)
			return
		}
		switch u.Scheme {
		case "http":
			listener, err := net.Listen("tcp", u.Host)
			if err != nil {
				log.Print(err)
				return
			}
			s := &http.Server{Handler: NewProxyHandler(dialer)}
			go func() {
				err := s.Serve(listener)
				if err != nil && err != http.ErrServerClosed {
					log.Printf("serve http: %s", err)
				}
			}()
			defer s.Close()
		case "socks", "socks5":
			listener, err := net.Listen("tcp", u.Host)
			if err != nil {
				log.Print(err)
				return
			}
			s := NewSOCKSServer(dialer)
			go func() {
				err := s.Serve(listener)
				if err != nil {
					log.Printf("serve socks: %s", err)
				}
			}()
			defer s.Close()
		default:
			log.Print("unsupported protocol: " + u.Scheme)
			return
		}
		log.Printf("listen %s", proxyURL)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	log.Printf("received %s", sig)
}
