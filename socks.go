package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/shadowsocks/go-shadowsocks2/socks"
	"golang.org/x/net/proxy"
)

type socksServer struct {
	ln     net.Listener
	dialer proxy.ContextDialer
}

// NewSOCKSServer returns a new SOCKS5 proxy server that intends
// to be a local proxy and does not require authentication.
// Caller should call Close when finished.
func NewSOCKSServer(dialer proxy.ContextDialer) *socksServer {
	return &socksServer{dialer: dialer}
}

// Serve serves connection accepted by ln,
// blocking until the server closes or encounters an unexpected error.
func (s *socksServer) Serve(ln net.Listener) error {
	s.ln = ln
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				err = nil
			}
			return err
		}

		go func(conn net.Conn) {
			defer conn.Close()
			addr, err := socks.Handshake(conn)
			if err != nil {
				log.Printf("handshake: %s", err)
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := s.dialer.DialContext(ctx, "tcp", addr.String())
			if err != nil {
				log.Printf("connect %s: %s", addr, err)
				return
			}
			defer stream.Close()

			if err = relay(conn, stream); err != nil {
				debugf("relay: %s", err)
			}
		}(conn)
	}
}

func (s *socksServer) Close() error {
	return s.ln.Close()
}

type closeWriter interface {
	CloseWrite() error
}

// relay copies data between streams bidirectionally
func relay(a, b net.Conn) error {
	wait := 5 * time.Second
	errc := make(chan error, 1)
	go func() {
		_, err := io.Copy(a, b)
		// unblock read on a
		if i, ok := a.(closeWriter); ok {
			i.CloseWrite()
		} else {
			a.SetReadDeadline(time.Now().Add(wait))
		}
		errc <- err
	}()
	_, err := io.Copy(b, a)
	// unblock read on b
	if i, ok := b.(closeWriter); ok {
		i.CloseWrite()
	} else {
		b.SetReadDeadline(time.Now().Add(wait))
	}
	err2 := <-errc

	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		return err
	}
	if err2 != nil && !errors.Is(err2, os.ErrDeadlineExceeded) {
		return err2
	}
	return nil
}
