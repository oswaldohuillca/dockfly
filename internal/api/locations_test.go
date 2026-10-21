package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProxyHostLocations(t *testing.T) {
	ts, dyn := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/access-lists", `{"name":"equipo","users":[{"username":"ana","password":"secreto1"}]}`)

	host := func(locations string) string {
		return `{"domains":["app.test"],"target_type":"external","forward_scheme":"http","forward_host":"10.0.0.1","forward_port":3000,` +
			`"access_list_id":1,"locations":` + locations + `}`
	}
	for name, tc := range map[string]struct{ locations, code string }{
		"raíz":       {`[{"path":"/","target_type":"external","forward_host":"api","forward_port":3001}]`, "location_path_root"},
		"sin barra":  {`[{"path":"api","target_type":"external","forward_host":"api","forward_port":3001}]`, "location_path_invalid"},
		"repetida":   {`[{"path":"/api","target_type":"external","forward_host":"a","forward_port":1},{"path":"/api/","target_type":"external","forward_host":"b","forward_port":2}]`, "location_path_duplicate"},
		"sin puerto": {`[{"path":"/api","target_type":"external","forward_host":"api"}]`, "location_invalid"},
		"lista nula": {`[{"path":"/api","target_type":"external","forward_host":"api","forward_port":1,"access":"list"}]`, "location_access_invalid"},
	} {
		if code, body := c.do("POST", "/api/proxy-hosts", host(tc.locations)); code != 400 || body["code"] != tc.code {
			t.Errorf("%s: %d %v", name, code, body)
		}
	}

	code, body := c.do("POST", "/api/proxy-hosts", host(`[
		{"path":"/api/","target_type":"docker","forward_scheme":"http","forward_host":"api","forward_port":3001},
		{"path":"/api/v2","target_type":"external","forward_scheme":"https","forward_host":"v2.internal","forward_port":443,"strip_prefix":true,"access":"public"},
		{"path":"/admin","target_type":"host","forward_host":"host.docker.internal","forward_port":9000,"access":"list","access_list_id":1}]`))
	if code != 201 {
		t.Fatalf("crear: %d %v", code, body)
	}
	locs := body["proxy_host"].(map[string]any)["locations"].([]any)
	if len(locs) != 3 || locs[0].(map[string]any)["path"] != "/api/v2" || locs[2].(map[string]any)["path"] != "/api" ||
		locs[2].(map[string]any)["access"] != "inherit" {
		t.Fatalf("ubicaciones normalizadas (de la más larga a la más corta, sin / final): %v", locs)
	}

	raw, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	var cfg struct {
		HTTP struct {
			Routers     map[string]map[string]any `yaml:"routers"`
			Services    map[string]map[string]any `yaml:"services"`
			Middlewares map[string]map[string]any `yaml:"middlewares"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	r := cfg.HTTP.Routers
	if r["ph-1-loc0"]["rule"] != "(Host(`app.test`)) && PathPrefix(`/api/v2`)" || r["ph-1-loc2"]["rule"] != "(Host(`app.test`)) && PathPrefix(`/api`)" {
		t.Errorf("reglas: %v / %v", r["ph-1-loc0"]["rule"], r["ph-1-loc2"]["rule"])
	}
	mws := func(name string) string {
		var out []string
		for _, m := range r[name]["middlewares"].([]any) {
			out = append(out, m.(string))
		}
		return strings.Join(out, ",")
	}
	if _, has := r["ph-1-loc0"]["middlewares"]; !has || mws("ph-1-loc0") != "ph-1-loc0-strip" {
		t.Errorf("/api/v2 es pública y quita el prefijo: %v", r["ph-1-loc0"]["middlewares"])
	}
	if !strings.HasPrefix(mws("ph-1-loc2"), "acl-1") || !strings.HasPrefix(mws("ph-1"), "acl-1") || !strings.HasPrefix(mws("ph-1-loc1"), "acl-1") {
		t.Errorf("/api hereda la lista del host y /admin usa la suya: host=%s api=%s admin=%s", mws("ph-1"), mws("ph-1-loc2"), mws("ph-1-loc1"))
	}
	if strip := cfg.HTTP.Middlewares["ph-1-loc0-strip"]; strip == nil {
		t.Error("falta el middleware stripPrefix")
	}
	url := func(name string) any {
		return cfg.HTTP.Services[name]["loadBalancer"].(map[string]any)["servers"].([]any)[0].(map[string]any)["url"]
	}
	if url("ph-1") != "http://10.0.0.1:3000" || url("ph-1-loc2") != "http://api:3001" || url("ph-1-loc0") != "https://v2.internal:443" {
		t.Errorf("destinos: %v %v %v", url("ph-1"), url("ph-1-loc2"), url("ph-1-loc0"))
	}
}
