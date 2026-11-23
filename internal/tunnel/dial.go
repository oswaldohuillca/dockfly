package tunnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
)

// dialURL opens a TCP connection (or TLS for https) to u's host.
func dialURL(ctx context.Context, u *url.URL) (net.Conn, error) {
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	var d net.Dialer
	if u.Scheme != "https" {
		return d.DialContext(ctx, "tcp", host)
	}
	td := tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: u.Hostname(), NextProtos: []string{"http/1.1"}}}
	return td.DialContext(ctx, "tcp", host)
}
