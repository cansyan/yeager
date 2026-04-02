package http2

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cansyan/yeager/config"
	"github.com/cansyan/yeager/echo"
)

func run() (*http.Server, *dialer, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	lis.Close()

	cliTLSConf, srvTLSConf, err := config.MutualTLS("127.0.0.1")
	if err != nil {
		return nil, nil, err
	}
	ts, err := NewServer(lis.Addr().String(), srvTLSConf, "", "")
	if err != nil {
		return nil, nil, err
	}
	td := NewStreamDialer(lis.Addr().String(), cliTLSConf, "", "")
	return ts, td, nil
}

func TestHTTP2Connect(t *testing.T) {
	e := echo.NewServer()
	defer e.Close()
	ts, td, err := run()
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	defer td.Close()

	time.Sleep(time.Millisecond * 100)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := td.DialContext(ctx, "tcp", e.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	want := []byte{1}
	got := make([]byte, len(want))
	if _, we := stream.Write(want); we != nil {
		t.Fatalf("write data: %s", we)
	}
	n, re := stream.Read(got)
	if re != nil && re != io.EOF {
		t.Fatalf("read data: %s", re)
	}
	if n != len(want) {
		t.Fatalf("got %d bytes, want %d bytes", n, len(want))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAuth(t *testing.T) {
	es := echo.NewServer()
	defer es.Close()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lis.Close()

	cliTLSConf, srvTLSConf, err := config.MutualTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	user, pass := "u", "p"
	ts, err := NewServer(lis.Addr().String(), srvTLSConf, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()

	td := NewStreamDialer(lis.Addr().String(), cliTLSConf, user, pass)
	defer td.Close()

	time.Sleep(time.Millisecond * 100)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := td.DialContext(ctx, "tcp", es.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	want := []byte{1}
	got := make([]byte, len(want))
	if _, we := stream.Write(want); we != nil {
		t.Fatalf("write data: %s", we)
	}
	n, re := stream.Read(got)
	if re != nil && re != io.EOF {
		t.Fatalf("read data: %s", re)
	}
	if n != len(want) {
		t.Fatalf("got %d bytes, want %d bytes", n, len(want))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBadAuth(t *testing.T) {
	es := echo.NewServer()
	defer es.Close()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lis.Close()

	cliTLSConf, srvTLSConf, err := config.MutualTLS("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	ts, err := NewServer(lis.Addr().String(), srvTLSConf, "u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()

	td := NewStreamDialer(lis.Addr().String(), cliTLSConf, "fakeuser", "fakepass")
	defer td.Close()

	time.Sleep(time.Millisecond * 100)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = td.DialContext(ctx, "tcp", es.Listener.Addr().String())
	if err == nil {
		t.Fatalf("expected error for mismatch auth")
	}

	td2 := NewStreamDialer(lis.Addr().String(), cliTLSConf, "", "")
	defer td2.Close()
	time.Sleep(time.Millisecond * 100)
	_, err = td2.DialContext(ctx, "tcp", es.Listener.Addr().String())
	if err == nil {
		t.Fatalf("expected error for empty auth")
	}
}

func BenchmarkThroughput(b *testing.B) {
	es := echo.NewServer()
	defer es.Close()

	ts, td, err := run()
	if err != nil {
		b.Fatal(err)
	}
	defer ts.Close()
	defer td.Close()
	// waiting for the server to start
	time.Sleep(time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rwc, err := td.DialContext(ctx, "tcp", es.Listener.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer rwc.Close()

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
			rwc.Write(up)
		}
	}()
	for i := 0; i < b.N; i++ {
		io.ReadFull(rwc, down)
	}
	b.StopTimer()
	elapsed := time.Since(start)

	megabits := 8 * n * b.N / 1e6
	b.ReportMetric(float64(megabits)/elapsed.Seconds(), "mbps")
}
