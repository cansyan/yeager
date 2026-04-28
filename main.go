package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
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
		subs   string
	}
	flag.StringVar(&flags.config, "c", "", "config file")
	flag.BoolVar(&verbose, "v", false, "verbose logging")
	flag.StringVar(&flags.listen, "listen", "", "socks://host:port")
	flag.StringVar(&flags.proxy, "proxy", "", "ss://method:password@host:port")
	flag.StringVar(&flags.subs, "subs", "", "convert JMS subscription to proxy url")
	flag.Parse()

	if flags.config == "" && flags.listen == "" && flags.subs == "" {
		flag.Usage()
		return
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if flags.subs != "" {
		urls, err := getSubscription(flags.subs)
		if err != nil {
			fmt.Printf("convert subscription: %s", err)
			return
		}
		bs, _ := json.MarshalIndent(urls, "", "    ")
		fmt.Println(string(bs))
		return
	}

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

	dialer, err := newProxyGroup(conf)
	if err != nil {
		log.Printf("init dialer: %s", err)
		return
	}

	for _, lnURL := range conf.Listen {
		u, err := url.Parse(lnURL)
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
		log.Printf("listen %s", lnURL)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	log.Printf("exiting on %s", sig)
}

// get JMS subscription
func getSubscription(url string) ([]string, error) {
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("unexpected status: " + resp.Status)
	}
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	bs, err = base64.RawURLEncoding.DecodeString(string(bs))
	if err != nil {
		return nil, err
	}
	rawurls := strings.Split(string(bs), "\n")
	results := make([]string, 0, len(rawurls))
	for _, rawURL := range rawurls {
		// ss://base64urlencode(method:password@host:port)#tag
		if rest, ok := strings.CutPrefix(rawURL, "ss://"); ok {
			rest, _, _ = strings.Cut(rest, "#")
			bs, err := base64.RawURLEncoding.DecodeString(rest)
			if err != nil {
				return nil, err
			}
			results = append(results, "ss://"+string(bs))
			continue
		}
		// vmess://base64urlencode({"ps":"tag","port":"port","id":"uuid","aid":0,"net":"tcp","type":"none","tls":"none","add":"host"})
		if rest, ok := strings.CutPrefix(rawURL, "vmess://"); ok {
			bs, err := base64.RawURLEncoding.DecodeString(rest)
			if err != nil {
				return nil, err
			}
			var data vmessConfig
			if err := json.Unmarshal(bs, &data); err != nil {
				return nil, err
			}
			results = append(results, fmt.Sprintf("vmess://auto:%s@%s:%s", data.ID, data.Add, data.Port))
		}
	}
	return results, nil
}

type vmessConfig struct {
	ID   string `json:"id"`
	Add  string `json:"add"`
	Port string `json:"port"`
}
