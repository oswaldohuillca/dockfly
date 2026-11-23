package api

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

func TestAPITokens(t *testing.T) {
	ts, static, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)

	if code, _ := admin.do("POST", "/api/tokens", `{"name":"","role":"deployer"}`); code != 400 {
		t.Errorf("no name: %d", code)
	}
	code, body := admin.do("POST", "/api/tokens", `{"name":"GitHub Actions","role":"deployer","expires_days":30}`)
	if code != 201 {
		t.Fatalf("creating a token: %d %v", code, body)
	}
	token := body["token"].(string)
	meta := body["api_token"].(map[string]any)
	if len(token) != 52 || meta["prefix"] != token[:10] || meta["expires_at"] == nil {
		t.Errorf("token: %s %v", token, meta)
	}
	if _, list := admin.doList("GET", "/api/tokens"); len(list) != 1 {
		t.Fatalf("listing tokens: %v", list)
	} else if _, has := list[0].(map[string]any)["token_hash"]; has {
		t.Error("the list must never include the hash")
	}

	// The token works without a session cookie.
	cli := newClient(t, ts)
	bearer := func(method, path, body string) (int, map[string]any) {
		return cli.do(method, path, body, "Authorization", "Bearer "+token)
	}
	if code, _ := bearer("GET", "/api/sites/1", ""); code != 200 {
		t.Errorf("reading with a token: %d", code)
	}
	// Upload a deployment, the way the CLI will.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file:index.html", "index.html")
	fmt.Fprint(fw, "from the CLI")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/sites/1/deployments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(uploadHeader, "1")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("uploading with a token: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()
	if _, html := visit(static, "web.test", "/"); html != "from the CLI" {
		t.Errorf("served %q", html)
	}

	// A deploy token touches neither the admin side, nor the account, nor other tokens.
	if code, _ := bearer("POST", "/api/proxy-hosts", `{}`); code != 403 {
		t.Errorf("deployer token on an admin route: %d", code)
	}
	if code, _ := bearer("POST", "/api/tokens", `{"name":"x","role":"admin"}`); code != 403 {
		t.Errorf("a token must not create tokens: %d", code)
	}
	if code, _ := bearer("PUT", "/api/auth/password", `{"current":"secret123","new":"other-password"}`); code != 403 {
		t.Errorf("a token must not change the password: %d", code)
	}
	if code, _ := cli.do("GET", "/api/sites", "", "Authorization", "Bearer dfy_fake"); code != 401 {
		t.Errorf("bogus token: %d", code)
	}
	// An invalid Authorization is not waved through by a valid cookie.
	if code, _ := admin.do("GET", "/api/sites", "", "Authorization", "Bearer dfy_fake"); code != 401 {
		t.Errorf("valid cookie with a bogus token: %d", code)
	}

	// A token can never do more than its user, even if the user is demoted later.
	s := serverOf(t, ts)
	_, inv := admin.do("POST", "/api/users", `{"name":"Mira","email":"mira@test.com","role":"deployer"}`)
	viewer := newClient(t, ts)
	accept(t, viewer, inv["link"].(string), "mira-secret")
	if code, _ := viewer.do("POST", "/api/tokens", `{"name":"x","role":"admin"}`); code != 403 {
		t.Errorf("token with a higher role than its user: %d", code)
	}
	_, vt := viewer.do("POST", "/api/tokens", `{"name":"ci","role":"deployer"}`)
	admin.do("PUT", "/api/users/2", `{"name":"Mira","role":"viewer"}`)
	if code, _ := viewer.do("POST", "/api/sites/1/builds", "", "Authorization", "Bearer "+vt["token"].(string)); code != 403 {
		t.Errorf("the token must lose permissions along with its user: %d", code)
	}

	// Expired or revoked: out.
	old := newToken()
	past := time.Now().Add(-time.Hour)
	if err := s.Store.CreateAPIToken(context.Background(), &store.APIToken{
		UserID: 1, Name: "old", Role: "admin", TokenHash: hashToken(old), Prefix: old[:10], ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := cli.do("GET", "/api/sites", "", "Authorization", "Bearer "+old); code != 401 {
		t.Errorf("expired token: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/tokens/1", ""); code != 204 {
		t.Errorf("revoking: %d", code)
	}
	if code, _ := viewer.do("DELETE", "/api/tokens/1", ""); code != 404 {
		t.Errorf("someone else's tokens can't be revoked: %d", code)
	}
}

// An API token with the admin role can't manage users: creating an admin
// with an invite/reset link would be a foothold that outlives revoking the
// token, exactly what the "panel-only" veto prevents for /api/auth and
// /api/tokens (M-B).
func TestAdminTokenCannotManageUsers(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)

	_, body := admin.do("POST", "/api/tokens", `{"name":"ci","role":"admin"}`)
	token := body["token"].(string)
	bearer := func(method, path, b string) (int, map[string]any) {
		return newClient(t, ts).do(method, path, b, "Authorization", "Bearer "+token)
	}
	// With an admin cookie users can be managed; with an admin token, not.
	if code, res := admin.do("POST", "/api/users", `{"name":"X","email":"x@test.com","role":"admin"}`); code != 201 {
		t.Fatalf("the admin does invite from the panel: %d %v", code, res)
	}
	for _, p := range []string{"/api/users", "/api/users/2/link"} {
		if code, res := bearer("POST", p, `{"name":"Y","email":"y@test.com","role":"admin"}`); code != 403 || res["code"] != "panel_only" {
			t.Errorf("admin token on %s: %d %v (want 403 panel_only)", p, code, res)
		}
	}
}
