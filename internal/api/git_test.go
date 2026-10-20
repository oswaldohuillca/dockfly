package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub simula la API de GitHub para el repo oswa/web y registra los
// webhooks creados y borrados.
type fakeGitHub struct {
	*httptest.Server
	mu      sync.Mutex
	hooks   map[string]string // id → URL
	deleted []string
	nextID  int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{hooks: map[string]string{}, nextID: 100}
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{"oswa-web-sha1/site/index.html": "desde git", "oswa-web-sha1/README.md": "x"} {
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))})
		io.WriteString(tw, content)
	}
	tw.Close()
	gz.Close()

	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_bueno" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		// Cualquier host distinto de api.github.com se trata como GitHub
		// Enterprise, cuya API cuelga de /api/v3.
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v3")
		switch {
		case r.URL.Path == "/user":
			io.WriteString(w, `{"login":"oswa"}`)
		case r.URL.Path == "/user/repos":
			io.WriteString(w, `[{"full_name":"oswa/web","default_branch":"main"}]`)
		case r.URL.Path == "/repos/oswa/web/commits/main":
			io.WriteString(w, `{"sha":"sha1","commit":{"message":"Hola"}}`)
		case strings.HasPrefix(r.URL.Path, "/repos/oswa/web/tarball/"):
			w.Write(tgz.Bytes())
		case r.URL.Path == "/repos/oswa/web/hooks" && r.Method == "POST":
			body, _ := io.ReadAll(r.Body)
			f.nextID++
			id := fmt.Sprint(f.nextID)
			f.hooks[id] = string(body)
			fmt.Fprintf(w, `{"id":%s}`, id)
		case strings.HasPrefix(r.URL.Path, "/repos/oswa/web/hooks/") && r.Method == "DELETE":
			f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/repos/oswa/web/hooks/"))
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// waitBuild espera a que el build termine y devuelve su estado.
func waitBuild(t *testing.T, c *client, id int) map[string]any {
	t.Helper()
	for range 100 {
		_, b := c.do("GET", fmt.Sprintf("/api/builds/%d", id), "")
		if s := b["status"]; s == "success" || s == "failed" || s == "canceled" {
			return b
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("el build no terminó")
	return nil
}

func TestGitIntegrationFlow(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, static, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	// 1. Vincular la cuenta: el token se valida antes de guardarse.
	if code, body := c.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_malo"}`, gh.URL)); code != 400 ||
		!strings.Contains(body["error"].(string), "token inválido") {
		t.Errorf("token inválido: %d %v", code, body)
	}
	code, body := c.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_bueno"}`, gh.URL))
	if code != 201 || body["username"] != "oswa" {
		t.Fatalf("vincular cuenta: %d %v", code, body)
	}
	if _, has := body["token_enc"]; has {
		t.Error("la API nunca debe devolver el token")
	}
	if code, repos := c.doList("GET", "/api/git/connections/1/repos"); code != 200 || len(repos) != 1 {
		t.Errorf("listar repos: %d %v", code, repos)
	}

	// 2. Enlazar un sitio. Sin URL pública no hay webhook, pero sí builds manuales.
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	link := `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site","env":{"API_URL":"https://x"}}`
	code, body = c.do("PUT", "/api/sites/1/git", link)
	if code != 200 || !strings.Contains(body["webhook_error"].(string), "URL pública") {
		t.Fatalf("enlazar sin URL pública: %d %v", code, body)
	}
	if code, _ := c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"oswa/web","branch":"no-existe"}`); code != 400 {
		t.Errorf("una rama inexistente debe rechazarse: %d", code)
	}
	if code, _ := c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"oswa/web","branch":"main","root_dir":"../etc"}`); code != 400 {
		t.Errorf("un directorio con .. debe rechazarse: %d", code)
	}

	// Con URL pública se registra el webhook en GitHub.
	if code, _ := c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.ejemplo.com/"}`); code != 200 {
		t.Fatal("guardar URL pública")
	}
	code, body = c.do("PUT", "/api/sites/1/git", link)
	if code != 200 || body["webhook_error"] != "" {
		t.Fatalf("enlazar con URL pública: %d %v", code, body)
	}
	gh.mu.Lock()
	hookBody := gh.hooks["101"]
	gh.mu.Unlock()
	if !strings.Contains(hookBody, `"url":"https://dockfly.ejemplo.com/api/hooks/github/1"`) {
		t.Fatalf("el webhook debe apuntar a la URL pública: %s", hookBody)
	}
	_, env := c.do("GET", "/api/sites/1/git/env", "")
	if env["API_URL"] != "https://x" {
		t.Errorf("las variables de entorno deben poder releerse: %v", env)
	}

	// Editar el sitio (nombre, dominios) no debe desenlazar el repo.
	c.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"spa":true}`)
	if _, body := c.do("GET", "/api/sites/1", ""); body["site"].(map[string]any)["repo"] != "oswa/web" {
		t.Fatal("editar el sitio borró la configuración de git")
	}

	// 3. Un push firmado dispara el build y publica el resultado.
	secret := gh.secret(t, "101")
	push := []byte(`{"ref":"refs/heads/main","after":"sha1","head_commit":{"id":"sha1","message":"Hola"}}`)
	hook := func(body []byte, sig, event string) (int, map[string]any) {
		return c.do("POST", "/api/hooks/github/1", string(body), "X-GitHub-Event", event, "X-Hub-Signature-256", sig)
	}
	if code, _ := hook(push, githubSignature("otro", push), "push"); code != 401 {
		t.Errorf("firma inválida: esperaba 401, obtuve %d", code)
	}
	if code, body := hook([]byte(`{"zen":"x"}`), githubSignature(secret, []byte(`{"zen":"x"}`)), "ping"); code != 200 {
		t.Errorf("ping: %d %v", code, body)
	}
	other := []byte(`{"ref":"refs/heads/dev","head_commit":{"id":"x","message":"y"}}`)
	if code, body := hook(other, githubSignature(secret, other), "push"); code != 200 || !strings.Contains(body["status"].(string), "ignorado") {
		t.Errorf("push a otra rama: %d %v", code, body)
	}
	code, body = hook(push, githubSignature(secret, push), "push")
	if code != 202 {
		t.Fatalf("push válido: %d %v", code, body)
	}
	build := waitBuild(t, c, int(body["build_id"].(float64)))
	if build["status"] != "success" || build["trigger"] != "push" {
		t.Fatalf("el build del push debe terminar bien: %v", build)
	}
	if _, html := visit(static, "web.test", "/"); html != "desde git" {
		t.Errorf("debe servirse lo construido, se sirvió %q", html)
	}

	// 4. El log se puede seguir por SSE y termina con el evento "end".
	resp, err := c.http.Get(fmt.Sprintf("%s/api/builds/%d/logs", ts.URL, int(build["id"].(float64))))
	if err != nil {
		t.Fatal(err)
	}
	sse, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(string(sse), "event: status") ||
		!strings.Contains(string(sse), "event: log") || !strings.Contains(string(sse), "event: end") {
		t.Errorf("SSE de logs:\n%s", sse)
	}

	// 5. Build manual: resuelve el último commit de la rama.
	code, body = c.do("POST", "/api/sites/1/builds", "")
	if code != 202 {
		t.Fatalf("build manual: %d %v", code, body)
	}
	if b := waitBuild(t, c, int(body["id"].(float64))); b["status"] != "success" || b["commit_sha"] != "sha1" {
		t.Errorf("build manual: %v", b)
	}
	if _, list := c.doList("GET", "/api/sites/1/builds"); len(list) != 2 {
		t.Errorf("deben listarse 2 builds, hay %d", len(list))
	}

	// 6. La cuenta en uso no se puede borrar; desenlazar borra el webhook.
	if code, _ := c.do("DELETE", "/api/git/connections/1", ""); code != 409 {
		t.Errorf("borrar cuenta en uso: esperaba 409, obtuve %d", code)
	}
	if code, _ := c.do("DELETE", "/api/sites/1/git", ""); code != 204 {
		t.Fatal("desenlazar")
	}
	gh.mu.Lock()
	deleted := strings.Join(gh.deleted, ",")
	gh.mu.Unlock()
	if deleted != "101" {
		t.Errorf("desenlazar debe borrar el webhook en GitHub, se borró %q", deleted)
	}
	if code, _ := hook(push, githubSignature(secret, push), "push"); code != 404 {
		t.Errorf("tras desenlazar, el webhook antiguo debe dar 404, dio %d", code)
	}
	if code, _ := c.do("DELETE", "/api/git/connections/1", ""); code != 204 {
		t.Errorf("borrar cuenta sin uso: %d", code)
	}
}

// secret devuelve el secreto con el que Dockfly registró el webhook, tal
// como lo recibió GitHub.
func (f *fakeGitHub) secret(t *testing.T, id string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var hook struct {
		Config struct {
			Secret string `json:"secret"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(f.hooks[id]), &hook); err != nil || len(hook.Config.Secret) < 32 {
		t.Fatalf("el webhook %s debe registrarse con un secreto robusto: %q %v", id, hook.Config.Secret, err)
	}
	return hook.Config.Secret
}

// doList es como do, para respuestas que son una lista JSON.
func (c *client) doList(method, path string) (int, []any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
