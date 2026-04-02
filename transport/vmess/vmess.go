package vmess

import (
	"context"
	"net"

	V "github.com/sagernet/sing-vmess"
	M "github.com/sagernet/sing/common/metadata"
)

type dialer struct {
	proxyAddr string
	client    *V.Client
}

func (d *dialer) DialContext(ctx context.Context, network, raddr string) (net.Conn, error) {
	conn, err := net.Dial(network, d.proxyAddr)
	if err != nil {
		return nil, err
	}

	cc, err := d.client.DialConn(conn, M.ParseSocksaddr(raddr))
	if err != nil {
		conn.Close()
		return nil, err
	}
	return cc, nil
}

func NewDialer(addr, userID, security string, alterID int) (*dialer, error) {
	client, err := V.NewClient(userID, security, alterID)
	if err != nil {
		return nil, err
	}
	return &dialer{proxyAddr: addr, client: client}, nil
}
