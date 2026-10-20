package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewSlugAndHost(t *testing.T) {
	for in, want := range map[string]string{
		"feature/Nuevo_Login":                "feature-nuevo-login",
		"--raro//rama--":                     "raro-rama",
		"dependabot/npm_and_yarn/vite-5.0.1": "dependabot-npm-and-yarn-vite-5-0-1",
		"ñandú":                              "and",
		strings.Repeat("a", 60):              strings.Repeat("a", 40),
	} {
		if got := previewSlug(in); got != want {
			t.Errorf("previewSlug(%q) = %q, quería %q", in, got, want)
		}
	}
	if h := previewHost("previews.test", "web", "feature-x"); h != "feature-x--web.previews.test" {
		t.Errorf("host: %s", h)
	}
	// La etiqueta nunca pasa de 63 caracteres, aunque el sitio tenga un nombre largo.
	if l := previewLabel(strings.Repeat("b", 40), strings.Repeat("s", 60)); len(l) > 63 {
		t.Errorf("etiqueta de %d caracteres: %s", len(l), l)
	}
}

func TestPreviewsFlow(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, static, dyn := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/git/connections", `{"provider":"github","base_url":"`+gh.URL+`","token":"ghp_bueno"}`)
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.ejemplo.com"}`)
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"],"previews":true}`)
	c.do("PUT", "/api/sites/1/git", `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site"}`)
	secret := gh.secret(t, "101")
	hook := func(body string) (int, map[string]any) {
		return c.do("POST", "/api/hooks/github/1", body, "X-GitHub-Event", "push",
			"X-Hub-Signature-256", githubSignature(secret, []byte(body)))
	}
	push := `{"ref":"refs/heads/feature/login","head_commit":{"id":"sha1","message":"Login"}}`

	// Sin dominio de previews, las otras ramas se ignoran.
	if code, body := hook(push); code != 200 || !strings.Contains(body["status"].(string), "ignorado") {
		t.Fatalf("sin dominio de previews: %d %v", code, body)
	}
	if code, body := c.do("PUT", "/api/settings", `{"preview_domain":"*.previews.test"}`); code != 400 {
		t.Errorf("el dominio de previews no lleva comodín: %d %v", code, body)
	}
	code, body := c.do("PUT", "/api/settings", `{"preview_domain":"Previews.Test."}`)
	if code != 200 {
		t.Fatalf("guardar dominio de previews: %d %v", code, body)
	}
	set := body["settings"].(map[string]any)
	if set["preview_domain"] != "previews.test" || set["public_url"] != "https://dockfly.ejemplo.com" || set["preview_ssl"] != false {
		t.Errorf("ajustes: %v", set)
	}

	code, body = hook(push)
	if code != 202 || body["status"] != "en cola (preview)" {
		t.Fatalf("push a una rama: %d %v", code, body)
	}
	b := waitBuild(t, c, int(body["build_id"].(float64)))
	if b["status"] != "success" || b["preview"] != "feature-login" {
		t.Fatalf("build de la preview: %v", b)
	}
	if _, html := visit(static, "feature-login--web.previews.test", "/"); html != "desde git" {
		t.Errorf("la preview debe servirse en su dominio, se sirvió %q", html)
	}
	// Producción no cambia.
	if _, site := c.do("GET", "/api/sites/1", ""); site["site"].(map[string]any)["active_deployment_id"] != nil {
		t.Error("una preview no debe publicarse en producción")
	}
	cfg, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	if !strings.Contains(string(cfg), `\.previews\.test$`) {
		t.Errorf("Traefik debe enrutar el comodín de previews:\n%s", cfg)
	}
	code, list := c.doList("GET", "/api/sites/1/previews")
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["url"] != "http://feature-login--web.previews.test" ||
		list[0].(map[string]any)["branch"] != "feature/login" {
		t.Fatalf("listar previews: %d %v", code, list)
	}
	if _, deps := c.do("GET", "/api/sites/1", ""); len(deps["deployments"].([]any)) != 0 {
		t.Error("las previews no deben aparecer entre los despliegues de producción")
	}

	// Al borrar la rama se borra la preview.
	code, body = hook(`{"ref":"refs/heads/feature/login","deleted":true,"head_commit":null}`)
	if code != 200 || body["status"] != "preview borrada" {
		t.Fatalf("borrar la rama: %d %v", code, body)
	}
	if code, _ := visit(static, "feature-login--web.previews.test", "/"); code != 404 {
		t.Errorf("la preview borrada no debe servirse: %d", code)
	}
	if _, list := c.doList("GET", "/api/sites/1/previews"); len(list) != 0 {
		t.Errorf("no deben quedar previews: %v", list)
	}

	// Con las previews desactivadas en el sitio, otra rama se ignora.
	c.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"previews":false}`)
	if code, body := hook(push); code != 200 || !strings.Contains(body["status"].(string), "ignorado") {
		t.Errorf("previews desactivadas: %d %v", code, body)
	}
}
