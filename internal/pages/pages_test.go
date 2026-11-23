package pages

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStorage(t *testing.T, limits Limits) *Storage {
	return &Storage{Root: t.TempDir(), Limits: limits}
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestZipSlipStaysInside(t *testing.T) {
	st := newStorage(t, DefaultLimits)
	s, err := st.NewStaging(1)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Discard(s)

	z := makeZip(t, map[string]string{
		"../../escaped.txt":     "bad",
		"/etc/absolute.txt":     "bad",
		`..\..\windows.txt`:     "bad",
		"index.html":            "ok",
		"__MACOSX/._index.html": "junk",
		"sub/.DS_Store":         "junk",
	})
	if err := s.AddZip(bytes.NewReader(z), int64(len(z))); err != nil {
		t.Fatal(err)
	}
	// The malicious paths stay anchored inside the deployment.
	for _, p := range []string{"escaped.txt", "etc/absolute.txt", "windows.txt", "index.html"} {
		if _, err := os.Stat(filepath.Join(s.Dir, p)); err != nil {
			t.Errorf("%s should be inside the deployment: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(st.Root), "escaped.txt")); err == nil {
		t.Fatal("zip slip: something was written outside the directory")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "__MACOSX")); err == nil {
		t.Error("__MACOSX must be ignored")
	}
}

func TestSymlinkInsideStagingCannotEscape(t *testing.T) {
	st := newStorage(t, DefaultLimits)
	s, _ := st.NewStaging(1)
	defer st.Discard(s)
	outside := t.TempDir()
	// Simulate a symlink planted in the directory (e.g. by a zip that carried
	// one): os.Root must not follow it outwards.
	if err := os.Symlink(outside, filepath.Join(s.Dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("link/pwned.txt", strings.NewReader("x")); err == nil {
		t.Error("writing through a symlink pointing outside must fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("something was written outside through the symlink")
	}
}

func TestLimits(t *testing.T) {
	t.Run("bytes (zip bomb)", func(t *testing.T) {
		st := newStorage(t, Limits{MaxFiles: 100, MaxBytes: 1000})
		s, _ := st.NewStaging(1)
		defer st.Discard(s)
		z := makeZip(t, map[string]string{"bomb.txt": strings.Repeat("A", 1_000_000)})
		if len(z) > 10_000 {
			t.Fatalf("the test zip should compress a lot, it takes up %d", len(z))
		}
		err := s.AddZip(bytes.NewReader(z), int64(len(z)))
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected ErrInvalid because of the size, got %v", err)
		}
		fi, _ := os.Stat(filepath.Join(s.Dir, "bomb.txt"))
		if fi != nil && fi.Size() > 1001 {
			t.Errorf("%d bytes were uncompressed, more than the limit", fi.Size())
		}
	})
	t.Run("files", func(t *testing.T) {
		st := newStorage(t, Limits{MaxFiles: 2, MaxBytes: 1 << 20})
		s, _ := st.NewStaging(1)
		defer st.Discard(s)
		for _, n := range []string{"a", "b"} {
			if err := s.Add(n, strings.NewReader("x")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add("c", strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
			t.Errorf("expected ErrInvalid because of the file count, got %v", err)
		}
	})
	t.Run("duplicates and empty", func(t *testing.T) {
		st := newStorage(t, DefaultLimits)
		s, _ := st.NewStaging(1)
		defer st.Discard(s)
		if _, _, err := s.Finish(); !errors.Is(err, ErrInvalid) {
			t.Errorf("an empty deployment must be invalid: %v", err)
		}
		s.Add("a.html", strings.NewReader("1"))
		if err := s.Add("./a.html", strings.NewReader("2")); !errors.Is(err, ErrInvalid) {
			t.Errorf("a duplicate file must be invalid: %v", err)
		}
	})
}

func TestFinishHoistsSingleFolder(t *testing.T) {
	st := newStorage(t, DefaultLimits)
	s, _ := st.NewStaging(7)
	z := makeZip(t, map[string]string{"dist/index.html": "home", "dist/assets/app.js": "js"})
	if err := s.AddZip(bytes.NewReader(z), int64(len(z))); err != nil {
		t.Fatal(err)
	}
	files, size, err := s.Finish()
	if err != nil || files != 2 || size != 6 {
		t.Fatalf("Finish: %d files, %d bytes, %v", files, size, err)
	}
	if err := st.Commit(s, 7, 42); err != nil {
		t.Fatal(err)
	}
	dir := st.DeploymentDir(7, 42)
	if readFile(t, filepath.Join(dir, "index.html")) != "home" || readFile(t, filepath.Join(dir, "assets/app.js")) != "js" {
		t.Error("the contents of dist/ must end up at the deployment root")
	}
}

func TestCleanStaging(t *testing.T) {
	st := newStorage(t, DefaultLimits)
	s, _ := st.NewStaging(1)
	s.Add("index.html", strings.NewReader("x"))
	s.close() // simulates a crash halfway through the upload
	if err := st.CleanStaging(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Error("CleanStaging must delete the half-uploaded deployments")
	}
}

// --- Server ---

func deploy(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func get(srv http.Handler, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestServerResolution(t *testing.T) {
	site := deploy(t, map[string]string{
		"index.html":      "home",
		"about.html":      "about",
		"docs/index.html": "docs",
		"404.html":        "not found",
		"assets/app.js":   "js",
		".env":            "SECRET=1",
		".well-known/x":   "verification",
	})
	spa := deploy(t, map[string]string{"index.html": "spa"})

	srv := &Server{}
	srv.SetRoutes(map[string]Route{
		"site.test":  {Dir: site, DeploymentID: 1},
		"spa.test":   {Dir: spa, DeploymentID: 2, SPA: true},
		"empty.test": {},
	})

	cases := []struct {
		host, path string
		status     int
		body       string
	}{
		{"site.test", "/", 200, "home"},
		{"SITE.test:443", "/", 200, "home"},
		{"site.test", "/about", 200, "about"},
		{"site.test", "/about.html", 200, "about"},
		{"site.test", "/docs", 200, "docs"},
		{"site.test", "/docs/", 200, "docs"},
		{"site.test", "/assets/app.js", 200, "js"},
		{"site.test", "/does-not-exist", 404, "not found"},
		{"site.test", "/../../etc/passwd", 404, "not found"},
		{"site.test", "/.env", 404, "not found"},
		{"site.test", "/.well-known/x", 200, "verification"},
		{"spa.test", "/users/42", 200, "spa"},
		{"empty.test", "/", 404, "no deployments yet"},
		{"other.test", "/", 404, ""},
	}
	for _, c := range cases {
		rec := get(srv, c.host, c.path)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s%s: expected %d %q, got %d %q", c.host, c.path, c.status, c.body, rec.Code, rec.Body.String())
		}
	}

	rec := get(srv, "site.test", "/assets/app.js")
	if rec.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" || rec.Header().Get("ETag") == "" {
		t.Errorf("cache headers: %v", rec.Header())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type of .js: %q", ct)
	}

	// Revalidation: with the same ETag the browser gets a 304.
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Host = "site.test"
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec304 := httptest.NewRecorder()
	srv.ServeHTTP(rec304, req)
	if rec304.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: expected 304, got %d", rec304.Code)
	}
}

// A site with a wildcard domain serves any single-level subdomain, the way
// Traefik routes it; another site's exact domain wins over it.
func TestServerWildcardHosts(t *testing.T) {
	app := deploy(t, map[string]string{"index.html": "app"})
	admin := deploy(t, map[string]string{"index.html": "admin"})
	srv := &Server{}
	srv.SetRoutes(map[string]Route{
		"app.test":       {Dir: app, DeploymentID: 1, SPA: true},
		"*.app.test":     {Dir: app, DeploymentID: 1, SPA: true},
		"admin.app.test": {Dir: admin, DeploymentID: 2},
	})
	cases := []struct {
		host, path string
		status     int
		body       string
	}{
		{"app.test", "/", 200, "app"},
		{"brasa.app.test", "/", 200, "app"},
		{"Carbon.App.test:443", "/tables/4", 200, "app"},
		{"admin.app.test", "/", 200, "admin"},
		// A wildcard covers a single level, just like the certificate.
		{"a.b.app.test", "/", 404, ""},
		{"other.test", "/", 404, ""},
	}
	for _, c := range cases {
		rec := get(srv, c.host, c.path)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s%s: expected %d %q, got %d %q", c.host, c.path, c.status, c.body, rec.Code, rec.Body.String())
		}
	}
}

func TestServerSwapIsAtomic(t *testing.T) {
	v1 := deploy(t, map[string]string{"index.html": "v1"})
	v2 := deploy(t, map[string]string{"index.html": "v2"})
	srv := &Server{}
	srv.SetRoutes(map[string]Route{"s.test": {Dir: v1, DeploymentID: 1}})
	etag1 := get(srv, "s.test", "/").Header().Get("ETag")

	srv.SetRoutes(map[string]Route{"s.test": {Dir: v2, DeploymentID: 2}})
	rec := get(srv, "s.test", "/")
	if rec.Body.String() != "v2" {
		t.Errorf("after publishing, v2 must be served, %q was served instead", rec.Body.String())
	}
	if rec.Header().Get("ETag") == etag1 {
		t.Error("the ETag must change between deployments to invalidate the browser cache")
	}
}

func makeTar(t *testing.T, entries map[string]string, symlinks map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		io.WriteString(tw, content)
	}
	for name, target := range symlinks {
		tw.WriteHeader(&tar.Header{Name: name, Linkname: target, Typeflag: tar.TypeSymlink})
	}
	tw.Close()
	return buf.Bytes()
}

func TestAddTar(t *testing.T) {
	src := makeTar(t, map[string]string{
		"web-abc/dist/index.html":   "home",
		"web-abc/dist/assets/a.js":  "js",
		"web-abc/distractor/x.html": "no",
		"web-abc/src/main.ts":       "no",
		"web-abc/../../outside.txt": "bad",
	}, map[string]string{"web-abc/dist/link": "/etc/passwd"})

	st := newStorage(t, DefaultLimits)
	s, _ := st.NewStaging(1)
	defer st.Discard(s)
	if err := s.AddTar(bytes.NewReader(src), "dist"); err != nil {
		t.Fatal(err)
	}
	if n, _, err := s.Finish(); err != nil || n != 2 {
		t.Fatalf("with prefix=dist there must be 2 files left, there are %d (%v)", n, err)
	}
	if readFile(t, filepath.Join(s.Dir, "index.html")) != "home" {
		t.Error("dist/index.html must end up at the root")
	}
	for _, p := range []string{"link", "main.ts", "x.html"} {
		if _, err := os.Lstat(filepath.Join(s.Dir, p)); err == nil {
			t.Errorf("%s should not be in the deployment", p)
		}
	}

	// With no prefix, everything hanging off the root folder gets in.
	s2, _ := st.NewStaging(1)
	defer st.Discard(s2)
	if err := s2.AddTar(bytes.NewReader(src), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s2.Dir, "src/main.ts")); err != nil {
		t.Error("with no prefix the whole tree must be included")
	}
}

func TestRedirectsAndHeaders(t *testing.T) {
	site := deploy(t, map[string]string{
		"index.html":     "home",
		"new/post.html":  "new post",
		"app/index.html": "app shell",
		"exists.html":    "real file",
		"_redirects": `# comment
/old              /new              301
/blog/*           /new/:splat       301
/u/:id            https://profile.com/:id
/app/*            /app/index.html   200
/exists           /index.html       200
/temporary        /                 307
`,
		"_headers": `/*
  X-Frame-Options: DENY
  Set-Cookie: evil=1; Domain=test
/assets/*
  Cache-Control: public, max-age=31536000, immutable
/no-frame
  ! X-Frame-Options
`,
		"assets/a.js":   "js",
		"no-frame.html": "embeddable",
	})
	srv := &Server{}
	srv.SetRoutes(map[string]Route{"s.test": {Dir: site, DeploymentID: 3}})

	redirect := func(path, wantLoc string, wantCode int) {
		t.Helper()
		rec := get(srv, "s.test", path)
		if rec.Code != wantCode || rec.Header().Get("Location") != wantLoc {
			t.Errorf("%s: %d %q, expected %d %q", path, rec.Code, rec.Header().Get("Location"), wantCode, wantLoc)
		}
	}
	redirect("/old", "/new", 301)
	redirect("/blog/2026/hello?ref=x", "/new/2026/hello?ref=x", 301)
	redirect("/u/oswa", "https://profile.com/oswa", 302) // no code: 302, as in Cloudflare Pages
	redirect("/temporary", "/", 307)

	cases := []struct{ path, body string }{
		{"/app/users/42", "app shell"}, // rewrite: same URL, another file
		{"/exists", "real file"},       // a rewrite does not override an existing file
		{"/new/post", "new post"},
	}
	for _, c := range cases {
		if rec := get(srv, "s.test", c.path); rec.Code != 200 || rec.Body.String() != c.body {
			t.Errorf("%s: %d %q, expected %q", c.path, rec.Code, rec.Body.String(), c.body)
		}
	}

	if h := get(srv, "s.test", "/").Header(); h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("the headers of /* must be applied: %v", h)
	}
	if h := get(srv, "s.test", "/assets/a.js").Header(); h.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("_headers must be able to override Dockfly's Cache-Control: %q", h.Get("Cache-Control"))
	}
	if h := get(srv, "s.test", "/no-frame").Header(); h.Get("X-Frame-Options") != "" {
		t.Errorf("«! Header» must remove it: %v", h)
	}
	if h := get(srv, "s.test", "/").Header(); h.Get("Set-Cookie") != "" {
		t.Errorf("_headers must not be able to set Set-Cookie: %q", h.Get("Set-Cookie"))
	}
	for _, p := range []string{"/_redirects", "/_headers"} {
		if rec := get(srv, "s.test", p); rec.Code != 404 {
			t.Errorf("%s must not be served: %d", p, rec.Code)
		}
	}
}
