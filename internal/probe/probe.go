// Package probe checks whether a container is ready to take traffic.
package probe

import (
	"context"
	"net"
	"net/http"
	"time"
)

var client = &http.Client{
	Timeout: 3 * time.Second,
	// A redirect (to /login, for example) already proves the app responds.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Ready reports whether hostport accepts connections or, with path, whether
// it answers a GET with a status below 400.
func Ready(ctx context.Context, hostport, path string) bool {
	if path == "" {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", hostport)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostport+path, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}
