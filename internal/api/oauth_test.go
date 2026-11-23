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

// fakeGHE fakes GitHub Enterprise: the web and the API (/api/v3) on one host.
func fakeGHE(t *testing.T) *httptest.Server {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "my-org-web-c1/index.html", Typeflag: tar.TypeReg, Mode: 0o644, Size: 8})
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
			io.WriteString(w, `{"id":9,"account":{"login":"my-org","type":"Organization"},"html_url":"http://ghe/organizations/my-org/settings/installations/9"}`)
		case p == "/app/installations/9/access_tokens" && strings.HasPrefix(auth, "Bearer ey"):
			io.WriteString(w, `{"token":"ghs_1","expires_at":"2099-01-01T00:00:00Z"}`)
		case p == "/app/hook/config" && r.Method == "PATCH":
			io.WriteString(w, `{}`)
		case auth != "Bearer ghs_1":
			w.WriteHeader(401)
		case p == "/installation/repositories":
			io.WriteString(w, `{"repositories":[{"full_name":"my-org/web","default_branch":"main"}]}`)
		case p == "/repos/my-org/web/commits/main":
			io.WriteString(w, `{"sha":"c1","commit":{"message":"Initial"}}`)
		case strings.HasPrefix(p, "/repos/my-org/web/tarball/"):
			w.Write(tgz.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// browser mimics the popup window: it follows the flow without the session
// cookie (SameSite=Strict doesn't travel on navigations from another site) and
// without following redirects, so they can be inspected.
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
		t.Fatalf("URL without a state: %s", rawURL)
	}
	return u.Query().Get("state")
}

