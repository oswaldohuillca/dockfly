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
		t.Errorf("sin nombre: %d", code)
	}
	code, body := admin.do("POST", "/api/tokens", `{"name":"GitHub Actions","role":"deployer","expires_days":30}`)
	if code != 201 {
		t.Fatalf("crear token: %d %v", code, body)
	}
	token := body["token"].(string)
	meta := body["api_token"].(map[string]any)
	if len(token) != 52 || meta["prefix"] != token[:10] || meta["expires_at"] == nil {
		t.Errorf("token: %s %v", token, meta)
	}
	if _, list := admin.doList("GET", "/api/tokens"); len(list) != 1 {
		t.Fatalf("listar tokens: %v", list)
	} else if _, has := list[0].(map[string]any)["token_hash"]; has {
		t.Error("la lista nunca debe incluir el hash")
	}

	// El token sirve sin cookie de sesión.
	cli := newClient(t, ts)
	bearer := func(method, path, body string) (int, map[string]any) {
		return cli.do(method, path, body, "Authorization", "Bearer "+token)
	}
	if code, _ := bearer("GET", "/api/sites/1", ""); code != 200 {
		t.Errorf("leer con token: %d", code)
	}
	// Subir un despliegue, como hará el CLI.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file:index.html", "index.html")
	fmt.Fprint(fw, "desde el CLI")
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/sites/1/deployments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(uploadHeader, "1")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("subir con token: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()
	if _, html := visit(static, "web.test", "/"); html != "desde el CLI" {
		t.Errorf("se sirvió %q", html)
	}

	// Con un token de despliegue no se toca lo de administrador, ni la cuenta, ni otros tokens.
	if code, _ := bearer("POST", "/api/proxy-hosts", `{}`); code != 403 {
		t.Errorf("token deployer en una ruta de admin: %d", code)
	}
	if code, _ := bearer("POST", "/api/tokens", `{"name":"x","role":"admin"}`); code != 403 {
		t.Errorf("un token no debe crear tokens: %d", code)
	}
	if code, _ := bearer("PUT", "/api/auth/password", `{"current":"secreto123","new":"otra-clave"}`); code != 403 {
		t.Errorf("un token no debe cambiar la contraseña: %d", code)
	}
	if code, _ := cli.do("GET", "/api/sites", "", "Authorization", "Bearer dfy_falso"); code != 401 {
		t.Errorf("token falso: %d", code)
	}
	// Un Authorization inválido no se salta con una cookie válida.
	if code, _ := admin.do("GET", "/api/sites", "", "Authorization", "Bearer dfy_falso"); code != 401 {
		t.Errorf("cookie válida con token falso: %d", code)
	}

	// Un token nunca puede más que su usuario, aunque el usuario baje de rol después.
	s := serverOf(t, ts)
	_, inv := admin.do("POST", "/api/users", `{"name":"Mira","email":"mira@test.com","role":"deployer"}`)
	viewer := newClient(t, ts)
	accept(t, viewer, inv["link"].(string), "mira-secreto")
	if code, _ := viewer.do("POST", "/api/tokens", `{"name":"x","role":"admin"}`); code != 403 {
		t.Errorf("token con más rol que el usuario: %d", code)
	}
	_, vt := viewer.do("POST", "/api/tokens", `{"name":"ci","role":"deployer"}`)
	admin.do("PUT", "/api/users/2", `{"name":"Mira","role":"viewer"}`)
	if code, _ := viewer.do("POST", "/api/sites/1/builds", "", "Authorization", "Bearer "+vt["token"].(string)); code != 403 {
		t.Errorf("el token debe perder permisos con su usuario: %d", code)
	}

	// Caducado o revocado: fuera.
	old := newToken()
	past := time.Now().Add(-time.Hour)
	if err := s.Store.CreateAPIToken(context.Background(), &store.APIToken{
		UserID: 1, Name: "viejo", Role: "admin", TokenHash: hashToken(old), Prefix: old[:10], ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := cli.do("GET", "/api/sites", "", "Authorization", "Bearer "+old); code != 401 {
		t.Errorf("token caducado: %d", code)
	}
	if code, _ := admin.do("DELETE", "/api/tokens/1", ""); code != 204 {
		t.Errorf("revocar: %d", code)
	}
	if code, _ := viewer.do("DELETE", "/api/tokens/1", ""); code != 404 {
		t.Errorf("no se pueden revocar tokens ajenos: %d", code)
	}
}
