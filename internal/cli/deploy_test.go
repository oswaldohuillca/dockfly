package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploy(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hola</h1>"), 0o644)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0o644)

	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dfy_ok" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"token de API inválido, caducado o revocado"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/sites":
			io.WriteString(w, `[{"id":7,"name":"web","domains":["web.test"],"ssl":true}]`)
		case r.Method == "POST" && r.URL.Path == "/api/sites/7/deployments" && r.Header.Get("X-Dockfly-Upload") == "1":
			mr, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				got[p.FormName()] = string(b)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"id":3,"file_count":2,"total_bytes":27}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	if code := Deploy([]string{dir, "--site", "web", "--url", srv.URL + "/", "--token", "dfy_ok"}, &out, &errOut); code != 0 {
		t.Fatalf("código %d: %s", code, errOut.String())
	}
	if len(got) != 2 || got["file:index.html"] != "<h1>hola</h1>" || got["file:assets/app.js"] != "console.log(1)" {
		t.Errorf("archivos subidos: %v", got)
	}
	if !strings.Contains(out.String(), "Despliegue #3 publicado") || !strings.Contains(out.String(), "https://web.test") {
		t.Errorf("salida: %s", out.String())
	}

	// Por variables de entorno, la carpeta después de las opciones.
	t.Setenv("DOCKFLY_URL", srv.URL)
	t.Setenv("DOCKFLY_TOKEN", "dfy_malo")
	errOut.Reset()
	if code := Deploy([]string{"--site", "web", dir}, io.Discard, &errOut); code != 1 || !strings.Contains(errOut.String(), "token de API inválido") {
		t.Errorf("token inválido: %d %s", code, errOut.String())
	}
	if code := Deploy([]string{dir}, io.Discard, io.Discard); code != 2 {
		t.Errorf("sin --site debe mostrar el uso: %d", code)
	}
	t.Setenv("DOCKFLY_TOKEN", "dfy_ok")
	errOut.Reset()
	if code := Deploy([]string{dir, "--site", "otro"}, io.Discard, &errOut); code != 1 || !strings.Contains(errOut.String(), `"otro"`) {
		t.Errorf("sitio inexistente: %d %s", code, errOut.String())
	}
}
