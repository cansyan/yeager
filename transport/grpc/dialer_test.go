package grpc

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/cansyan/yeager/config"
	"github.com/cansyan/yeager/echo"
)

func TestTunnel(t *testing.T) {
	e := echo.NewServer()
	defer e.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	addr := listener.Addr().String()
	cliTLSConf, srvTLSConf, err := config.MutualTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := NewServer(addr, srvTLSConf)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Stop()
	td := NewStreamDialer(addr, cliTLSConf)
	defer td.Close()
	// the tunnel server may not started yet
	time.Sleep(time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := td.DialContext(ctx, "tcp", e.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want := []byte{1}
	got := make([]byte, len(want))
	if _, err := stream.Write(want); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func BenchmarkThroughput(b *testing.B) {
	echo := echo.NewServer()
	defer echo.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	listener.Close()
	addr := listener.Addr().String()
	cliTLSConf, srvTLSConf, err := config.MutualTLS("127.0.0.1")
	if err != nil {
		b.Fatal(err)
	}
	ts, err := NewServer(addr, srvTLSConf)
	if err != nil {
		b.Fatal(err)
	}
	defer ts.Stop()
	td := NewStreamDialer(addr, cliTLSConf)
	defer td.Close()
	// the tunnel server may not started yet
	time.Sleep(time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := td.DialContext(ctx, "tcp", echo.Listener.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer stream.Close()

	const n = 1000
	up := make([]byte, n)
	for i := 0; i < n; i++ {
		up[i] = byte(i)
	}
	down := make([]byte, n)
	start := time.Now()
	b.ResetTimer()
	go func() {
		for i := 0; i < b.N; i++ {
			stream.Write(up)
		}
	}()
	for i := 0; i < b.N; i++ {
		io.ReadFull(stream, down)
	}
	b.StopTimer()
	elapsed := time.Since(start)

	megabits := 8 * n * b.N / 1e6
	b.ReportMetric(float64(megabits)/elapsed.Seconds(), "mbps")
}
