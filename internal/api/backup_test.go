package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
)

func (c *client) restore(backup []byte) (int, map[string]any) {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	w, _ := mw.CreateFormFile("backup", "copia.tar.gz")
	w.Write(backup)
	mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/api/backup/restore", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(uploadHeader, "1")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func TestBackupEndpoints(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)

	anon := newClient(t, ts)
	if resp, _ := anon.http.Get(ts.URL + "/api/backup"); resp.StatusCode != 401 {
		t.Errorf("descargar una copia sin sesión: esperaba 401, obtuve %d", resp.StatusCode)
	}

	resp, err := admin.http.Get(ts.URL + "/api/backup")
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/gzip" || len(archive) < 100 {
		t.Fatalf("descargar copia: %d %s (%d bytes)", resp.StatusCode, resp.Header.Get("Content-Type"), len(archive))
	}

	// Con usuarios, restaurar exige sesión: si no, cualquiera podría
	// sustituir todos los datos.
	if code, _ := anon.restore(archive); code != 401 {
		t.Errorf("restaurar sin sesión en una instalación con usuarios: esperaba 401, obtuve %d", code)
	}
	if code, body := admin.restore([]byte("basura")); code != 400 {
		t.Errorf("restaurar basura: %d %v", code, body)
	}
	if code, body := admin.restore(archive); code != 202 {
		t.Errorf("restaurar: %d %v", code, body)
	}

	// En una instalación nueva se puede restaurar sin sesión (migración).
	fresh, _, _ := testServerWithPages(t)
	if code, body := newClient(t, fresh).restore(archive); code != 202 {
		t.Errorf("restaurar en una instalación nueva: %d %v", code, body)
	}
}
