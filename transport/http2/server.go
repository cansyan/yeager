package http2

import (
	"crypto/subtle"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cansyan/yeager/logger"
)

// NewServer starts a HTTP/2 Server for forward proxying.
// The caller should call Close when finished.
func NewServer(addr string, cfg *tls.Config, username, password string) (*http.Server, error) {
	cfg.NextProtos = []string{"h2"}
	lis, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}

	var h handler
	if username != "" {
		h.auth = []byte("Basic " + basicAuth(username, password))
	}
	s := &http.Server{
		Handler:     h,
		IdleTimeout: 10 * time.Minute,
	}
	go func() {
		err := s.Serve(lis)
		if err != nil && err != http.ErrServerClosed {
			logger.Error.Print(err)
		}
	}()
	return s, nil
}

type handler struct {
	auth []byte
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 {
		w.WriteHeader(http.StatusHTTPVersionNotSupported)
		return
	}
	if r.Method != http.MethodConnect {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Host == "" {
		http.Error(w, "missing host", http.StatusBadRequest)
		return
	}
	if len(h.auth) != 0 {
		auth := r.Header.Get("Proxy-Authorization")
		prefix := "Basic "
		if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) ||
			subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), h.auth[len(prefix):]) != 1 {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	// client is waiting for response header
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	conn, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
	if err != nil {
		logger.Error.Print(err)
		return
	}
	defer conn.Close()
	go func() {
		bufferedCopy(conn, r.Body)
		conn.(*net.TCPConn).CloseWrite()
	}()
	bufferedCopy(flushWriter{w}, conn)
}

type flushWriter struct {
	http.ResponseWriter
}

func (w flushWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, err
}
