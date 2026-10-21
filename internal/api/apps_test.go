package api

import (
	"os"
	"strings"
	"testing"
)

func TestAppsAPI(t *testing.T) {
	ts, _, dynDir := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)

	bad := map[string]string{
		"nombre inválido":     `{"name":"Mi App","source":"image","image":"nginx"}`,
		"imagen inválida":     `{"name":"a","source":"image","image":"no válida!"}`,
		"dominio sin puerto":  `{"name":"a","source":"image","image":"nginx","domains":["a.test"]}`,
		"volumen relativo":    `{"name":"a","source":"image","image":"nginx","volumes":[{"name":"data","path":"data"}]}`,
		"comillas sin cerrar": `{"name":"a","source":"image","image":"nginx","command":"sh -c 'x"}`,
		"git sin repositorio": `{"name":"a","source":"git"}`,
	}
	for name, body := range bad {
		if code, res := c.do("POST", "/api/apps", body); code != 400 {
			t.Errorf("%s: esperaba 400, obtuve %d %v", name, code, res)
		}
	}
	if code, res := c.do("POST", "/api/apps", `{"name":"a","source":"image","image":"nginx","port":80,"domains":["web.test"]}`); code != 409 ||
		res["code"] != "domain_in_use" || res["params"].(map[string]any)["kind"] != "site" || res["params"].(map[string]any)["domain"] != "web.test" {
		t.Errorf("un dominio de un sitio no puede usarlo una app: %d %v", code, res)
	}

	code, res := c.do("POST", "/api/apps", `{"name":"api","source":"image","image":"ghcr.io/oswa/api:1.2","port":3000,
		"domains":["API.test"],"ssl":true,"env":{"DATABASE_URL":"postgres://postgres:5432/db"},
		"volumes":[{"name":"data","path":"/data"}],"health_path":"health","deploy":true}`)
	if code != 201 {
		t.Fatalf("crear app: %d %v", code, res)
	}
	app := res["app"].(map[string]any)
	if app["health_path"] != "/health" || app["domains"].([]any)[0] != "api.test" {
		t.Errorf("la app debe normalizarse: %v", app)
	}
	// Sin Docker, desplegar no es posible: se avisa sin perder la app creada.
	if !strings.Contains(res["warning"].(string), "Docker no está disponible") {
		t.Errorf("aviso sin Docker: %v", res["warning"])
	}
	if _, env := c.do("GET", "/api/apps/1/env", ""); env["DATABASE_URL"] != "postgres://postgres:5432/db" {
		t.Errorf("variables de entorno: %v", env)
	}

	// El nombre no cambia (de él dependen volúmenes y red) y env: null conserva las variables.
	code, res = c.do("PUT", "/api/apps/1", `{"name":"otro","source":"image","image":"ghcr.io/oswa/api:1.3","port":3000,"domains":["api.test"],"env":null}`)
	if code != 200 || res["app"].(map[string]any)["name"] != "api" || res["app"].(map[string]any)["image"] != "ghcr.io/oswa/api:1.3" {
		t.Fatalf("editar: %d %v", code, res)
	}
	if _, env := c.do("GET", "/api/apps/1/env", ""); env["DATABASE_URL"] == nil {
		t.Error("env: null debe conservar las variables")
	}

	// Sin desplegar no hay ruta en Traefik (no hay contenedor al que enviar tráfico).
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	if strings.Contains(string(cfg), "app-1") {
		t.Errorf("una app sin desplegar no debe tener ruta:\n%s", cfg)
	}
	if code, _ := c.do("POST", "/api/apps/1/stop", ""); code != 409 {
		t.Errorf("detener una app sin desplegar: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/apps/1", ""); code != 204 {
		t.Errorf("borrar: %d", code)
	}
}
