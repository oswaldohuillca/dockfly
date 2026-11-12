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
	if !strings.Contains(res["deploy_error"].(string), "Docker no está disponible") {
		t.Errorf("aviso sin Docker: %v", res["deploy_error"])
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

	// La consola: sin comando no se ejecuta nada, y sin desplegar no hay contenedor.
	if code, res := c.do("POST", "/api/apps/1/exec", `{"command":"  "}`); code != 400 || res["code"] != "app_exec_empty" {
		t.Errorf("comando vacío: %d %v", code, res)
	}
	if code, res := c.do("POST", "/api/apps/1/exec", `{"command":"ls"}`); code != 409 || res["code"] != "app_not_deployed" {
		t.Errorf("exec sin desplegar: %d %v", code, res)
	}

	// Tareas programadas: se validan el comando y el horario, y son de su app.
	for name, body := range map[string]string{
		"sin comando":      `{"command":" ","schedule":"* * * * *","enabled":true}`,
		"horario inválido": `{"command":"ls","schedule":"cada hora","enabled":true}`,
	} {
		if code, res := c.do("POST", "/api/apps/1/jobs", body); code != 400 {
			t.Errorf("tarea %s: esperaba 400, obtuve %d %v", name, code, res)
		}
	}
	code, res = c.do("POST", "/api/apps/1/jobs", `{"command":" php artisan schedule:run ","schedule":"*/5  * * * *","enabled":true}`)
	if code != 201 || res["command"] != "php artisan schedule:run" || res["schedule"] != "*/5 * * * *" {
		t.Fatalf("crear tarea: %d %v", code, res)
	}
	if code, res := c.do("PUT", "/api/apps/1/jobs/1", `{"command":"ls","schedule":"0 3 * * *","enabled":false}`); code != 200 || res["enabled"] != false {
		t.Errorf("editar tarea: %d %v", code, res)
	}
	if code, res := c.do("POST", "/api/apps/1/jobs/1/run", ""); code != 409 || res["code"] != "job_app_not_running" {
		t.Errorf("ejecutar una tarea sin desplegar: %d %v", code, res)
	}
	if code, _ := c.do("DELETE", "/api/apps/2/jobs/1", ""); code != 404 {
		t.Errorf("una tarea no se borra desde otra app: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/apps/1/jobs/1", ""); code != 204 {
		t.Errorf("borrar tarea: %d", code)
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

// Dos apps no pueden acabar en el mismo volumen de Docker
// (dockfly-app-<app>-<volumen>): "shop"+"db-data" = "shop-db"+"data".
func TestAppVolumesNotShared(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	if code, res := c.do("POST", "/api/apps", `{"name":"shop","source":"image","image":"nginx","volumes":[{"name":"db-data","path":"/d"}]}`); code != 201 {
		t.Fatalf("crear shop: %d %v", code, res)
	}
	code, res := c.do("POST", "/api/apps", `{"name":"shop-db","source":"image","image":"postgres","volumes":[{"name":"data","path":"/var/lib/postgresql/data"}]}`)
	if code != 409 || res["code"] != "app_volume_shared" {
		t.Errorf("volumen compartido: %d %v", code, res)
	}
	if code, _ := c.do("POST", "/api/apps", `{"name":"shop-db","source":"image","image":"postgres","volumes":[{"name":"pg","path":"/var/lib/postgresql/data"}]}`); code != 201 {
		t.Errorf("con otro nombre sí: %d", code)
	}
}

// Los valores de las variables se guardan tal cual, también con ":" o "=".
func TestAppEnvValuesKeptVerbatim(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	if code, res := c.do("POST", "/api/apps", `{"name":"api","source":"image","image":"nginx"}`); code != 201 {
		t.Fatalf("crear: %d %v", code, res)
	}
	put := `{"name":"api","source":"image","image":"nginx","env":{"KEY":"foo:bar","URL":"postgres://u:p@db:5432/x?a=b"}}`
	if code, res := c.do("PUT", "/api/apps/1", put); code != 200 {
		t.Fatalf("guardar: %d %v", code, res)
	}
	_, env := c.do("GET", "/api/apps/1/env", "")
	if env["KEY"] != "foo:bar" || env["URL"] != "postgres://u:p@db:5432/x?a=b" {
		t.Errorf("variables: %v", env)
	}
}

// Un deployer no puede quitar ni cambiar la lista de acceso de una app (la
// dejaría pública); solo el admin, igual que en los sitios (M-A).
func TestAppAccessListAdminOnly(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("PUT", "/api/settings", `{"public_url":"https://dockfly.ejemplo.com"}`)
	admin.do("POST", "/api/access-lists", `{"name":"Equipo","users":[{"username":"o","password":"secreto1"}]}`)
	if code, res := admin.do("POST", "/api/apps", `{"name":"api","source":"image","image":"nginx","port":80,"domains":["api.test"],"access_list_id":1}`); code != 201 {
		t.Fatalf("crear app protegida: %d %v", code, res)
	}

	_, inv := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"deployer"}`)
	dev := newClient(t, ts)
	accept(t, dev, inv["link"].(string), "dev-secreto")

	// El deployer edita la app pero SIN tocar la lista: permitido.
	if code, res := dev.do("PUT", "/api/apps/1", `{"name":"api","source":"image","image":"nginx:1.2","port":80,"domains":["api.test"],"access_list_id":1}`); code != 200 {
		t.Fatalf("deployer edita sin tocar la ACL: %d %v", code, res)
	}
	// El deployer quita la lista (la omite): prohibido.
	if code, res := dev.do("PUT", "/api/apps/1", `{"name":"api","source":"image","image":"nginx:1.2","port":80,"domains":["api.test"]}`); code != 403 || res["code"] != "app_access_admin_only" {
		t.Fatalf("deployer no debe poder quitar la ACL: %d %v", code, res)
	}
	// El admin sí puede quitarla.
	if code, res := admin.do("PUT", "/api/apps/1", `{"name":"api","source":"image","image":"nginx:1.2","port":80,"domains":["api.test"]}`); code != 200 {
		t.Fatalf("el admin sí puede quitar la ACL: %d %v", code, res)
	}
}
