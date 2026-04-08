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
	lis    net.Listener
	dialer transport.Dialer
}

// NewSOCKSServer returns a new SOCKS5 proxy server that intends
// to be a local proxy and does not require authentication.
// Caller should call Close when finished.
func NewSOCKSServer(dialer transport.Dialer) *socksServer {
	return &socksServer{dialer: dialer}
}

// Serve serves connection accepted by lis,
// blocking until the server closes or encounters an unexpected error.
func (s *socksServer) Serve(lis net.Listener) error {
	s.lis = lis
	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				err = nil
			}
			return err
		}

		go s.handle(conn)
	}
}

func (s *socksServer) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	addr, err := socks.Handshake(conn)
	if err != nil {
		log.Printf("handshake: %s", err)
		return
	}
	conn.SetReadDeadline(time.Time{})

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
}

func (s *socksServer) Close() error {
	return s.lis.Close()
}
