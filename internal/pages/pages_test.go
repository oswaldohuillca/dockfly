package pages

import (
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
		"../../escapado.txt":    "malo",
		"/etc/absoluto.txt":     "malo",
		`..\..\windows.txt`:     "malo",
		"index.html":            "ok",
		"__MACOSX/._index.html": "basura",
		"sub/.DS_Store":         "basura",
	})
	if err := s.AddZip(bytes.NewReader(z), int64(len(z))); err != nil {
		t.Fatal(err)
	}
	// Las rutas maliciosas quedan ancladas dentro del despliegue.
	for _, p := range []string{"escapado.txt", "etc/absoluto.txt", "windows.txt", "index.html"} {
		if _, err := os.Stat(filepath.Join(s.Dir, p)); err != nil {
			t.Errorf("%s debería estar dentro del despliegue: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(st.Root), "escapado.txt")); err == nil {
		t.Fatal("zip slip: se escribió fuera del directorio")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "__MACOSX")); err == nil {
		t.Error("__MACOSX debe ignorarse")
	}
}

func TestSymlinkInsideStagingCannotEscape(t *testing.T) {
	st := newStorage(t, DefaultLimits)
	s, _ := st.NewStaging(1)
	defer st.Discard(s)
	outside := t.TempDir()
	// Simulamos un symlink plantado en el directorio (p. ej. por un zip que
	// los incluyera): os.Root no debe seguirlo hacia fuera.
	if err := os.Symlink(outside, filepath.Join(s.Dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("link/pwned.txt", strings.NewReader("x")); err == nil {
		t.Error("escribir a través de un symlink hacia fuera debe fallar")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Fatal("se escribió fuera a través del symlink")
	}
}

func TestLimits(t *testing.T) {
	t.Run("bytes (zip bomb)", func(t *testing.T) {
		st := newStorage(t, Limits{MaxFiles: 100, MaxBytes: 1000})
		s, _ := st.NewStaging(1)
		defer st.Discard(s)
		z := makeZip(t, map[string]string{"bomba.txt": strings.Repeat("A", 1_000_000)})
		if len(z) > 10_000 {
			t.Fatalf("el zip de prueba debería comprimir mucho, ocupa %d", len(z))
		}
		err := s.AddZip(bytes.NewReader(z), int64(len(z)))
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("esperaba ErrInvalid por tamaño, obtuve %v", err)
		}
		fi, _ := os.Stat(filepath.Join(s.Dir, "bomba.txt"))
		if fi != nil && fi.Size() > 1001 {
			t.Errorf("se descomprimieron %d bytes, más del límite", fi.Size())
		}
	})
	t.Run("archivos", func(t *testing.T) {
		st := newStorage(t, Limits{MaxFiles: 2, MaxBytes: 1 << 20})
		s, _ := st.NewStaging(1)
		defer st.Discard(s)
		for _, n := range []string{"a", "b"} {
			if err := s.Add(n, strings.NewReader("x")); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add("c", strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
			t.Errorf("esperaba ErrInvalid por número de archivos, obtuve %v", err)
		}
	})
	t.Run("duplicados y vacío", func(t *testing.T) {
		st := newStorage(t, DefaultLimits)
		s, _ := st.NewStaging(1)
		defer st.Discard(s)
		if _, _, err := s.Finish(); !errors.Is(err, ErrInvalid) {
			t.Errorf("un despliegue vacío debe ser inválido: %v", err)
		}
		s.Add("a.html", strings.NewReader("1"))
		if err := s.Add("./a.html", strings.NewReader("2")); !errors.Is(err, ErrInvalid) {
			t.Errorf("un archivo duplicado debe ser inválido: %v", err)
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
		t.Fatalf("Finish: %d archivos, %d bytes, %v", files, size, err)
	}
	if err := st.Commit(s, 7, 42); err != nil {
		t.Fatal(err)
	}
	dir := st.DeploymentDir(7, 42)
	if readFile(t, filepath.Join(dir, "index.html")) != "home" || readFile(t, filepath.Join(dir, "assets/app.js")) != "js" {
		t.Error("el contenido de dist/ debe quedar en la raíz del despliegue")
	}
}

func TestCleanStaging(t *testing.T) {
	st := newStorage(t, DefaultLimits)
	s, _ := st.NewStaging(1)
	s.Add("index.html", strings.NewReader("x"))
	s.close() // simula una caída a mitad de subida
	if err := st.CleanStaging(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Error("CleanStaging debe borrar los despliegues a medio subir")
	}
}

// --- Servidor ---

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
		"404.html":        "no encontrado",
		"assets/app.js":   "js",
		".env":            "SECRETO=1",
		".well-known/x":   "verificación",
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
		{"site.test", "/no-existe", 404, "no encontrado"},
		{"site.test", "/../../etc/passwd", 404, "no encontrado"},
		{"site.test", "/.env", 404, "no encontrado"},
		{"site.test", "/.well-known/x", 200, "verificación"},
		{"spa.test", "/usuarios/42", 200, "spa"},
		{"empty.test", "/", 404, "todavía no tiene despliegues"},
		{"otro.test", "/", 404, ""},
	}
	for _, c := range cases {
		rec := get(srv, c.host, c.path)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s%s: esperaba %d %q, obtuve %d %q", c.host, c.path, c.status, c.body, rec.Code, rec.Body.String())
		}
	}

	rec := get(srv, "site.test", "/assets/app.js")
	if rec.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" || rec.Header().Get("ETag") == "" {
		t.Errorf("cabeceras de caché: %v", rec.Header())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type de .js: %q", ct)
	}

	// Revalidación: con el mismo ETag el navegador recibe 304.
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Host = "site.test"
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec304 := httptest.NewRecorder()
	srv.ServeHTTP(rec304, req)
	if rec304.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: esperaba 304, obtuve %d", rec304.Code)
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
		t.Errorf("tras publicar debe servirse v2, se sirvió %q", rec.Body.String())
	}
	if rec.Header().Get("ETag") == etag1 {
		t.Error("el ETag debe cambiar entre despliegues para invalidar la caché del navegador")
	}
}
