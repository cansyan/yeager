package proxy

import (
	"errors"
	"net/url"

	"golang.org/x/net/proxy"
)

type ContextDialer proxy.ContextDialer

// FromURL returns a ContextDialer from the given URL.
func FromURL(u *url.URL) (ContextDialer, error) {
	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	switch u.Scheme {
	case "ss":
		return Shadowsocks(u.Host, user, pass)
	case "vmess":
		return Vmess(u.Host, user, pass)
	case "socks5":
		// Reuse the standard SOCKS5 dialer so yeager can interoperate with
		// existing SOCKS5-compatible proxies, including naiveproxy.
		var auth *proxy.Auth
		if user != "" || pass != "" {
			auth = &proxy.Auth{User: user, Password: pass}
		}
		d, err := proxy.SOCKS5("tcp", u.Host, auth, nil)
		if err != nil {
			return nil, err
		}
		return d.(ContextDialer), nil
	default:
		return nil, errors.New("unknown url: " + u.String())
	}
}
