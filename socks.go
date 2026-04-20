package main

import (
	"context"
	"errors"
	"log"
	"net"
	"time"

	"github.com/cansyan/yeager/transport"
	"github.com/shadowsocks/go-shadowsocks2/socks"
)

type socksServer struct {
	ln     net.Listener
	dialer transport.ContextDialer
}

// NewSOCKSServer returns a new SOCKS5 proxy server that intends
// to be a local proxy and does not require authentication.
// Caller should call Close when finished.
func NewSOCKSServer(dialer transport.ContextDialer) *socksServer {
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
