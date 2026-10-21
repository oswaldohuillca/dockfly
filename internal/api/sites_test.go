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

// upload envía un despliegue. Con zipFiles != nil sube un .zip; si no, sube
// files como si fuera una carpeta arrastrada al navegador.
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
		w, _ := mw.CreateFormFile("archive", "sitio.zip")
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

// visit pide una ruta al servidor estático como lo haría Traefik.
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
		t.Fatalf("crear sitio: %d %v", code, body)
	}
	if body["name"] != "mi-web" {
		t.Errorf("el nombre debe normalizarse a minúsculas: %v", body["name"])
	}
	if code, _ := c.do("POST", "/api/sites", `{"name":"mi web","domains":["x.test"]}`); code != 400 {
		t.Errorf("nombre con espacios: esperaba 400, obtuve %d", code)
	}
	if code, _ := c.do("POST", "/api/sites", `{"name":"mi-web","domains":["otro.test"]}`); code != 409 {
		t.Errorf("nombre repetido: esperaba 409, obtuve %d", code)
	}

	// Un dominio no puede estar en un sitio y en un proxy host a la vez.
	if code, body := c.do("POST", "/api/proxy-hosts",
		`{"domains":["web.test"],"target_type":"external","forward_host":"1.1.1.1","forward_port":80}`); code != 409 ||
		body["code"] != "domain_in_use" {
		t.Errorf("dominio de un sitio en un proxy host: %d %v", code, body)
	}

	// Sin despliegues, Traefik ya enruta el dominio y se ve el aviso.
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	if !strings.Contains(string(cfg), "site-1-secure") || !strings.Contains(string(cfg), "http://127.0.0.1:8099") {
		t.Fatalf("el sitio no está en la configuración de Traefik:\n%s", cfg)
	}
	if _, html := visit(static, "web.test", "/"); !strings.Contains(html, "todavía no tiene despliegues") {
		t.Errorf("sin despliegues debe verse el aviso, se vio %q", html)
	}

	// Despliegue 1: una carpeta.
	code, body = c.upload(1, map[string]string{"index.html": "v1", "css/app.css": "body{}"}, nil, true)
	if code != http.StatusCreated || body["file_count"] != float64(2) {
		t.Fatalf("subir carpeta: %d %v", code, body)
	}
	if _, html := visit(static, "web.test", "/"); html != "v1" {
		t.Errorf("tras el despliegue 1 debe servirse v1, se sirvió %q", html)
	}
	if _, css := visit(static, "web.test", "/css/app.css"); css != "body{}" {
		t.Errorf("la ruta de la carpeta debe conservarse, se sirvió %q", css)
	}

	// Despliegue 2: un zip con todo dentro de dist/ (se sube un nivel).
	code, _ = c.upload(1, nil, map[string]string{"dist/index.html": "v2", "dist/about.html": "acerca"}, true)
	if code != http.StatusCreated {
		t.Fatalf("subir zip: %d", code)
	}
	if _, html := visit(static, "web.test", "/"); html != "v2" {
		t.Errorf("tras el despliegue 2 debe servirse v2, se sirvió %q", html)
	}
	if _, html := visit(static, "web.test", "/about"); html != "acerca" {
		t.Errorf("URL limpia /about: se sirvió %q", html)
	}

	// Rollback al despliegue 1.
	if code, _ := c.do("POST", "/api/sites/1/deployments/1/activate", ""); code != http.StatusNoContent {
		t.Fatalf("rollback: %d", code)
	}
	if _, html := visit(static, "web.test", "/"); html != "v1" {
		t.Errorf("tras el rollback debe servirse v1, se sirvió %q", html)
	}

	// El publicado no se puede borrar; el otro sí.
	if code, _ := c.do("DELETE", "/api/sites/1/deployments/1", ""); code != http.StatusConflict {
		t.Errorf("borrar el publicado: esperaba 409, obtuve %d", code)
	}
	if code, _ := c.do("DELETE", "/api/sites/1/deployments/2", ""); code != http.StatusNoContent {
		t.Errorf("borrar despliegue: esperaba 204, obtuve %d", code)
	}
	if code, body := c.do("GET", "/api/sites/1", ""); code != 200 || len(body["deployments"].([]any)) != 1 {
		t.Errorf("detalle del sitio: %d %v", code, body)
	}

	// Activar el modo SPA al editar.
	if code, _ := c.do("PUT", "/api/sites/1", `{"name":"mi-web","domains":["web.test"],"spa":true}`); code != 200 {
		t.Fatalf("editar sitio: %d", code)
	}
	if _, html := visit(static, "web.test", "/usuarios/42"); html != "v1" {
		t.Errorf("en modo SPA cualquier ruta debe servir index.html, se sirvió %q", html)
	}

	// Borrar el sitio deja de servirlo y elimina sus archivos.
	if code, _ := c.do("DELETE", "/api/sites/1", ""); code != http.StatusNoContent {
		t.Fatalf("borrar sitio: %d", code)
	}
	if code, _ := visit(static, "web.test", "/"); code != 404 {
		t.Errorf("tras borrar el sitio debe dar 404, dio %d", code)
	}
}

