package proxy

import (
	"context"
	"net"

	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/transport/shadowsocks"
)

type ssDialer struct {
	*shadowsocks.StreamDialer
}

func (d *ssDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.DialStream(ctx, addr)
}

func resolvedTCPEndpoint(addr *ResolvedAddr) transport.StreamEndpoint {
	return transport.FuncStreamEndpoint(func(ctx context.Context) (transport.StreamConn, error) {
		proxyAddr, err := addr.Address(ctx)
		if err != nil {
			return nil, err
		}
		var dialer transport.TCPDialer
		return dialer.DialStream(ctx, proxyAddr)
	})
}

// Shadowsocks returns a ContextDialer that makes Shadowsocks connections to the given address.
func Shadowsocks(address, method, password string) (ContextDialer, error) {
	key, err := shadowsocks.NewEncryptionKey(method, password)
	if err != nil {
		return nil, err
	}
	endpoint := resolvedTCPEndpoint(GetCachedAddr(address))
	d, err := shadowsocks.NewStreamDialer(endpoint, key)
	if err != nil {
		return nil, err
	}
	return &ssDialer{StreamDialer: d}, nil
}
