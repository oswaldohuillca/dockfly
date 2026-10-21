package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// restore sube una copia; fields son campos previos (password, setup_token).
func (c *client) restore(backup []byte, fields ...string) (int, map[string]any) {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i+1 < len(fields); i += 2 {
		mw.WriteField(fields[i], fields[i+1])
	}
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
	// Solo un administrador, y confirmando su contraseña: restaurar
	// sustituye la base de datos y las claves.
	for _, role := range []string{"viewer", "deployer"} {
		_, inv := admin.do("POST", "/api/users", `{"name":"`+role+`","email":"`+role+`@test.com","role":"`+role+`"}`)
		other := newClient(t, ts)
		accept(t, other, inv["link"].(string), "clave-"+role)
		if code, body := other.restore(archive, "password", "clave-"+role); code != 403 || body["code"] != "role_forbidden" {
			t.Errorf("restaurar como %s: %d %v", role, code, body)
		}
	}
	if code, body := admin.restore(archive); code != 403 || body["code"] != "wrong_password" {
		t.Errorf("restaurar sin confirmar la contraseña: %d %v", code, body)
	}
	if code, body := admin.restore(archive, "password", "mala"); code != 403 {
		t.Errorf("restaurar con una contraseña incorrecta: %d %v", code, body)
	}
	if code, body := admin.restore([]byte("basura"), "password", "secreto123"); code != 400 {
		t.Errorf("restaurar basura: %d %v", code, body)
	}
	if code, body := admin.restore(archive, "password", "secreto123"); code != 202 {
		t.Errorf("restaurar: %d %v", code, body)
	}

	// En una instalación nueva se restaura sin sesión (migración), pero con
	// el código de instalación.
	fresh, _, _ := testServerWithPages(t)
	token, err := serverOf(t, fresh).PrepareSetupToken(t.Context())
	if err != nil || len(token) < 20 {
		t.Fatalf("código de instalación: %q %v", token, err)
	}
	if code, body := newClient(t, fresh).restore(archive); code != 403 || body["code"] != "setup_token_invalid" {
		t.Errorf("restaurar en una instalación nueva sin código: %d %v", code, body)
	}
	if code, body := newClient(t, fresh).restore(archive, "setup_token", strings.ToUpper(token)); code != 202 {
		t.Errorf("restaurar en una instalación nueva con el código: %d %v", code, body)
	}
}

// El alta del primer administrador exige el código de instalación, que se
// borra en cuanto hay administrador.
func TestSetupToken(t *testing.T) {
	ts, _ := testServer(t)
	s := serverOf(t, ts)
	token, err := s.PrepareSetupToken(t.Context())
	if err != nil || token == "" {
		t.Fatal(token, err)
	}
	if again, _ := s.PrepareSetupToken(t.Context()); again != token {
		t.Error("al reiniciar sin usuarios debe reutilizarse el mismo código")
	}
	c := newClient(t, ts)
	if code, body := c.do("POST", "/api/setup", adminJSON); code != 403 || body["code"] != "setup_token_invalid" {
		t.Errorf("alta sin código: %d %v", code, body)
	}
	withToken := strings.Replace(adminJSON, `"password"`, `"setup_token":"`+token+`","password"`, 1)
	if code, body := c.do("POST", "/api/setup", withToken); code != 201 {
		t.Fatalf("alta con código: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, setupTokenFile)); !os.IsNotExist(err) {
		t.Error("el código debe borrarse tras el alta")
	}
	if again, _ := s.PrepareSetupToken(t.Context()); again != "" {
		t.Error("con usuarios no debe generarse código")
	}
}