func TestUploadValidation(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/sites", `{"name":"s","domains":["s.test"]}`)

	if code, _ := c.upload(1, map[string]string{"index.html": "x"}, nil, false); code != http.StatusUnsupportedMediaType {
		t.Errorf("sin la cabecera anti-CSRF: esperaba 415, obtuve %d", code)
	}
	if code, _ := c.upload(1, map[string]string{}, nil, true); code != 400 {
		t.Errorf("subida vacía: esperaba 400, obtuve %d", code)
	}
	// MaxUploadMB = 1 en el servidor de pruebas.
	if code, _ := c.upload(1, map[string]string{"grande.bin": strings.Repeat("x", 2<<20)}, nil, true); code != http.StatusRequestEntityTooLarge {
		t.Errorf("subida demasiado grande: esperaba 413, obtuve %d", code)
	}
	if code, _ := c.upload(99, map[string]string{"index.html": "x"}, nil, true); code != 404 {
		t.Errorf("sitio inexistente: esperaba 404, obtuve %d", code)
	}
	if code, body := c.do("GET", "/api/sites/1", ""); len(body["deployments"].([]any)) != 0 {
		t.Errorf("las subidas fallidas no deben crear despliegues: %d %v", code, body)
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
			t.Fatalf("subida %d: %d", i, code)
		}
	}

	_, body := c.do("GET", "/api/sites/1", "")
	deps := body["deployments"].([]any)
	if len(deps) != keepDeployments {
		t.Fatalf("deben quedar %d despliegues, quedan %d", keepDeployments, len(deps))
	}
	// Se conservan los más recientes.
	newest := deps[0].(map[string]any)["id"].(float64)
	oldest := deps[len(deps)-1].(map[string]any)["id"].(float64)
	if newest != float64(total) || oldest != float64(total-keepDeployments+1) {
		t.Errorf("se conservaron los despliegues %v..%v, esperaba %d..%d", oldest, newest, total-keepDeployments+1, total)
	}
	if _, html := visit(static, "s.test", "/"); html != fmt.Sprint("v", total) {
		t.Errorf("debe servirse el último subido, se sirvió %q", html)
	}
	// Se puede volver al más antiguo que queda, pero no a uno ya borrado.
	if code, _ := c.do("POST", fmt.Sprintf("/api/sites/1/deployments/%d/activate", int(oldest)), ""); code != 204 {
		t.Errorf("rollback al más antiguo conservado: %d", code)
	}
	if code, _ := c.do("POST", "/api/sites/1/deployments/1/activate", ""); code != 404 {
		t.Errorf("activar un despliegue borrado: esperaba 404, obtuve %d", code)
	}
}
