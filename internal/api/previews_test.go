package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewSlug(t *testing.T) {
	for in, want := range map[string]string{
		"feature/New_Login":                  "feature-new-login",
		"--weird//branch--":                  "weird-branch",
		"dependabot/npm_and_yarn/vite-5.0.1": "dependabot-npm-and-yarn-vite-5-0-1",
		"ñandú":                              "and",
		strings.Repeat("a", 60):              strings.Repeat("a", 40),
	} {
		if got := previewSlug(in); got != want {
			t.Errorf("previewSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviewsFlow(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, static, dyn := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/git/connections", `{"provider":"github","base_url":"`+gh.URL+`","token":"ghp_good"}`)
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"],"previews":true}`)
	c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site"}`)
	secret := gh.secret(t, "101")
	hook := func(body string) (int, map[string]any) {
		return c.do("POST", "/api/hooks/github/1", body, "X-GitHub-Event", "push",
			"X-Hub-Signature-256", githubSignature(secret, []byte(body)))
	}
	push := `{"ref":"refs/heads/feature/login","head_commit":{"id":"sha1","message":"Login"}}`

	// With no preview domain, the other branches are ignored.
	if code, body := hook(push); code != 200 || !strings.Contains(body["status"].(string), "ignored") {
		t.Fatalf("no preview domain: %d %v", code, body)
	}
	if code, body := c.do("PUT", "/api/settings", `{"preview_domain":"*.previews.test"}`); code != 400 {
		t.Errorf("the preview domain must not carry a wildcard: %d %v", code, body)
	}
	code, body := c.do("PUT", "/api/settings", `{"preview_domain":"Previews.Test."}`)
	if code != 200 {
		t.Fatalf("saving the preview domain: %d %v", code, body)
	}
	set := body["settings"].(map[string]any)
	if set["preview_domain"] != "previews.test" || set["public_url"] != "https://dockfly.example.com" || set["preview_ssl"] != false {
		t.Errorf("settings: %v", set)
	}

	code, body = hook(push)
	if code != 202 || body["status"] != "queued (preview)" {
		t.Fatalf("push to a branch: %d %v", code, body)
	}
	b := waitBuild(t, c, int(body["build_id"].(float64)))
	if b["status"] != "success" || b["preview"] != "feature-login" {
		t.Fatalf("preview build: %v", b)
	}
	if _, html := visit(static, "feature-login--web.previews.test", "/"); html != "from git" {
		t.Errorf("the preview must be served on its own domain, served %q", html)
	}
	// Production does not change.
	if _, site := c.do("GET", "/api/sites/1", ""); site["site"].(map[string]any)["active_deployment_id"] != nil {
		t.Error("a preview must not be published to production")
	}
	cfg, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	if !strings.Contains(string(cfg), `\.previews\.test$`) {
		t.Errorf("Traefik must route the preview wildcard:\n%s", cfg)
	}
	code, list := c.doList("GET", "/api/sites/1/previews")
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["url"] != "http://feature-login--web.previews.test" ||
		list[0].(map[string]any)["branch"] != "feature/login" {
		t.Fatalf("listing previews: %d %v", code, list)
	}
	if _, deps := c.do("GET", "/api/sites/1", ""); len(deps["deployments"].([]any)) != 0 {
		t.Error("previews must not show up among the production deployments")
	}

	// Deleting the branch deletes the preview.
	code, body = hook(`{"ref":"refs/heads/feature/login","deleted":true,"head_commit":null}`)
	if code != 200 || body["status"] != "preview deleted" {
		t.Fatalf("deleting the branch: %d %v", code, body)
	}
	if code, _ := visit(static, "feature-login--web.previews.test", "/"); code != 404 {
		t.Errorf("a deleted preview must not be served: %d", code)
	}
	if _, list := c.doList("GET", "/api/sites/1/previews"); len(list) != 0 {
		t.Errorf("no previews should be left: %v", list)
	}

	// With previews disabled on the site, another branch is ignored.
	c.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"previews":false}`)
	if code, body := hook(push); code != 200 || !strings.Contains(body["status"].(string), "ignored") {
		t.Errorf("previews disabled: %d %v", code, body)
	}
}
