package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/tunnel"
)

// testServer brings up the whole API on a temporary directory.
func testServer(t *testing.T) (*httptest.Server, string) {
	ts, _, dyn := testServerWithPages(t)
	return ts, dyn
}

// testServerWithPages also returns the static server, so tests can check
// what gets published.
func testServerWithPages(t *testing.T) (*httptest.Server, *pages.Server, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	signer, err := auth.LoadOrCreateSigner(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	opts := traefik.Options{DataDir: dir}
	if err := os.MkdirAll(opts.DynamicDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:    st,
		Signer:   signer,
		Traefik:  opts,
		Frontend: fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),

		Pages:        &pages.Server{},
		PagesStorage: &pages.Storage{Root: filepath.Join(dir, "sites"), Limits: pages.DefaultLimits},
		StaticURL:    "http://127.0.0.1:8099",
		MaxUploadMB:  1,
	}
	s.DataDir = dir
	s.Hub = &tunnel.Hub{OnConnect: s.OnAgentConnect}
	s.Restart = func() { s.Log.Info("restart requested") }
	s.Secrets, _ = secrets.New(make([]byte, 32))
	s.Git = gitauth.New(st, s.Secrets)
	s.Notify = &notify.Dispatcher{Store: st, Secrets: s.Secrets, Log: s.Log}
	s.Apps = apps.New(apps.Config{Store: st, Secrets: s.Secrets, Providers: s.Git, LogDir: filepath.Join(dir, "builds"), Remote: s.RemoteDocker,
		Log: s.Log, Apply: s.Apply})
	s.Builds = builder.New(builder.Config{
		Store: st, Secrets: s.Secrets, Providers: s.Git, Pages: s.PagesStorage, LogDir: filepath.Join(dir, "builds"),
		Log: s.Log, Publish: s.Publish, Timeout: time.Minute,
	})
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Builds.Run(runCtx)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	testServers.Store(ts, s)
	return ts, s.Pages, opts.DynamicDir()
}

// testServers maps each test server to its *Server, for the tests that call
// its methods directly.
var testServers sync.Map

func serverOf(t *testing.T, ts *httptest.Server) *Server {
	t.Helper()
	s, ok := testServers.Load(ts)
	if !ok {
		t.Fatal("unknown test server")
	}
	return s.(*Server)
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, ts *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}}
}

// do sends a request and returns the status code and the decoded body.
func (c *client) do(method, path, body string, header ...string) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

const adminJSON = `{"name":"Admin","email":"admin@test.com","password":"secret123"}`

