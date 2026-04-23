package proxy

import (
	"context"
	"net"

	vmess "github.com/sagernet/sing-vmess"
	"github.com/sagernet/sing/common/metadata"
)

type vmessDialer struct {
	proxyAddr *ResolvedAddr
	client    *vmess.Client
}

func (d *vmessDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	proxyAddr, err := d.proxyAddr.Address(ctx)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}

	cc, err := d.client.DialConn(conn, metadata.ParseSocksaddr(addr))
	if err != nil {
		conn.Close()
		return nil, err
	}
	return cc, nil
}

// Vmess returns a ContextDialer that makes Vmess connections to the given address.
func Vmess(address, security, userID string) (ContextDialer, error) {
	client, err := vmess.NewClient(userID, security, 0)
	if err != nil {
		return nil, err
	}
	return &vmessDialer{proxyAddr: GetCachedAddr(address), client: client}, nil
}
