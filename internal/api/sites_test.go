package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dockfly/dockfly/internal/pages"
)

// upload sends a deployment. With zipFiles != nil it uploads a .zip;
// otherwise it uploads files as if a folder had been dragged onto the
// browser.
func (c *client) upload(siteID int, files, zipFiles map[string]string, withHeader bool) (int, map[string]any) {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if zipFiles != nil {
		var zbuf bytes.Buffer
		zw := zip.NewWriter(&zbuf)
		for name, content := range zipFiles {
			w, _ := zw.Create(name)
			io.WriteString(w, content)
		}
		zw.Close()
		w, _ := mw.CreateFormFile("archive", "site.zip")
		w.Write(zbuf.Bytes())
	}
	for name, content := range files {
		w, _ := mw.CreateFormFile("file:"+name, name)
		io.WriteString(w, content)
	}
	mw.Close()

	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/sites/%d/deployments", c.base, siteID), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if withHeader {
		req.Header.Set(uploadHeader, "1")
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

// visit asks the static server for a path the way Traefik would.
func visit(srv *pages.Server, host, path string) (int, string) {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestSiteDeployFlow(t *testing.T) {
	ts, static, dynDir := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	code, body := c.do("POST", "/api/sites", `{"name":"Mi-Web","domains":["web.test"],"ssl":true,"force_https":true}`)
	if code != http.StatusCreated {
		t.Fatalf("creating a site: %d %v", code, body)
	}
	if body["name"] != "mi-web" {
		t.Errorf("the name must be normalized to lowercase: %v", body["name"])
	}
	if code, _ := c.do("POST", "/api/sites", `{"name":"mi web","domains":["x.test"]}`); code != 400 {
		t.Errorf("name with spaces: want 400, got %d", code)
	}
	if code, _ := c.do("POST", "/api/sites", `{"name":"mi-web","domains":["other.test"]}`); code != 409 {
		t.Errorf("duplicate name: want 409, got %d", code)
	}

	// A domain can't be on a site and on a proxy host at the same time.
	if code, body := c.do("POST", "/api/proxy-hosts",
		`{"domains":["web.test"],"target_type":"external","forward_host":"1.1.1.1","forward_port":80}`); code != 409 ||
		body["code"] != "domain_in_use" {
		t.Errorf("a site's domain on a proxy host: %d %v", code, body)
	}

	// With no deployments Traefik already routes the domain and the notice shows.
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	if !strings.Contains(string(cfg), "site-1-secure") || !strings.Contains(string(cfg), "http://127.0.0.1:8099") {
		t.Fatalf("the site is missing from Traefik's configuration:\n%s", cfg)
	}
	if _, html := visit(static, "web.test", "/"); !strings.Contains(html, "has no deployments yet") {
		t.Errorf("with no deployments the notice must show, got %q", html)
	}

	// Deployment 1: a folder.
	code, body = c.upload(1, map[string]string{"index.html": "v1", "css/app.css": "body{}"}, nil, true)
	if code != http.StatusCreated || body["file_count"] != float64(2) {
		t.Fatalf("uploading a folder: %d %v", code, body)
	}
	if _, html := visit(static, "web.test", "/"); html != "v1" {
		t.Errorf("after deployment 1 it must serve v1, served %q", html)
	}
	if _, css := visit(static, "web.test", "/css/app.css"); css != "body{}" {
		t.Errorf("the path inside the folder must be kept, served %q", css)
	}

	// Deployment 2: a zip with everything inside dist/ (one level is stripped).
	code, _ = c.upload(1, nil, map[string]string{"dist/index.html": "v2", "dist/about.html": "about"}, true)
	if code != http.StatusCreated {
		t.Fatalf("uploading a zip: %d", code)
	}
	if _, html := visit(static, "web.test", "/"); html != "v2" {
		t.Errorf("after deployment 2 it must serve v2, served %q", html)
	}
	if _, html := visit(static, "web.test", "/about"); html != "about" {
		t.Errorf("clean URL /about: served %q", html)
	}

	// Rollback to deployment 1.
	if code, _ := c.do("POST", "/api/sites/1/deployments/1/activate", ""); code != http.StatusNoContent {
		t.Fatalf("rollback: %d", code)
	}
	if _, html := visit(static, "web.test", "/"); html != "v1" {
		t.Errorf("after the rollback it must serve v1, served %q", html)
	}

	// The published one can't be deleted; the other one can.
	if code, _ := c.do("DELETE", "/api/sites/1/deployments/1", ""); code != http.StatusConflict {
		t.Errorf("deleting the published one: want 409, got %d", code)
	}
	if code, _ := c.do("DELETE", "/api/sites/1/deployments/2", ""); code != http.StatusNoContent {
		t.Errorf("deleting a deployment: want 204, got %d", code)
	}
	if code, body := c.do("GET", "/api/sites/1", ""); code != 200 || len(body["deployments"].([]any)) != 1 {
		t.Errorf("site detail: %d %v", code, body)
	}

	// Turn on SPA mode while editing.
	if code, _ := c.do("PUT", "/api/sites/1", `{"name":"mi-web","domains":["web.test"],"spa":true}`); code != 200 {
		t.Fatalf("editing the site: %d", code)
	}
	if _, html := visit(static, "web.test", "/users/42"); html != "v1" {
		t.Errorf("in SPA mode any path must serve index.html, served %q", html)
	}

	// Deleting the site stops serving it and removes its files.
	if code, _ := c.do("DELETE", "/api/sites/1", ""); code != http.StatusNoContent {
		t.Fatalf("deleting the site: %d", code)
	}
	if code, _ := visit(static, "web.test", "/"); code != 404 {
		t.Errorf("after deleting the site it must give 404, gave %d", code)
	}
}

func TestUploadValidation(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/sites", `{"name":"s","domains":["s.test"]}`)

	if code, _ := c.upload(1, map[string]string{"index.html": "x"}, nil, false); code != http.StatusUnsupportedMediaType {
		t.Errorf("without the anti-CSRF header: want 415, got %d", code)
	}
	if code, _ := c.upload(1, map[string]string{}, nil, true); code != 400 {
		t.Errorf("empty upload: want 400, got %d", code)
	}
	// MaxUploadMB = 1 on the test server.
	if code, _ := c.upload(1, map[string]string{"large.bin": strings.Repeat("x", 2<<20)}, nil, true); code != http.StatusRequestEntityTooLarge {
		t.Errorf("upload too large: want 413, got %d", code)
	}
	if code, _ := c.upload(99, map[string]string{"index.html": "x"}, nil, true); code != 404 {
		t.Errorf("nonexistent site: want 404, got %d", code)
	}
	if code, body := c.do("GET", "/api/sites/1", ""); len(body["deployments"].([]any)) != 0 {
		t.Errorf("failed uploads must not create deployments: %d %v", code, body)
	}
}

func TestPruneKeepsLatestDeployments(t *testing.T) {
	ts, static, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/sites", `{"name":"s","domains":["s.test"]}`)

	total := keepDeployments + 3
	for i := 1; i <= total; i++ {
		if code, _ := c.upload(1, map[string]string{"index.html": fmt.Sprint("v", i)}, nil, true); code != 201 {
			t.Fatalf("upload %d: %d", i, code)
		}
	}

	_, body := c.do("GET", "/api/sites/1", "")
	deps := body["deployments"].([]any)
	if len(deps) != keepDeployments {
		t.Fatalf("%d deployments should be left, %d are", keepDeployments, len(deps))
	}
	// The most recent ones are kept.
	newest := deps[0].(map[string]any)["id"].(float64)
	oldest := deps[len(deps)-1].(map[string]any)["id"].(float64)
	if newest != float64(total) || oldest != float64(total-keepDeployments+1) {
		t.Errorf("kept deployments %v..%v, want %d..%d", oldest, newest, total-keepDeployments+1, total)
	}
	if _, html := visit(static, "s.test", "/"); html != fmt.Sprint("v", total) {
		t.Errorf("the last upload must be served, served %q", html)
	}
	// You can roll back to the oldest one left, but not to a deleted one.
	if code, _ := c.do("POST", fmt.Sprintf("/api/sites/1/deployments/%d/activate", int(oldest)), ""); code != 204 {
		t.Errorf("rollback to the oldest kept one: %d", code)
	}
	if code, _ := c.do("POST", "/api/sites/1/deployments/1/activate", ""); code != 404 {
		t.Errorf("activating a deleted deployment: want 404, got %d", code)
	}
}
