package http2

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"sync"
	"time"

	"github.com/cansyan/yeager/transport"
)

type dialer struct {
	proxyAddr string
	username  string
	password  string
	client    *http.Client
}

var _ transport.Dialer = (*dialer)(nil)

// NewStreamDialer returns a new transport.StreamDialer that dials through the provided
// proxy server's address.
func NewStreamDialer(addr string, cfg *tls.Config, username, password string) *dialer {
	d := &dialer{
		proxyAddr: addr,
		username:  username,
		password:  password,
	}

	// mitigate website fingerprinting via multiplexing of HTTP/2 ,
	// the fewer connections the better
	d.client = &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig:     cfg,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
		},
	}
	return d
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (d *dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodConnect, "https://"+d.proxyAddr, pr)
	if err != nil {
		return nil, err
	}
	req.Host = address
	if d.username != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+basicAuth(d.username, d.password))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		dump, err := httputil.DumpResponse(resp, true)
		if err != nil {
			return nil, fmt.Errorf("failed to connect, status code: %s", resp.Status)
		}
		return nil, fmt.Errorf("failed to connect, response: %q", dump)
	}
	return &stream{writer: pw, reader: resp.Body}, nil
}

func (d *dialer) Close() error {
	d.client.CloseIdleConnections()
	return nil
}

type stream struct {
	writer *io.PipeWriter
	reader io.ReadCloser

	writeDeadline time.Time
	readDeadline  time.Time
}

func (s *stream) Read(p []byte) (n int, err error) {
	if !s.readDeadline.IsZero() && time.Now().After(s.readDeadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return s.reader.Read(p)
}

func (s *stream) Write(p []byte) (n int, err error) {
	if !s.writeDeadline.IsZero() && time.Now().After(s.writeDeadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return s.writer.Write(p)
}

func (s *stream) Close() error {
	s.writer.Close()
	return s.reader.Close()
}

func (s *stream) CloseWrite() error {
	return s.writer.Close()
}

func (s *stream) ReadFrom(r io.Reader) (n int64, err error) {
	return bufferedCopy(s.writer, r)
}

func (s *stream) WriteTo(w io.Writer) (written int64, err error) {
	return bufferedCopy(w, s.reader)
}

func (s *stream) SetDeadline(t time.Time) error {
	s.readDeadline = t
	s.writeDeadline = t
	return nil
}

func (s *stream) SetReadDeadline(t time.Time) error {
	s.readDeadline = t
	return nil
}

func (s *stream) SetWriteDeadline(t time.Time) error {
	s.writeDeadline = t
	return nil
}

func (s *stream) LocalAddr() net.Addr {
	return nil
}

func (s *stream) RemoteAddr() net.Addr {
	return nil
}

var bufPool = sync.Pool{
	New: func() any {
		// refer to 16KB maxPlaintext in crypto/tls/common.go
		b := make([]byte, 16*1024)
		return &b
	},
}

// copy data from src to dst using buffer pool
func bufferedCopy(dst io.Writer, src io.Reader) (written int64, err error) {
	buf := bufPool.Get().(*[]byte)
	for {
		nr, er := src.Read(*buf)
		if nr > 0 {
			nw, ew := dst.Write((*buf)[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	bufPool.Put(buf)
	return written, err
}
