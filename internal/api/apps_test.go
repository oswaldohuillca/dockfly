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
		"invalid name":        `{"name":"Mi App","source":"image","image":"nginx"}`,
		"invalid image":       `{"name":"a","source":"image","image":"not valid!"}`,
		"domain with no port": `{"name":"a","source":"image","image":"nginx","domains":["a.test"]}`,
		"relative volume":     `{"name":"a","source":"image","image":"nginx","volumes":[{"name":"data","path":"data"}]}`,
		"unclosed quote":      `{"name":"a","source":"image","image":"nginx","command":"sh -c 'x"}`,
		"git with no repo":    `{"name":"a","source":"git"}`,
	}
	for name, body := range bad {
		if code, res := c.do("POST", "/api/apps", body); code != 400 {
			t.Errorf("%s: expected 400, got %d %v", name, code, res)
		}
	}
	if code, res := c.do("POST", "/api/apps", `{"name":"a","source":"image","image":"nginx","port":80,"domains":["web.test"]}`); code != 409 ||
		res["code"] != "domain_in_use" || res["params"].(map[string]any)["kind"] != "site" || res["params"].(map[string]any)["domain"] != "web.test" {
		t.Errorf("an app can't take a domain that belongs to a site: %d %v", code, res)
	}

	code, res := c.do("POST", "/api/apps", `{"name":"api","source":"image","image":"ghcr.io/oswa/api:1.2","port":3000,
		"domains":["API.test"],"ssl":true,"env":{"DATABASE_URL":"postgres://postgres:5432/db"},
		"volumes":[{"name":"data","path":"/data"}],"health_path":"health","deploy":true}`)
	if code != 201 {
		t.Fatalf("create app: %d %v", code, res)
	}
	app := res["app"].(map[string]any)
	if app["health_path"] != "/health" || app["domains"].([]any)[0] != "api.test" {
		t.Errorf("the app must be normalized: %v", app)
	}
	// Without Docker, deploying isn't possible: it warns without losing the
	// app that was just created.
	if !strings.Contains(res["deploy_error"].(string), "Docker") {
		t.Errorf("warning when Docker is missing: %v", res["deploy_error"])
	}
	if _, env := c.do("GET", "/api/apps/1/env", ""); env["DATABASE_URL"] != "postgres://postgres:5432/db" {
		t.Errorf("environment variables: %v", env)
	}

	// The name never changes (volumes and network depend on it) and env: null keeps the variables.
	code, res = c.do("PUT", "/api/apps/1", `{"name":"other","source":"image","image":"ghcr.io/oswa/api:1.3","port":3000,"domains":["api.test"],"env":null}`)
	if code != 200 || res["app"].(map[string]any)["name"] != "api" || res["app"].(map[string]any)["image"] != "ghcr.io/oswa/api:1.3" {
		t.Fatalf("edit: %d %v", code, res)
	}
	if _, env := c.do("GET", "/api/apps/1/env", ""); env["DATABASE_URL"] == nil {
		t.Error("env: null must keep the variables")
	}

	// The console: with no command nothing runs, and with nothing deployed there's no container.
	if code, res := c.do("POST", "/api/apps/1/exec", `{"command":"  "}`); code != 400 || res["code"] != "app_exec_empty" {
		t.Errorf("empty command: %d %v", code, res)
	}
	if code, res := c.do("POST", "/api/apps/1/exec", `{"command":"ls"}`); code != 409 || res["code"] != "app_not_deployed" {
		t.Errorf("exec with nothing deployed: %d %v", code, res)
	}

	// Scheduled tasks: the command and the schedule are validated, and they belong to their app.
	for name, body := range map[string]string{
		"no command":       `{"command":" ","schedule":"* * * * *","enabled":true}`,
		"invalid schedule": `{"command":"ls","schedule":"every hour","enabled":true}`,
	} {
		if code, res := c.do("POST", "/api/apps/1/jobs", body); code != 400 {
			t.Errorf("task %s: expected 400, got %d %v", name, code, res)
		}
	}
	code, res = c.do("POST", "/api/apps/1/jobs", `{"command":" php artisan schedule:run ","schedule":"*/5  * * * *","enabled":true}`)
	if code != 201 || res["command"] != "php artisan schedule:run" || res["schedule"] != "*/5 * * * *" {
		t.Fatalf("create task: %d %v", code, res)
	}
	if code, res := c.do("PUT", "/api/apps/1/jobs/1", `{"command":"ls","schedule":"0 3 * * *","enabled":false}`); code != 200 || res["enabled"] != false {
		t.Errorf("edit task: %d %v", code, res)
	}
	if code, res := c.do("POST", "/api/apps/1/jobs/1/run", ""); code != 409 || res["code"] != "job_app_not_running" {
		t.Errorf("run a task with nothing deployed: %d %v", code, res)
	}
	if code, _ := c.do("DELETE", "/api/apps/2/jobs/1", ""); code != 404 {
		t.Errorf("a task can't be deleted from another app: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/apps/1/jobs/1", ""); code != 204 {
		t.Errorf("delete task: %d", code)
	}

	// With nothing deployed there's no route in Traefik (no container to send traffic to).
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	if strings.Contains(string(cfg), "app-1") {
		t.Errorf("an app that isn't deployed must have no route:\n%s", cfg)
	}
	if code, _ := c.do("POST", "/api/apps/1/stop", ""); code != 409 {
		t.Errorf("stopping an app that isn't deployed: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/apps/1", ""); code != 204 {
		t.Errorf("delete: %d", code)
	}
}

// Two apps can't end up on the same Docker volume
// (dockfly-app-<app>-<volume>): "shop"+"db-data" = "shop-db"+"data".
func TestAppVolumesNotShared(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	if code, res := c.do("POST", "/api/apps", `{"name":"shop","source":"image","image":"nginx","volumes":[{"name":"db-data","path":"/d"}]}`); code != 201 {
		t.Fatalf("create shop: %d %v", code, res)
	}
	code, res := c.do("POST", "/api/apps", `{"name":"shop-db","source":"image","image":"postgres","volumes":[{"name":"data","path":"/var/lib/postgresql/data"}]}`)
	if code != 409 || res["code"] != "app_volume_shared" {
		t.Errorf("shared volume: %d %v", code, res)
	}
	if code, _ := c.do("POST", "/api/apps", `{"name":"shop-db","source":"image","image":"postgres","volumes":[{"name":"pg","path":"/var/lib/postgresql/data"}]}`); code != 201 {
		t.Errorf("with another name it's fine: %d", code)
	}
}

// Variable values are stored verbatim, including those with ":" or "=".
func TestAppEnvValuesKeptVerbatim(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	if code, res := c.do("POST", "/api/apps", `{"name":"api","source":"image","image":"nginx"}`); code != 201 {
		t.Fatalf("create: %d %v", code, res)
	}
	put := `{"name":"api","source":"image","image":"nginx","env":{"KEY":"foo:bar","URL":"postgres://u:p@db:5432/x?a=b"}}`
	if code, res := c.do("PUT", "/api/apps/1", put); code != 200 {
		t.Fatalf("save: %d %v", code, res)
	}
	_, env := c.do("GET", "/api/apps/1/env", "")
	if env["KEY"] != "foo:bar" || env["URL"] != "postgres://u:p@db:5432/x?a=b" {
		t.Errorf("variables: %v", env)
	}
}

// A deployer can neither drop nor change an app's access list (that would
// leave it public); only the admin can, as with sites (M-A).
func TestAppAccessListAdminOnly(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)
	admin.do("POST", "/api/access-lists", `{"name":"Team","users":[{"username":"o","password":"secret1"}]}`)
	if code, res := admin.do("POST", "/api/apps", `{"name":"api","source":"image","image":"nginx","port":80,"domains":["api.test"],"access_list_id":1}`); code != 201 {
		t.Fatalf("create a protected app: %d %v", code, res)
	}

	_, inv := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"deployer"}`)
	dev := newClient(t, ts)
	accept(t, dev, inv["link"].(string), "dev-secret")

	// The deployer edits the app but WITHOUT touching the list: allowed.
	if code, res := dev.do("PUT", "/api/apps/1", `{"name":"api","source":"image","image":"nginx:1.2","port":80,"domains":["api.test"],"access_list_id":1}`); code != 200 {
		t.Fatalf("deployer edits without touching the ACL: %d %v", code, res)
	}
	// The deployer drops the list (by omitting it): forbidden.
	if code, res := dev.do("PUT", "/api/apps/1", `{"name":"api","source":"image","image":"nginx:1.2","port":80,"domains":["api.test"]}`); code != 403 || res["code"] != "app_access_admin_only" {
		t.Fatalf("a deployer must not be able to drop the ACL: %d %v", code, res)
	}
	// The admin can drop it.
	if code, res := admin.do("PUT", "/api/apps/1", `{"name":"api","source":"image","image":"nginx:1.2","port":80,"domains":["api.test"]}`); code != 200 {
		t.Fatalf("the admin can drop the ACL: %d %v", code, res)
	}
}
