package grpc

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"

	"github.com/cansyan/yeager/logger"
	"github.com/cansyan/yeager/transport/grpc/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

const idleTimeout = 10 * time.Minute

// NewServer starts a gRPC server for forword proxy.
// The caller should call Stop when finished.
func NewServer(addr string, config *tls.Config) (*grpc.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(config)),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: idleTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: keepaliveInterval,
		}),
	)
	pb.RegisterTunnelServer(s, service{})
	go func() {
		err := s.Serve(listener)
		if err != nil {
			logger.Error.Print(err)
		}
	}()
	return s, nil
}

type service struct {
	pb.UnimplementedTunnelServer
}

func (service) Stream(stream pb.Tunnel_StreamServer) error {
	if stream.Context().Err() != nil {
		return stream.Context().Err()
	}
	v := metadata.ValueFromIncomingContext(stream.Context(), addressKey)
	if len(v) == 0 {
		return errors.New("missing address")
	}
	address := v[0]

	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	ss := serverStream{stream}
	go func() {
		ss.WriteTo(conn)
		conn.(*net.TCPConn).CloseWrite()
	}()
	ss.ReadFrom(conn)
	return nil
}

// serverStream implements io.WriterTo and io.ReaderFrom as optimizations
// so copying to or from it can avoid allocating unnecessary buffers.
type serverStream struct {
	pb.Tunnel_StreamServer
}

var _ io.WriterTo = serverStream{}
var _ io.ReaderFrom = serverStream{}

// WriteTo uses buffer received from grpc stream, instead of allocating a new one
func (ss serverStream) WriteTo(w io.Writer) (written int64, err error) {
	for {
		msg, er := ss.Recv()
		if msg != nil && len(msg.Data) > 0 {
			nr := len(msg.Data)
			nw, ew := w.Write(msg.Data)
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
	return written, err
}

func (ss serverStream) ReadFrom(r io.Reader) (n int64, err error) {
	buf := bufPool.Get().(*[]byte)
	for {
		nr, er := r.Read(*buf)
		if nr > 0 {
			n += int64(nr)
			ew := ss.Send(&pb.Message{Data: (*buf)[:nr]})
			if ew != nil {
				err = ew
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
	return n, err
}
