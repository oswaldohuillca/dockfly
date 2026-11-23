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

// restore uploads a backup; fields are the fields that come first (password,
// setup_token).
func (c *client) restore(backup []byte, fields ...string) (int, map[string]any) {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i+1 < len(fields); i += 2 {
		mw.WriteField(fields[i], fields[i+1])
	}
	w, _ := mw.CreateFormFile("backup", "backup.tar.gz")
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
		t.Errorf("downloading a backup with no session: expected 401, got %d", resp.StatusCode)
	}

	resp, err := admin.http.Get(ts.URL + "/api/backup")
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/gzip" || len(archive) < 100 {
		t.Fatalf("download backup: %d %s (%d bytes)", resp.StatusCode, resp.Header.Get("Content-Type"), len(archive))
	}

	// Once there are users, restoring requires a session: otherwise anyone
	// could replace every bit of data.
	if code, _ := anon.restore(archive); code != 401 {
		t.Errorf("restoring with no session on an install with users: expected 401, got %d", code)
	}
	// Only an administrator, and only by confirming their password: restoring
	// replaces the database and the keys.
	for _, role := range []string{"viewer", "deployer"} {
		_, inv := admin.do("POST", "/api/users", `{"name":"`+role+`","email":"`+role+`@test.com","role":"`+role+`"}`)
		other := newClient(t, ts)
		accept(t, other, inv["link"].(string), "password-"+role)
		if code, body := other.restore(archive, "password", "password-"+role); code != 403 || body["code"] != "role_forbidden" {
			t.Errorf("restoring as %s: %d %v", role, code, body)
		}
	}
	if code, body := admin.restore(archive); code != 403 || body["code"] != "wrong_password" {
		t.Errorf("restoring without confirming the password: %d %v", code, body)
	}
	if code, body := admin.restore(archive, "password", "wrong"); code != 403 {
		t.Errorf("restoring with a wrong password: %d %v", code, body)
	}
	if code, body := admin.restore([]byte("junk"), "password", "secret123"); code != 400 {
		t.Errorf("restoring garbage: %d %v", code, body)
	}
	if code, body := admin.restore(archive, "password", "secret123"); code != 202 {
		t.Errorf("restore: %d %v", code, body)
	}

	// On a fresh install you restore with no session (a migration), but with
	// the setup code.
	fresh, _, _ := testServerWithPages(t)
	token, err := serverOf(t, fresh).PrepareSetupToken(t.Context())
	if err != nil || len(token) < 20 {
		t.Fatalf("setup code: %q %v", token, err)
	}
	if code, body := newClient(t, fresh).restore(archive); code != 403 || body["code"] != "setup_token_invalid" {
		t.Errorf("restoring on a fresh install with no code: %d %v", code, body)
	}
	if code, body := newClient(t, fresh).restore(archive, "setup_token", strings.ToUpper(token)); code != 202 {
		t.Errorf("restoring on a fresh install with the code: %d %v", code, body)
	}
}

// Signing up the first administrator requires the setup code, which is
// deleted as soon as there is an administrator.
func TestSetupToken(t *testing.T) {
	ts, _ := testServer(t)
	s := serverOf(t, ts)
	token, err := s.PrepareSetupToken(t.Context())
	if err != nil || token == "" {
		t.Fatal(token, err)
	}
	if again, _ := s.PrepareSetupToken(t.Context()); again != token {
		t.Error("restarting with no users must reuse the same code")
	}
	c := newClient(t, ts)
	if code, body := c.do("POST", "/api/setup", adminJSON); code != 403 || body["code"] != "setup_token_invalid" {
		t.Errorf("signup with no code: %d %v", code, body)
	}
	withToken := strings.Replace(adminJSON, `"password"`, `"setup_token":"`+token+`","password"`, 1)
	if code, body := c.do("POST", "/api/setup", withToken); code != 201 {
		t.Fatalf("signup with the code: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, setupTokenFile)); !os.IsNotExist(err) {
		t.Error("the code must be deleted after signup")
	}
	if again, _ := s.PrepareSetupToken(t.Context()); again != "" {
		t.Error("no code must be generated once there are users")
	}
}
