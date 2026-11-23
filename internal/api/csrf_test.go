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

	// Security headers on the panel.
	resp, _ := c.http.Get(ts.URL + "/api/auth/me")
	for h, want := range map[string]string{
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "frame-ancestors 'none'",
	} {
		if got := resp.Header.Get(h); !strings.Contains(got, want) {
			t.Errorf("%s = %q, wanted it to contain %q", h, got, want)
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
	// A page on another subdomain (same-site) or on another site (cross-site)
	// can't trigger actions with the admin's cookie.
	if code := req("same-site"); code != http.StatusForbidden {
		t.Errorf("CSRF same-site: %d, wanted 403", code)
	}
	if code := req("cross-site"); code != http.StatusForbidden {
		t.Errorf("CSRF cross-site: %d, wanted 403", code)
	}
	// The SPA itself (same-origin) and browser-less clients (the CLI, with no
	// Sec-Fetch-Site) do pass the CSRF filter (404 because the app doesn't exist).
	if code := req("same-origin"); code == http.StatusForbidden {
		t.Errorf("same-origin must not be blocked: %d", code)
	}
	if code := req(""); code == http.StatusForbidden {
		t.Errorf("a request from outside a browser must not be blocked: %d", code)
	}
}
