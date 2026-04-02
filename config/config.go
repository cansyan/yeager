package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
)

type Config struct {
	Listen    []ServerConfig `json:"listen,omitempty"`    // supports http, socks5, grpc and h2 protocols
	Transport []ServerConfig `json:"transport,omitempty"` // supports grpc, h2 and shadowsocks protocols

	// Bypass specifies a string that contains comma-separated values
	// specifying hosts that should be excluded from proxying. Each value is
	// represented by an IP address (1.2.3.4), an IP address in
	// CIDR notation (1.2.3.4/8), a domain name, or a special DNS label (*).
	// A domain name matches that name and all subdomains.
	// A single asterisk (*) indicates that no proxying should be done.
	// A best effort is made to parse the string and errors are ignored.
	Bypass string `json:"bypass,omitempty"`

	// Block specifies a string that contains comma-separated values
	// specifying hosts that should be blocked from proxying.
	// Block has priority over Bypass.
	Block string `json:"block,omitempty"`
}

const (
	ProtoHTTP   = "http"
	ProtoSOCKS5 = "socks5"

	ProtoGRPC        = "grpc"
	ProtoHTTP2       = "h2"
	ProtoShadowsocks = "ss"
	ProtoVMess       = "vmess"
)

type ServerConfig struct {
	Protocol string `json:"protocol,omitempty"`
	Address  string `json:"address,omitempty"`

	// for TLS
	CertPEM []string `json:"cert_pem,omitempty"`
	KeyPEM  []string `json:"key_pem,omitempty"`
	CAPEM   []string `json:"ca_pem,omitempty"`

	// for h2
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// for shadowsocks, vmess
	Cipher string `json:"cipher,omitempty"`
	Secret string `json:"secret,omitempty"`
}

func mergeLine(s []string) string {
	return strings.Join(s, "\n")
}

func splitLine(s string) []string {
	return strings.Split(strings.TrimSpace(s), "\n")
}

func (s ServerConfig) ClientTLS() (*tls.Config, error) {
	if s.CertPEM == nil {
		return nil, errors.New("no certificate")
	}
	cert := []byte(mergeLine(s.CertPEM))
	if s.KeyPEM == nil {
		return nil, errors.New("no key")
	}
	key := []byte(mergeLine(s.KeyPEM))
	if s.CAPEM == nil {
		return nil, errors.New("no CA")
	}
	ca := []byte(mergeLine(s.CAPEM))
	return newClientTLSConfig(ca, cert, key)
}

func (s ServerConfig) ServerTLS() (*tls.Config, error) {
	if s.CertPEM == nil {
		return nil, errors.New("no certificate")
	}
	cert := []byte(mergeLine(s.CertPEM))
	if s.KeyPEM == nil {
		return nil, errors.New("no key")
	}
	key := []byte(mergeLine(s.KeyPEM))
	if s.CAPEM == nil {
		return nil, errors.New("no CA")
	}
	ca := []byte(mergeLine(s.CAPEM))
	return newServerTLSConfig(ca, cert, key)
}

// Generate returns a pair of client and server configuration for the given host
func Generate(host string) (cli, srv Config, err error) {
	cert, err := newCert(host)
	if err != nil {
		return
	}

	proxyPort := 57175
	srv = Config{
		Listen: []ServerConfig{
			{
				Address:  fmt.Sprintf("0.0.0.0:%d", proxyPort),
				Protocol: ProtoGRPC,
				CAPEM:    splitLine(string(cert.rootCert)),
				CertPEM:  splitLine(string(cert.serverCert)),
				KeyPEM:   splitLine(string(cert.serverKey)),
			},
		},
	}

	cli = Config{
		Listen: []ServerConfig{
			{Protocol: ProtoHTTP, Address: "127.0.0.1:8080"},
			{Protocol: ProtoSOCKS5, Address: "127.0.0.1:1080"},
		},
		Transport: []ServerConfig{
			{
				Address:  fmt.Sprintf("%s:%d", host, proxyPort),
				Protocol: ProtoGRPC,
				CAPEM:    splitLine(string(cert.rootCert)),
				CertPEM:  splitLine(string(cert.clientCert)),
				KeyPEM:   splitLine(string(cert.clientKey)),
			},
		},
	}
	return cli, srv, nil
}
