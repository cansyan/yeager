package main

type Config struct {
	// listen url, for example:
	//   socks://127.0.0.1:1080
	//   http://127.0.0.1:8080
	Listen []string `json:"listen,omitempty"`

	// proxy url, for example:
	//   ss://method:password@host:port
	//   vmess://security:uuid@host:port
	//   socks5://localhost:port
	Proxy []string `json:"proxy,omitempty"`

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

	Probe probe `json:"probe,omitempty"`
}

type probe struct {
	Interval int `json:"interval,omitempty"` // in seconds
	Timeout  int `json:"timeout,omitempty"`  // in seconds
}
