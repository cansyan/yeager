package proxy

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestResolvedAddrCachesAndRefreshes(t *testing.T) {
	addr := NewResolvedAddr("proxy.example:443")
	now := time.Unix(100, 0)
	addr.ttl = time.Minute
	addr.now = func() time.Time { return now }

	lookups := 0
	addr.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		switch lookups {
		case 1:
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		case 2:
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.11")}}, nil
		default:
			t.Fatalf("unexpected lookup %d", lookups)
			return nil, nil
		}
	}

	got, err := addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.10:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	now = now.Add(30 * time.Second)
	got, err = addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.10:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if lookups != 1 {
		t.Fatalf("got %d lookups, want 1", lookups)
	}

	now = now.Add(31 * time.Second)
	got, err = addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.11:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if lookups != 2 {
		t.Fatalf("got %d lookups, want 2", lookups)
	}
}

func TestResolvedAddrKeepsStaleAddressOnRefreshFailure(t *testing.T) {
	addr := NewResolvedAddr("proxy.example:443")
	now := time.Unix(100, 0)
	addr.ttl = time.Minute
	addr.now = func() time.Time { return now }

	lookups := 0
	addr.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		if lookups == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		}
		return nil, errors.New("dns failed")
	}

	got, err := addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.10:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	now = now.Add(2 * time.Minute)
	got, err = addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.10:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolvedAddrLeavesIPsUntouched(t *testing.T) {
	addr := NewResolvedAddr("127.0.0.1:1080")

	got, err := addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:1080"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolvedAddrReturnsStaleWhileRefreshIsRunning(t *testing.T) {
	addr := NewResolvedAddr("proxy.example:443")
	now := time.Unix(100, 0)
	addr.ttl = time.Minute
	addr.now = func() time.Time { return now }

	var (
		mu       sync.Mutex
		lookups  int
		release  chan struct{}
		started  chan struct{}
		started1 sync.Once
	)
	addr.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		mu.Lock()
		lookups++
		current := lookups
		mu.Unlock()
		if current == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		}
		started1.Do(func() { close(started) })
		<-release
		return []net.IPAddr{{IP: net.ParseIP("203.0.113.11")}}, nil
	}

	got, err := addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.10:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	now = now.Add(2 * time.Minute)
	release = make(chan struct{})
	started = make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		_, err := addr.Address(context.Background())
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}

	got, err = addr.Address(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := "203.0.113.10:443"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not finish")
	}
}
