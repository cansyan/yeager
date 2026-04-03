package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/cansyan/yeager/logger"
	"github.com/cansyan/yeager/transport"
)

type socks5Server struct {
	mu         sync.Mutex
	lis        net.Listener
	activeConn map[net.Conn]struct{}
	dialer     transport.Dialer
}

// NewSOCKS5Server returns a new SOCKS5 proxy server that intends
// to be a local proxy and does not require authentication.
// The call should call Close when finished.
func NewSOCKS5Server(dialer transport.Dialer) *socks5Server {
	return &socks5Server{dialer: dialer}
}

// Serve serves connection accepted by lis,
// blocking until the server closes or encounters an unexpected error.
func (s *socks5Server) Serve(lis net.Listener) error {
	s.mu.Lock()
	s.lis = lis
	s.mu.Unlock()
	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				err = nil
			}
			return err
		}

		s.trackConn(conn, true)
		go s.handleConn(conn)
	}
}

func (s *socks5Server) handleConn(proxyConn net.Conn) {
	defer s.trackConn(proxyConn, false)
	defer proxyConn.Close()

	proxyConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	addr, err := handshake(proxyConn)
	if err != nil {
		logger.Error.Printf("handshake: %s", err)
		return
	}
	proxyConn.SetReadDeadline(time.Time{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := s.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		logger.Error.Printf("connect %s: %s", addr, err)
		return
	}
	defer stream.Close()

	err = transport.Relay(proxyConn, stream)
	if err != nil {
		logger.Debug.Printf("relay: %s", err)
	}
}

func (s *socks5Server) trackConn(c net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeConn == nil {
		s.activeConn = make(map[net.Conn]struct{})
	}
	if add {
		s.activeConn[c] = struct{}{}
	} else {
		delete(s.activeConn, c)
	}
}

func (s *socks5Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.lis != nil {
		err = s.lis.Close()
	}
	for c := range s.activeConn {
		c.Close()
	}
	return err
}

// maxAddrLen is the maximum size of SOCKS address in bytes.
const maxAddrLen = 1 + 1 + 255 + 2

const (
	cmdConnect = 0x01
	// cmdUDPAssociate = 0x03
)

const (
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

// Refer to https://datatracker.ietf.org/doc/html/rfc1928
func handshake(rw io.ReadWriter) (addr string, err error) {
	buf := [maxAddrLen]byte{}
	// read VER, NMETHODS, METHODS
	if _, err = io.ReadFull(rw, buf[:2]); err != nil {
		return "", err
	}
	if version := buf[0]; version != 0x05 {
		return "", fmt.Errorf("unsupported verison number: %d", version)
	}
	nmethods := buf[1]
	if _, err = io.ReadFull(rw, buf[:nmethods]); err != nil {
		return "", err
	}

	// reply VER METHOD
	// it is fine to reply no auth when serving locally
	noAuth := []byte{0x05, 0x00}
	if _, err = rw.Write(noAuth); err != nil {
		return "", err
	}
	// read VER CMD RSV ATYP DST.ADDR DST.PORT
	if _, err = io.ReadFull(rw, buf[:3]); err != nil {
		return "", err
	}

	if cmd := buf[1]; cmd != cmdConnect {
		return "", fmt.Errorf("unsupported cmd: %x", cmd)
	}

	addr, err = readSOCKSAddr(rw, buf[:])
	if err != nil {
		return "", err
	}

	// reply VER REP RSV ATYP BND.ADDR BND.PORT
	_, err = rw.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if err != nil {
		return "", err
	}

	return addr, nil
}

// read SOCKS address from r
// bytes order:
//
//	ATYP BND.ADDR BND.PORT
func readSOCKSAddr(r io.Reader, buf []byte) (addr string, err error) {
	if len(buf) < maxAddrLen {
		return "", errors.New("short buffer")
	}
	if _, err = io.ReadFull(r, buf[:1]); err != nil {
		return "", err
	}

	var (
		atyp = buf[0]
		host string
	)
	switch atyp {
	case atypIPv4:
		if _, err = io.ReadFull(r, buf[:net.IPv4len]); err != nil {
			return "", err
		}
		host = net.IPv4(buf[0], buf[1], buf[2], buf[3]).String()
	case atypDomain:
		if _, err = io.ReadFull(r, buf[:1]); err != nil {
			return "", err
		}
		domainLen := buf[0]
		if _, err = io.ReadFull(r, buf[:domainLen]); err != nil {
			return "", err
		}
		host = string(buf[:domainLen])
	case atypIPv6:
		if _, err = io.ReadFull(r, buf[:net.IPv6len]); err != nil {
			return "", err
		}
		ipv6 := make(net.IP, net.IPv6len)
		copy(ipv6, buf[:net.IPv6len])
		host = ipv6.String()
	}

	if _, err = io.ReadFull(r, buf[:2]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(buf[:2])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}
