// Package probe comprueba si un contenedor está listo para recibir tráfico.
package probe

import (
	"context"
	"net"
	"net/http"
	"time"
)

var client = &http.Client{
	Timeout: 3 * time.Second,
	// Una redirección (p. ej. a /login) ya demuestra que la app responde.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Ready indica si hostport acepta conexiones o, con path, si responde a un
// GET con un código menor que 400.
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
