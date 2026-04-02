package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/cansyan/yeager/proxy"
	"github.com/cansyan/yeager/transport/https"
)

func localAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	ln.Close()
	return ln.Addr().String()
}

func TestHTTPTransport(t *testing.T) {
	// client request -> [http proxy server A -> http transport] -> http proxy server B -> http test server
	hostport := localAddr()
	proxySrvA := &http.Server{Addr: localAddr(), Handler: proxy.NewHTTPHandler(https.NewDialer(hostport))}
	go proxySrvA.ListenAndServe()
	defer proxySrvA.Close()

	proxySrvB := &http.Server{Addr: hostport, Handler: proxy.NewHTTPHandler(&net.Dialer{})}
	go proxySrvB.ListenAndServe()
	defer proxySrvB.Close()

	testSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "1")
	}))
	defer testSrv.Close()

	client := http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: proxySrvA.Addr}),
		},
		Timeout: time.Second,
	}

	// the proxy services may not started yet
	time.Sleep(time.Millisecond)

	resp, err := client.Get(testSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(bs) != "1" {
		t.Fatalf("want 1, got %s", bs)
	}
}
