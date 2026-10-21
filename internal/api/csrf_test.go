package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestCrossOriginAndHeaders(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	// Cabeceras de seguridad en el panel.
	resp, _ := c.http.Get(ts.URL + "/api/auth/me")
	for h, want := range map[string]string{
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "frame-ancestors 'none'",
	} {
		if got := resp.Header.Get(h); !strings.Contains(got, want) {
			t.Errorf("%s = %q, quería contener %q", h, got, want)
		}
	}
	resp.Body.Close()

	req := func(site string) int {
		r, _ := http.NewRequest("POST", ts.URL+"/api/apps/1/stop", nil)
		for _, ck := range c.http.Jar.Cookies(r.URL) {
			r.AddCookie(ck)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
			r.Header.Set("Origin", "https://evil.example.com")
		}
		resp, err := c.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// Una página de otro subdominio (same-site) o de otro sitio (cross-site)
	// no puede disparar acciones con la cookie del admin.
	if code := req("same-site"); code != http.StatusForbidden {
		t.Errorf("CSRF same-site: %d, quería 403", code)
	}
	if code := req("cross-site"); code != http.StatusForbidden {
		t.Errorf("CSRF cross-site: %d, quería 403", code)
	}
	// La propia SPA (same-origin) y los clientes sin navegador (CLI, sin
	// Sec-Fetch-Site) sí pasan el filtro CSRF (404 porque la app no existe).
	if code := req("same-origin"); code == http.StatusForbidden {
		t.Errorf("same-origin no debe bloquearse: %d", code)
	}
	if code := req(""); code == http.StatusForbidden {
		t.Errorf("una petición sin navegador no debe bloquearse: %d", code)
	}
}
