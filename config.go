package main

type Config struct {
	Listen []string       `json:"listen,omitempty"` // local http or socks5 proxy
	Proxy  []ServerConfig `json:"proxy,omitempty"`  // remote proxy server

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

	URLTest urltest `json:"urltest,omitempty"`
}

const (
	ProtoShadowsocks = "ss"
	ProtoVMess       = "vmess"
)

type ServerConfig struct {
	Protocol string `json:"protocol,omitempty"`
	Address  string `json:"address,omitempty"`
	Cipher   string `json:"cipher,omitempty"` // encrytion method
	Secret   string `json:"secret,omitempty"`
}

type urltest struct {
	Interval  int    `json:"interval,omitempty"`  // seconds
	Timeout   int    `json:"timeout,omitempty"`   // seconds
	Tolerance int    `json:"tolerance,omitempty"` // milliseconds
	URL       string `json:"url,omitempty"`
}
