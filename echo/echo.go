package echo

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/cansyan/yeager/logger"
)

// Server accepts connection and writes back anything it reads.
type Server struct {
	Listener net.Listener
	running  sync.WaitGroup
}

func (s *Server) Serve() {
	s.running.Add(1)
	defer s.running.Done()
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			if s != nil && !errors.Is(err, net.ErrClosed) {
				logger.Error.Print(err)
			}
			return
		}
		s.running.Add(1)
		go func() {
			defer s.running.Done()
			io.Copy(conn, conn)
			conn.Close()
		}()
	}
}

func (e *Server) Close() error {
	err := e.Listener.Close()
	e.running.Wait()
	return err
}

// NewServer starts a local TCP server for testing,
// it sends back whatever it receives.
func NewServer() *Server {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	e := Server{Listener: listener}
	go e.Serve()
	return &e
}