func TestAuthFlow(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)

	if code, _ := c.do("GET", "/api/proxy-hosts", ""); code != http.StatusUnauthorized {
		t.Fatalf("no session: expected 401, got %d", code)
	}
	if code, body := c.do("GET", "/api/setup", ""); code != 200 || body["needs_setup"] != true {
		t.Fatalf("setup pending: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/setup", adminJSON); code != http.StatusCreated {
		t.Fatalf("setup: expected 201, got %d", code)
	}
	if code, _ := c.do("POST", "/api/setup", adminJSON); code != http.StatusConflict {
		t.Fatalf("second setup: expected 409, got %d", code)
	}
	if code, body := c.do("GET", "/api/auth/me", ""); code != 200 || body["email"] != "admin@test.com" {
		t.Fatalf("me: %d %v", code, body)
	}

	other := newClient(t, ts)
	if code, _ := other.do("POST", "/api/auth/login", `{"email":"admin@test.com","password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: expected 401, got %d", code)
	}
	if code, _ := other.do("POST", "/api/auth/login", `{"email":"nadie@test.com","password":"x"}`); code != http.StatusUnauthorized {
		t.Fatalf("user doesn't exist: expected 401, got %d", code)
	}
	if code, _ := other.do("POST", "/api/auth/login", `{"email":"ADMIN@test.com","password":"secret123"}`); code != 200 {
		t.Fatalf("login (case-insensitive email): expected 200, got %d", code)
	}
}

func TestRoutingErrorsAreJSON(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)

	if code, body := c.do("GET", "/api/does-not-exist", ""); code != 404 || body["error"] == nil {
		t.Errorf("404: %d %v", code, body)
	}
	if code, body := c.do("PATCH", "/api/setup", "{}"); code != 405 || body["error"] == nil {
		t.Errorf("405: %d %v", code, body)
	}
	// An HTML form on another site can't send JSON: that blocks CSRF.
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/login", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("form POST: expected 415, got %d", resp.StatusCode)
	}
}

func TestSPAFallback(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/proxy-hosts/edit")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "spa") {
		t.Errorf("client-side routes must serve index.html: %d %q", resp.StatusCode, body)
	}
}

func TestLoginRateLimitIgnoresSpoofedHeaders(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)

	var last int
	for i := range authAttempts + 1 {
		// Each attempt pretends to come from a different IP: that must get it
		// nowhere, because no trusted proxies are configured.
		last, _ = c.do("POST", "/api/auth/login", `{"email":"a@b.com","password":"x"}`,
			"X-Forwarded-For", "203.0.113."+string(rune('1'+i%9)))
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after %d attempts expected 429, got %d", authAttempts+1, last)
	}
}

func TestProxyHostCRUD(t *testing.T) {
	ts, dynDir := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	host := `{"domains":["App.Test.com","app.test.com"],"target_type":"external","forward_host":"10.0.0.5","forward_port":8080}`
	code, body := c.do("POST", "/api/proxy-hosts", host)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, body)
	}
	ph := body["proxy_host"].(map[string]any)
	if d := ph["domains"].([]any); len(d) != 1 || d[0] != "app.test.com" {
		t.Errorf("the domains must be normalized and deduplicated: %v", d)
	}

	cfg, err := os.ReadFile(filepath.Join(dynDir, "dockfly.yml"))
	if err != nil || !strings.Contains(string(cfg), "Host(`app.test.com`)") ||
		!strings.Contains(string(cfg), "http://10.0.0.5:8080") {
		t.Fatalf("the Traefik configuration doesn't reflect the new host (%v):\n%s", err, cfg)
	}

	if code, _ := c.do("POST", "/api/proxy-hosts", host); code != http.StatusConflict {
		t.Errorf("duplicate domain: expected 409, got %d", code)
	}
	if code, _ := c.do("POST", "/api/proxy-hosts",
		`{"domains":["x.com"],"target_type":"external","forward_host":"http://1.1.1.1/","forward_port":80}`); code != 400 {
		t.Errorf("host with a scheme: expected 400, got %d", code)
	}
	if code, _ := c.do("PUT", "/api/proxy-hosts/abc", host); code != 400 {
		t.Errorf("non-numeric id: expected 400, got %d", code)
	}
	if code, _ := c.do("PUT", "/api/proxy-hosts/999", host); code != 404 {
		t.Errorf("editing one that doesn't exist: expected 404, got %d", code)
	}

	// Editing the host itself with its own domains isn't a conflict.
	updated := strings.Replace(host, "8080", "9090", 1)
	if code, body := c.do("PUT", "/api/proxy-hosts/1", updated); code != 200 {
		t.Fatalf("edit: %d %v", code, body)
	}
	cfg, _ = os.ReadFile(filepath.Join(dynDir, "dockfly.yml"))
	if !strings.Contains(string(cfg), "http://10.0.0.5:9090") {
		t.Errorf("the edit wasn't applied in Traefik:\n%s", cfg)
	}

	if code, _ := c.do("DELETE", "/api/proxy-hosts/1", ""); code != http.StatusNoContent {
		t.Errorf("delete: expected 204, got %d", code)
	}
	cfg, _ = os.ReadFile(filepath.Join(dynDir, "dockfly.yml"))
	if strings.Contains(string(cfg), "app.test.com") {
		t.Errorf("the deleted host is still in Traefik:\n%s", cfg)
	}
}
