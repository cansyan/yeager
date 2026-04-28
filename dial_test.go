package main

import (
	"net"
	"net/url"
	"testing"
)

func TestSelectDoesNotThrottleRetriesAfterFailedProbeRound(t *testing.T) {
	addr1 := unusedLocalAddr(t)
	addr2 := unusedLocalAddr(t)

	u1, err := url.Parse("socks5://" + addr1)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := url.Parse("socks5://" + addr2)
	if err != nil {
		t.Fatal(err)
	}

	g := &proxyGroup{
		urls:  []*url.URL{u1, u2},
		stats: []*ServerStat{newServerStat(1000), newServerStat(1000)},
	}

	err = g.Select(probeConfig{Type: "tcp", Timeout: 1, Interval: 60})
	if err == nil {
		t.Fatal("Select() error = nil, want no reachable server")
	}
	if got := g.lastSelect.Load(); got != 0 {
		t.Fatalf("lastSelect = %d, want 0 after failed probe round", got)
	}
	if !g.needRefresh() {
		t.Fatal("needRefresh() = false, want true after failed probe round")
	}
}

func unusedLocalAddr(t *testing.T) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
