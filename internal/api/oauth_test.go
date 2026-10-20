package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeGHE simula GitHub Enterprise: la web y la API (/api/v3) en el mismo host.
func fakeGHE(t *testing.T) *httptest.Server {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "mi-org-web-c1/index.html", Typeflag: tar.TypeReg, Mode: 0o644, Size: 8})
	io.WriteString(tw, "app v1 !")
	tw.Close()
	gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch p := strings.TrimPrefix(r.URL.Path, "/api/v3"); {
		case p == "/app-manifests/CODE1/conversions" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]any{
				"id": 77, "slug": "dockfly-abc", "name": "Dockfly abc", "html_url": "http://ghe/apps/dockfly-abc",
				"client_id": "Iv1.x", "client_secret": "cs", "webhook_secret": "wsecret", "pem": string(pemData),
				"owner": map[string]string{"login": "oswa"},
			})
		case p == "/app/installations/9" && strings.HasPrefix(auth, "Bearer ey"):
			io.WriteString(w, `{"id":9,"account":{"login":"mi-org","type":"Organization"},"html_url":"http://ghe/organizations/mi-org/settings/installations/9"}`)
		case p == "/app/installations/9/access_tokens" && strings.HasPrefix(auth, "Bearer ey"):
			io.WriteString(w, `{"token":"ghs_1","expires_at":"2099-01-01T00:00:00Z"}`)
		case p == "/app/hook/config" && r.Method == "PATCH":
			io.WriteString(w, `{}`)
		case auth != "Bearer ghs_1":
			w.WriteHeader(401)
		case p == "/installation/repositories":
			io.WriteString(w, `{"repositories":[{"full_name":"mi-org/web","default_branch":"main"}]}`)
		case p == "/repos/mi-org/web/commits/main":
			io.WriteString(w, `{"sha":"c1","commit":{"message":"Inicio"}}`)
		case strings.HasPrefix(p, "/repos/mi-org/web/tarball/"):
			w.Write(tgz.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// browser imita la ventana emergente: sigue el flujo sin la cookie de
// sesión (SameSite=Strict no viaja en navegaciones desde otro sitio) y sin
// seguir redirecciones, para inspeccionarlas.
var browser = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func browse(t *testing.T, rawURL string) (*http.Response, string) {
	t.Helper()
	resp, err := browser.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func stateOf(t *testing.T, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Query().Get("state") == "" {
		t.Fatalf("URL sin state: %s", rawURL)
	}
	return u.Query().Get("state")
}

func TestGitHubAppFlow(t *testing.T) {
	ghe := fakeGHE(t)
	ts, static, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.ejemplo.com"}`)

	// 1. El panel pide el manifiesto y lo envía a GitHub.
	code, start := c.do("POST", "/api/git/github/manifest", fmt.Sprintf(`{"panel_url":%q,"enterprise_url":%q}`, ts.URL, ghe.URL))
	if code != 200 {
		t.Fatalf("manifiesto: %d %v", code, start)
	}
	action := start["action"].(string)
	if !strings.HasPrefix(action, ghe.URL+"/settings/apps/new?state=") {
		t.Errorf("el formulario debe ir a la web de GitHub: %s", action)
	}
	var manifest map[string]any
	json.Unmarshal([]byte(start["manifest"].(string)), &manifest)
	if manifest["redirect_url"] != ts.URL+"/api/git/github/manifest" ||
		manifest["hook_attributes"].(map[string]any)["url"] != "https://dockfly.ejemplo.com/api/hooks/github-app" {
		t.Errorf("manifiesto: %v", manifest)
	}

	// 2. GitHub crea la app y vuelve con un código: Dockfly lleva a instalarla.
	resp, _ := browse(t, ts.URL+"/api/git/github/manifest?code=CODE1&state="+stateOf(t, action))
	install := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.HasPrefix(install, ghe.URL+"/apps/dockfly-abc/installations/new?state=") {
		t.Fatalf("tras crear la app debe ir a instalarla: %d %s", resp.StatusCode, install)
	}
	if resp, body := browse(t, ts.URL+"/api/git/github/manifest?code=CODE1&state="+stateOf(t, action)); resp.StatusCode != 400 ||
		!strings.Contains(body, "caducó o ya se usó") {
		t.Errorf("un state no puede reutilizarse: %d", resp.StatusCode)
	}

	// 3. El usuario elige la organización y vuelve: queda conectada.
	resp, body := browse(t, ts.URL+"/api/git/github/setup?installation_id=9&setup_action=install&state="+stateOf(t, install))
	if resp.StatusCode != 200 || !strings.Contains(body, "GitHub conectado: mi-org") || !strings.Contains(body, "postMessage") {
		t.Fatalf("instalación: %d %s", resp.StatusCode, body)
	}
	// Cambiar los repos desde GitHub vuelve sin state: no debe duplicar.
	browse(t, ts.URL+"/api/git/github/setup?installation_id=9&setup_action=update")
	_, conns := c.doList("GET", "/api/git/connections")
	if len(conns) != 1 {
		t.Fatalf("debe haber una conexión, hay %d", len(conns))
	}
	conn := conns[0].(map[string]any)
	if conn["auth_type"] != "github_app" || conn["username"] != "mi-org" || !strings.Contains(conn["settings_url"].(string), "installations/9") {
		t.Errorf("conexión: %v", conn)
	}
	if resp, body := browse(t, ts.URL+"/api/git/github/setup?installation_id=12345"); resp.StatusCode != 400 ||
		!strings.Contains(body, "no pertenece") {
		t.Errorf("una instalación ajena debe rechazarse: %d", resp.StatusCode)
	}

	// 4. Los repos son los de la instalación y el sitio usa el webhook de la app.
	if _, repos := c.doList("GET", "/api/git/connections/1/repos"); len(repos) != 1 {
		t.Fatalf("repos de la instalación: %v", repos)
	}
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	code, body2 := c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"mi-org/web","branch":"main"}`)
	if code != 200 || body2["webhook_error"] != "" || body2["site"].(map[string]any)["webhook_id"] != "github-app" {
		t.Fatalf("enlazar con la app: %d %v", code, body2)
	}

	// 5. El webhook de la app dispara el build del sitio que despliega ese repo.
	appHook := func(event string, payload, secret string) (int, map[string]any) {
		return c.do("POST", "/api/hooks/github-app", payload, "X-GitHub-Event", event,
			"X-GitHub-Hook-Installation-Target-ID", "77", "X-Hub-Signature-256", githubSignature(secret, []byte(payload)))
	}
	push := `{"ref":"refs/heads/main","head_commit":{"id":"c1","message":"Inicio"},"repository":{"full_name":"mi-org/web"},"installation":{"id":9}}`
	if code, _ := appHook("push", push, "otro"); code != 401 {
		t.Errorf("firma inválida: %d", code)
	}
	other := strings.Replace(push, "mi-org/web", "mi-org/otro", 1)
	if code, body := appHook("push", other, "wsecret"); code != 200 || !strings.Contains(body["status"].(string), "ignorado") {
		t.Errorf("push de un repo sin sitio: %d %v", code, body)
	}
	code, res := appHook("push", push, "wsecret")
	if code != 202 {
		t.Fatalf("push de la app: %d %v", code, res)
	}
	b := waitBuild(t, c, int(res["build_ids"].([]any)[0].(float64)))
	if b["status"] != "success" {
		t.Fatalf("build con token de instalación: %v", b)
	}
	if _, html := visit(static, "web.test", "/"); html != "app v1 !" {
		t.Errorf("se sirvió %q", html)
	}

	// 6. Si desinstalan la app, la conexión desaparece.
	if code, _ := appHook("installation", `{"action":"deleted","installation":{"id":9}}`, "wsecret"); code != 200 {
		t.Errorf("desinstalación: %d", code)
	}
	if _, conns := c.doList("GET", "/api/git/connections"); len(conns) != 0 {
		t.Errorf("tras desinstalar no deben quedar conexiones: %v", conns)
	}
}

func TestGitLabOAuthFlow(t *testing.T) {
	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			r.ParseForm()
			if r.PostForm.Get("client_secret") != "gl-secret" || r.PostForm.Get("code") != "C1" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			io.WriteString(w, `{"access_token":"gla","refresh_token":"glr","expires_in":7200}`)
		case "/api/v4/user":
			if r.Header.Get("Authorization") != "Bearer gla" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, `{"username":"oswa"}`)
		}
	}))
	defer gl.Close()
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	if code, _ := c.do("POST", "/api/git/gitlab/authorize", fmt.Sprintf(`{"app_id":1,"panel_url":%q}`, ts.URL)); code != 404 {
		t.Errorf("sin aplicación configurada: %d", code)
	}
	code, app := c.do("PUT", "/api/git/gitlab/apps", fmt.Sprintf(`{"web_url":%q,"client_id":"cid","client_secret":"gl-secret"}`, gl.URL))
	if code != 200 || app["id"] == nil {
		t.Fatalf("guardar app de GitLab: %d %v", code, app)
	}
	if _, has := app["client_secret_enc"]; has {
		t.Error("la API nunca debe devolver el secreto")
	}

	authorize := func() string {
		_, res := c.do("POST", "/api/git/gitlab/authorize", fmt.Sprintf(`{"app_id":1,"panel_url":%q}`, ts.URL))
		u, _ := url.Parse(res["url"].(string))
		if u.Query().Get("redirect_uri") != ts.URL+"/api/git/gitlab/callback" || u.Query().Get("scope") != "api" {
			t.Fatalf("URL de autorización: %s", u)
		}
		return u.Query().Get("state")
	}

	// Cancelar en la ventana de GitLab.
	if resp, body := browse(t, ts.URL+"/api/git/gitlab/callback?error=access_denied&state="+authorize()); resp.StatusCode != 400 ||
		!strings.Contains(body, "Cancelaste") {
		t.Errorf("cancelar: %d %s", resp.StatusCode, body)
	}
	// Autorizar.
	resp, body := browse(t, ts.URL+"/api/git/gitlab/callback?code=C1&state="+authorize())
	if resp.StatusCode != 200 || !strings.Contains(body, "GitLab conectado: oswa") {
		t.Fatalf("autorizar: %d %s", resp.StatusCode, body)
	}
	// Autorizar otra vez no duplica la cuenta.
	browse(t, ts.URL+"/api/git/gitlab/callback?code=C1&state="+authorize())
	_, conns := c.doList("GET", "/api/git/connections")
	if len(conns) != 1 || conns[0].(map[string]any)["auth_type"] != "gitlab_oauth" {
		t.Fatalf("conexiones: %v", conns)
	}
	if code, _ := c.do("DELETE", "/api/git/gitlab/apps/1", ""); code != 204 {
		t.Errorf("borrar la app sin sitios: %d", code)
	}
}
