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

// fakeGitHub fakes GitHub's API for the oswa/web repo and records the
// webhooks that get created and deleted.
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
	for name, content := range map[string]string{"oswa-web-sha1/site/index.html": "from git", "oswa-web-sha1/README.md": "x"} {
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))})
		io.WriteString(tw, content)
	}
	tw.Close()
	gz.Close()

	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_good" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		// Any host other than api.github.com is treated as GitHub
		// Enterprise, whose API lives under /api/v3.
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v3")
		switch {
		case r.URL.Path == "/user":
			io.WriteString(w, `{"login":"oswa"}`)
		case r.URL.Path == "/user/repos":
			io.WriteString(w, `[{"full_name":"oswa/web","default_branch":"main"}]`)
		case r.URL.Path == "/repos/oswa/web/commits/main":
			io.WriteString(w, `{"sha":"sha1","commit":{"message":"Hello"}}`)
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

// waitBuild waits for the build to finish and returns its state.
func waitBuild(t *testing.T, c *client, id int) map[string]any {
	t.Helper()
	for range 100 {
		_, b := c.do("GET", fmt.Sprintf("/api/builds/%d", id), "")
		if s := b["status"]; s == "success" || s == "failed" || s == "canceled" {
			return b
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the build never finished")
	return nil
}

func TestGitIntegrationFlow(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, static, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	// 1. Link the account: the token is validated before being stored.
	if code, body := c.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_malo"}`, gh.URL)); code != 400 ||
		body["code"] != "git_token_check_failed" {
		t.Errorf("invalid token: %d %v", code, body)
	}
	code, body := c.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_good"}`, gh.URL))
	if code != 201 || body["username"] != "oswa" {
		t.Fatalf("link account: %d %v", code, body)
	}
	if _, has := body["token_enc"]; has {
		t.Error("the API must never return the token")
	}
	if code, repos := c.doList("GET", "/api/git/connections/1/repos"); code != 200 || len(repos) != 1 {
		t.Errorf("list repos: %d %v", code, repos)
	}

	// 2. Link a site. Without a public URL there is no webhook, but manual
	// builds still work.
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	link := `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site","env":{"API_URL":"https://x"}}`
	code, body = c.do("PUT", "/api/sites/1/git", link)
	if code != 200 || problemCode(body["webhook_warning"]) != "webhook_public_url_missing" {
		t.Fatalf("link without a public URL: %d %v", code, body)
	}
	if code, _ := c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"oswa/web","branch":"does-not-exist"}`); code != 400 {
		t.Errorf("a nonexistent branch must be rejected: %d", code)
	}
	if code, _ := c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"oswa/web","branch":"main","root_dir":"../etc"}`); code != 400 {
		t.Errorf("a directory containing .. must be rejected: %d", code)
	}

	// With a public URL the webhook gets registered on GitHub.
	if code, _ := c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com/"}`); code != 200 {
		t.Fatal("save public URL")
	}
	code, body = c.do("PUT", "/api/sites/1/git", link)
	if code != 200 || body["webhook_warning"] != nil {
		t.Fatalf("link with a public URL: %d %v", code, body)
	}
	gh.mu.Lock()
	hookBody := gh.hooks["101"]
	gh.mu.Unlock()
	if !strings.Contains(hookBody, `"url":"https://dockfly.example.com/api/hooks/github/1"`) {
		t.Fatalf("the webhook must point at the public URL: %s", hookBody)
	}
	_, env := c.do("GET", "/api/sites/1/git/env", "")
	if env["API_URL"] != "https://x" {
		t.Errorf("the environment variables must be readable again: %v", env)
	}

	// Editing the site (name, domains) must not unlink the repo.
	c.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"spa":true}`)
	if _, body := c.do("GET", "/api/sites/1", ""); body["site"].(map[string]any)["repo"] != "oswa/web" {
		t.Fatal("editing the site wiped the git configuration")
	}

	// 3. A signed push triggers the build and publishes the result.
	secret := gh.secret(t, "101")
	push := []byte(`{"ref":"refs/heads/main","after":"sha1","head_commit":{"id":"sha1","message":"Hello"}}`)
	hook := func(body []byte, sig, event string) (int, map[string]any) {
		return c.do("POST", "/api/hooks/github/1", string(body), "X-GitHub-Event", event, "X-Hub-Signature-256", sig)
	}
	badCode, badBody := hook(push, githubSignature("wrong", push), "push")
	if badCode != 401 {
		t.Errorf("invalid signature: expected 401, got %d", badCode)
	}
	// Without the secret, an id that exists looks just like one that doesn't,
	// and so does the provider.
	for _, path := range []string{"/api/hooks/github/999", "/api/hooks/gitlab/1", "/api/hooks/github/apps/999", "/api/hooks/otro/apps/999"} {
		code, body := c.do("POST", path, string(push), "X-GitHub-Event", "push", "X-Hub-Signature-256", githubSignature("wrong", push))
		if code != badCode || body["code"] != badBody["code"] {
			t.Errorf("%s: expected the same answer as an invalid signature (%d %v), got %d %v", path, badCode, badBody, code, body)
		}
	}
	if code, body := hook([]byte(`{"zen":"x"}`), githubSignature(secret, []byte(`{"zen":"x"}`)), "ping"); code != 200 {
		t.Errorf("ping: %d %v", code, body)
	}
	other := []byte(`{"ref":"refs/heads/dev","head_commit":{"id":"x","message":"y"}}`)
	if code, body := hook(other, githubSignature(secret, other), "push"); code != 200 || !strings.Contains(body["status"].(string), "ignored") {
		t.Errorf("push to another branch: %d %v", code, body)
	}
	code, body = hook(push, githubSignature(secret, push), "push")
	if code != 202 {
		t.Fatalf("valid push: %d %v", code, body)
	}
	build := waitBuild(t, c, int(body["build_id"].(float64)))
	if build["status"] != "success" || build["trigger"] != "push" {
		t.Fatalf("the push build must finish successfully: %v", build)
	}
	if _, html := visit(static, "web.test", "/"); html != "from git" {
		t.Errorf("the built output must be served, got %q", html)
	}

	// 4. The log can be followed over SSE and ends with the "end" event.
	resp, err := c.http.Get(fmt.Sprintf("%s/api/builds/%d/logs", ts.URL, int(build["id"].(float64))))
	if err != nil {
		t.Fatal(err)
	}
	sse, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(string(sse), "event: status") ||
		!strings.Contains(string(sse), "event: log") || !strings.Contains(string(sse), "event: end") {
		t.Errorf("log SSE:\n%s", sse)
	}

	// 5. Manual build: it resolves the branch's latest commit.
	code, body = c.do("POST", "/api/sites/1/builds", "")
	if code != 202 {
		t.Fatalf("manual build: %d %v", code, body)
	}
	if b := waitBuild(t, c, int(body["id"].(float64))); b["status"] != "success" || b["commit_sha"] != "sha1" {
		t.Errorf("manual build: %v", b)
	}
	if _, list := c.doList("GET", "/api/sites/1/builds"); len(list) != 2 {
		t.Errorf("2 builds must be listed, there are %d", len(list))
	}

	// 5b. Deploy hook: a secret URL triggers a build; regenerating it invalidates the old one.
	_, deployHook := c.do("POST", "/api/sites/1/deploy-hook", "")
	hookToken, _ := deployHook["token"].(string)
	if _, got := c.do("GET", "/api/sites/1/deploy-hook", ""); hookToken == "" || got["token"] != hookToken {
		t.Fatalf("the deploy hook must be readable afterwards: %v", got)
	}
	if code, body := c.do("POST", "/api/hooks/deploy/"+hookToken, `{"event":"entry.publish"}`); code != 202 || body["build_id"] == nil {
		t.Fatalf("the deploy hook must queue a build: %d %v", code, body)
	} else if b := waitBuild(t, c, int(body["build_id"].(float64))); b["trigger"] != "hook" {
		t.Errorf("deploy hook build: %v", b)
	}
	c.do("POST", "/api/sites/1/deploy-hook", "")
	if code, _ := c.do("POST", "/api/hooks/deploy/"+hookToken, ""); code != 404 {
		t.Errorf("a regenerated deploy hook must stop working: %d", code)
	}
	c.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"]}`)
	if _, got := c.do("GET", "/api/sites/1/deploy-hook", ""); got["token"] == "" {
		t.Error("editing the site must not delete its deploy hook")
	}

	// 6. An account in use can't be deleted; unlinking deletes the webhook.
	if code, _ := c.do("DELETE", "/api/git/connections/1", ""); code != 409 {
		t.Errorf("delete account in use: expected 409, got %d", code)
	}
	if code, _ := c.do("DELETE", "/api/sites/1/git", ""); code != 204 {
		t.Fatal("unlink")
	}
	gh.mu.Lock()
	deleted := strings.Join(gh.deleted, ",")
	gh.mu.Unlock()
	if deleted != "101" {
		t.Errorf("unlinking must delete the webhook on GitHub, deleted %q", deleted)
	}
	if code, _ := hook(push, githubSignature(secret, push), "push"); code != 401 {
		t.Errorf("after unlinking, the old webhook must be rejected with 401, got %d", code)
	}
	if code, _ := c.do("DELETE", "/api/git/connections/1", ""); code != 204 {
		t.Errorf("delete unused account: %d", code)
	}
}

// secret returns the secret Dockfly registered the webhook with, exactly as
// GitHub received it.
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
		t.Fatalf("webhook %s must be registered with a strong secret: %q %v", id, hook.Config.Secret, err)
	}
	return hook.Config.Secret
}

// doList is like do, for responses that are a JSON list.
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

// problemCode is the code of a coded warning (or "" when there is none).
func problemCode(v any) string {
	m, _ := v.(map[string]any)
	code, _ := m["code"].(string)
	return code
}
