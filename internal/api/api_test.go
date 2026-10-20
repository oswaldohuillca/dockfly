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
	"testing"
	"testing/fstest"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

// testServer levanta la API completa sobre un directorio temporal.
func testServer(t *testing.T) (*httptest.Server, string) {
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
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, opts.DynamicDir()
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

// do envía una petición y devuelve el código y el cuerpo decodificado.
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

const adminJSON = `{"name":"Admin","email":"admin@test.com","password":"secreto123"}`

func TestAuthFlow(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)

	if code, _ := c.do("GET", "/api/proxy-hosts", ""); code != http.StatusUnauthorized {
		t.Fatalf("sin sesión: esperaba 401, obtuve %d", code)
	}
	if code, body := c.do("GET", "/api/setup", ""); code != 200 || body["needs_setup"] != true {
		t.Fatalf("setup pendiente: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/setup", adminJSON); code != http.StatusCreated {
		t.Fatalf("setup: esperaba 201, obtuve %d", code)
	}
	if code, _ := c.do("POST", "/api/setup", adminJSON); code != http.StatusConflict {
		t.Fatalf("segundo setup: esperaba 409, obtuve %d", code)
	}
	if code, body := c.do("GET", "/api/auth/me", ""); code != 200 || body["email"] != "admin@test.com" {
		t.Fatalf("me: %d %v", code, body)
	}

	other := newClient(t, ts)
	if code, _ := other.do("POST", "/api/auth/login", `{"email":"admin@test.com","password":"mala"}`); code != http.StatusUnauthorized {
		t.Fatalf("contraseña incorrecta: esperaba 401, obtuve %d", code)
	}
	if code, _ := other.do("POST", "/api/auth/login", `{"email":"nadie@test.com","password":"x"}`); code != http.StatusUnauthorized {
		t.Fatalf("usuario inexistente: esperaba 401, obtuve %d", code)
	}
	if code, _ := other.do("POST", "/api/auth/login", `{"email":"ADMIN@test.com","password":"secreto123"}`); code != 200 {
		t.Fatalf("login (email sin distinguir mayúsculas): esperaba 200, obtuve %d", code)
	}
}

func TestRoutingErrorsAreJSON(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)

	if code, body := c.do("GET", "/api/no-existe", ""); code != 404 || body["error"] == nil {
		t.Errorf("404: %d %v", code, body)
	}
	if code, body := c.do("PATCH", "/api/setup", "{}"); code != 405 || body["error"] == nil {
		t.Errorf("405: %d %v", code, body)
	}
	// Un formulario HTML de otro sitio no puede enviar JSON: bloquea CSRF.
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/login", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("form POST: esperaba 415, obtuve %d", resp.StatusCode)
	}
}

func TestSPAFallback(t *testing.T) {
	ts, _ := testServer(t)
	resp, err := http.Get(ts.URL + "/proxy-hosts/editar")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "spa") {
		t.Errorf("las rutas del cliente deben servir index.html: %d %q", resp.StatusCode, body)
	}
}

func TestLoginRateLimitIgnoresSpoofedHeaders(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)

	var last int
	for i := range authAttempts + 1 {
		// Cada intento finge venir de una IP distinta: no debe servir de nada,
		// porque no hay proxies de confianza configurados.
		last, _ = c.do("POST", "/api/auth/login", `{"email":"a@b.com","password":"x"}`,
			"X-Forwarded-For", "203.0.113."+string(rune('1'+i%9)))
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("tras %d intentos esperaba 429, obtuve %d", authAttempts+1, last)
	}
}

func TestProxyHostCRUD(t *testing.T) {
	ts, dynDir := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	host := `{"domains":["App.Test.com","app.test.com"],"target_type":"external","forward_host":"10.0.0.5","forward_port":8080}`
	code, body := c.do("POST", "/api/proxy-hosts", host)
	if code != http.StatusCreated {
		t.Fatalf("crear: %d %v", code, body)
	}
	ph := body["proxy_host"].(map[string]any)
	if d := ph["domains"].([]any); len(d) != 1 || d[0] != "app.test.com" {
		t.Errorf("los dominios deben normalizarse y deduplicarse: %v", d)
	}

	cfg, err := os.ReadFile(filepath.Join(dynDir, "dockfly.yml"))
	if err != nil || !strings.Contains(string(cfg), "Host(`app.test.com`)") ||
		!strings.Contains(string(cfg), "http://10.0.0.5:8080") {
		t.Fatalf("la configuración de Traefik no refleja el host creado (%v):\n%s", err, cfg)
	}

	if code, _ := c.do("POST", "/api/proxy-hosts", host); code != http.StatusConflict {
		t.Errorf("dominio duplicado: esperaba 409, obtuve %d", code)
	}
	if code, _ := c.do("POST", "/api/proxy-hosts",
		`{"domains":["x.com"],"target_type":"external","forward_host":"http://1.1.1.1/","forward_port":80}`); code != 400 {
		t.Errorf("host con esquema: esperaba 400, obtuve %d", code)
	}
	if code, _ := c.do("PUT", "/api/proxy-hosts/abc", host); code != 400 {
		t.Errorf("id no numérico: esperaba 400, obtuve %d", code)
	}
	if code, _ := c.do("PUT", "/api/proxy-hosts/999", host); code != 404 {
		t.Errorf("editar inexistente: esperaba 404, obtuve %d", code)
	}

	// Editar el propio host con sus dominios no es un conflicto.
	updated := strings.Replace(host, "8080", "9090", 1)
	if code, body := c.do("PUT", "/api/proxy-hosts/1", updated); code != 200 {
		t.Fatalf("editar: %d %v", code, body)
	}
	cfg, _ = os.ReadFile(filepath.Join(dynDir, "dockfly.yml"))
	if !strings.Contains(string(cfg), "http://10.0.0.5:9090") {
		t.Errorf("la edición no se aplicó en Traefik:\n%s", cfg)
	}

	if code, _ := c.do("DELETE", "/api/proxy-hosts/1", ""); code != http.StatusNoContent {
		t.Errorf("eliminar: esperaba 204, obtuve %d", code)
	}
	cfg, _ = os.ReadFile(filepath.Join(dynDir, "dockfly.yml"))
	if strings.Contains(string(cfg), "app.test.com") {
		t.Errorf("el host eliminado sigue en Traefik:\n%s", cfg)
	}
}