func TestGitHubAppFlow(t *testing.T) {
	ghe := fakeGHE(t)
	ts, static, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)

	// 1. The panel asks for the manifest and posts it to GitHub.
	code, start := c.do("POST", "/api/git/github/manifest", fmt.Sprintf(`{"panel_url":%q,"enterprise_url":%q}`, ts.URL, ghe.URL))
	if code != 200 {
		t.Fatalf("manifest: %d %v", code, start)
	}
	action := start["action"].(string)
	if !strings.HasPrefix(action, ghe.URL+"/settings/apps/new?state=") {
		t.Errorf("the form must go to GitHub's website: %s", action)
	}
	var manifest map[string]any
	json.Unmarshal([]byte(start["manifest"].(string)), &manifest)
	if manifest["redirect_url"] != ts.URL+"/api/git/github/manifest" ||
		manifest["hook_attributes"].(map[string]any)["url"] != "https://dockfly.example.com/api/hooks/github-app" {
		t.Errorf("manifest: %v", manifest)
	}

	// 2. GitHub creates the app and comes back with a code: Dockfly sends the
	// user off to install it.
	resp, _ := browse(t, ts.URL+"/api/git/github/manifest?code=CODE1&state="+stateOf(t, action))
	install := resp.Header.Get("Location")
	if resp.StatusCode != 302 || !strings.HasPrefix(install, ghe.URL+"/apps/dockfly-abc/installations/new?state=") {
		t.Fatalf("after creating the app it must go on to install it: %d %s", resp.StatusCode, install)
	}
	if resp, body := browse(t, ts.URL+"/api/git/github/manifest?code=CODE1&state="+stateOf(t, action)); resp.StatusCode != 400 ||
		!strings.Contains(body, "expired or was already used") {
		t.Errorf("a state can't be reused: %d", resp.StatusCode)
	}

	// 3. The user picks the organization and comes back: it ends up connected.
	resp, body := browse(t, ts.URL+"/api/git/github/setup?installation_id=9&setup_action=install&state="+stateOf(t, install))
	if resp.StatusCode != 200 || !strings.Contains(body, "GitHub connected: my-org") || !strings.Contains(body, "postMessage") {
		t.Fatalf("installation: %d %s", resp.StatusCode, body)
	}
	// Changing the repos from GitHub comes back with no state: it must not
	// create a duplicate.
	browse(t, ts.URL+"/api/git/github/setup?installation_id=9&setup_action=update")
	_, conns := c.doList("GET", "/api/git/connections")
	if len(conns) != 1 {
		t.Fatalf("there must be one connection, there are %d", len(conns))
	}
	conn := conns[0].(map[string]any)
	if conn["auth_type"] != "github_app" || conn["username"] != "my-org" || !strings.Contains(conn["settings_url"].(string), "installations/9") {
		t.Errorf("connection: %v", conn)
	}
	if resp, body := browse(t, ts.URL+"/api/git/github/setup?installation_id=12345"); resp.StatusCode != 400 ||
		!strings.Contains(body, "doesn't belong") {
		t.Errorf("someone else's installation must be rejected: %d", resp.StatusCode)
	}

	// 4. The repos are the installation's and the site uses the app's webhook.
	if _, repos := c.doList("GET", "/api/git/connections/1/repos"); len(repos) != 1 {
		t.Fatalf("installation repos: %v", repos)
	}
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	code, body2 := c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"my-org/web","branch":"main"}`)
	if code != 200 || body2["webhook_warning"] != nil || body2["site"].(map[string]any)["webhook_id"] != "github-app" {
		t.Fatalf("link through the app: %d %v", code, body2)
	}

	// 5. The app's webhook triggers the build of the site that deploys that repo.
	appHook := func(event string, payload, secret string) (int, map[string]any) {
		return c.do("POST", "/api/hooks/github-app", payload, "X-GitHub-Event", event,
			"X-GitHub-Hook-Installation-Target-ID", "77", "X-Hub-Signature-256", githubSignature(secret, []byte(payload)))
	}
	push := `{"ref":"refs/heads/main","head_commit":{"id":"c1","message":"Initial"},"repository":{"full_name":"my-org/web"},"installation":{"id":9}}`
	if code, _ := appHook("push", push, "other"); code != 401 {
		t.Errorf("invalid signature: %d", code)
	}
	other := strings.Replace(push, "my-org/web", "my-org/other", 1)
	if code, body := appHook("push", other, "wsecret"); code != 200 || !strings.Contains(body["status"].(string), "ignored") {
		t.Errorf("push from a repo with no site: %d %v", code, body)
	}
	code, res := appHook("push", push, "wsecret")
	if code != 202 {
		t.Fatalf("push through the app: %d %v", code, res)
	}
	b := waitBuild(t, c, int(res["build_ids"].([]any)[0].(float64)))
	if b["status"] != "success" {
		t.Fatalf("build with an installation token: %v", b)
	}
	if _, html := visit(static, "web.test", "/"); html != "app v1 !" {
		t.Errorf("served %q", html)
	}

	// 6. If the app gets uninstalled, the connection disappears.
	if code, _ := appHook("installation", `{"action":"deleted","installation":{"id":9}}`, "wsecret"); code != 200 {
		t.Errorf("uninstall: %d", code)
	}
	if _, conns := c.doList("GET", "/api/git/connections"); len(conns) != 0 {
		t.Errorf("no connections must be left after uninstalling: %v", conns)
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
		t.Errorf("with no application configured: %d", code)
	}
	code, app := c.do("PUT", "/api/git/gitlab/apps", fmt.Sprintf(`{"web_url":%q,"client_id":"cid","client_secret":"gl-secret"}`, gl.URL))
	if code != 200 || app["id"] == nil {
		t.Fatalf("save GitLab app: %d %v", code, app)
	}
	if _, has := app["client_secret_enc"]; has {
		t.Error("the API must never return the secret")
	}

	authorize := func() string {
		_, res := c.do("POST", "/api/git/gitlab/authorize", fmt.Sprintf(`{"app_id":1,"panel_url":%q}`, ts.URL))
		u, _ := url.Parse(res["url"].(string))
		if u.Query().Get("redirect_uri") != ts.URL+"/api/git/gitlab/callback" || u.Query().Get("scope") != "api" {
			t.Fatalf("authorization URL: %s", u)
		}
		return u.Query().Get("state")
	}

	// Canceling in the GitLab window.
	if resp, body := browse(t, ts.URL+"/api/git/gitlab/callback?error=access_denied&state="+authorize()); resp.StatusCode != 400 ||
		!strings.Contains(body, "You canceled") {
		t.Errorf("cancel: %d %s", resp.StatusCode, body)
	}
	// Authorizing.
	resp, body := browse(t, ts.URL+"/api/git/gitlab/callback?code=C1&state="+authorize())
	if resp.StatusCode != 200 || !strings.Contains(body, "GitLab connected: oswa") {
		t.Fatalf("authorize: %d %s", resp.StatusCode, body)
	}
	// Authorizing again does not duplicate the account.
	browse(t, ts.URL+"/api/git/gitlab/callback?code=C1&state="+authorize())
	_, conns := c.doList("GET", "/api/git/connections")
	if len(conns) != 1 || conns[0].(map[string]any)["auth_type"] != "gitlab_oauth" {
		t.Fatalf("connections: %v", conns)
	}
	if code, _ := c.do("DELETE", "/api/git/gitlab/apps/1", ""); code != 204 {
		t.Errorf("delete the app with no sites: %d", code)
	}
}
