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

// Un sitio con ubicaciones: el sitio sirve su build y /api va a otro destino
// en el mismo dominio (un frontend y su API detrás del mismo host).
func TestSiteLocations(t *testing.T) {
	ts, dyn := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/access-lists", `{"name":"equipo","users":[{"username":"ana","password":"secreto1"}]}`)
	// El comodín con SSL pide su zona DNS (Let's Encrypt solo lo emite por DNS).
	if code, body := c.do("PUT", "/api/dns-providers", `{"provider":"cloudflare","zones":["app.test"],"env":{"CLOUDFLARE_DNS_API_TOKEN":"cf"}}`); code != 200 {
		t.Fatalf("zona DNS: %d %v", code, body)
	}

	site := func(locations string) string {
		return `{"name":"web","domains":["app.test","*.app.test"],"ssl":true,"spa":true,"access_list_id":1,"locations":` + locations + `}`
	}
	for name, tc := range map[string]struct{ locations, code string }{
		"raíz":        {`[{"path":"/","target_type":"docker","forward_host":"api","forward_port":3001}]`, "location_path_root"},
		"sin destino": {`[{"path":"/api","target_type":"docker","forward_port":3001}]`, "location_invalid"},
		"lista ajena": {`[{"path":"/api","target_type":"docker","forward_host":"api","forward_port":3001,"access":"list","access_list_id":99}]`, "access_list_missing"},
	} {
		if code, body := c.do("POST", "/api/sites", site(tc.locations)); code != 400 || body["code"] != tc.code {
			t.Errorf("%s: %d %v", name, code, body)
		}
	}

	code, body := c.do("POST", "/api/sites", site(`[
		{"path":"/signup","target_type":"docker","forward_host":"api","forward_port":3001,"access":"public"},
		{"path":"/api/","target_type":"docker","forward_host":"api","forward_port":3001}]`))
	if code != 201 {
		t.Fatalf("crear: %d %v", code, body)
	}
	locs := body["locations"].([]any)
	if len(locs) != 2 || locs[0].(map[string]any)["path"] != "/signup" || locs[1].(map[string]any)["path"] != "/api" {
		t.Fatalf("ubicaciones normalizadas: %v", locs)
	}
	if code, body := c.do("GET", "/api/sites/1", ""); code != 200 || len(body["site"].(map[string]any)["locations"].([]any)) != 2 {
		t.Fatalf("leer: %d %v", code, body)
	}

	raw, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	var cfg struct {
		HTTP struct {
			Routers  map[string]map[string]any `yaml:"routers"`
			Services map[string]map[string]any `yaml:"services"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	r := cfg.HTTP.Routers
	host := "Host(`app.test`) || HostRegexp(`^[a-z0-9-]+\\.app\\.test$`)"
	if r["site-1-loc1-secure"]["rule"] != "("+host+") && PathPrefix(`/api`)" {
		t.Errorf("regla de /api: %v", r["site-1-loc1-secure"]["rule"])
	}
	// Con comodines el sitio va con prioridad 1: la ubicación tiene que ganarle.
	if p, site := r["site-1-loc1-secure"]["priority"], r["site-1-secure"]["priority"]; p.(int) <= site.(int) {
		t.Errorf("prioridades: ubicación %v, sitio %v", p, site)
	}
	mws := func(name string) string {
		var out []string
		for _, m := range r[name]["middlewares"].([]any) {
			out = append(out, m.(string))
		}
		return strings.Join(out, ",")
	}
	if !strings.Contains(mws("site-1-secure"), "dockfly-compress") {
		t.Errorf("el sitio se comprime: %s", mws("site-1-secure"))
	}
	if m := mws("site-1-loc1-secure"); !strings.HasPrefix(m, "acl-1") || strings.Contains(m, "dockfly-compress") {
		t.Errorf("/api hereda la lista del sitio y no se comprime: %s", m)
	}
	if m, has := r["site-1-loc0-secure"]["middlewares"]; has && strings.Contains(mws("site-1-loc0-secure"), "acl-") {
		t.Errorf("/signup es pública: %v", m)
	}
	lb := func(name string) map[string]any { return cfg.HTTP.Services[name]["loadBalancer"].(map[string]any) }
	if url := lb("site-1-loc1")["servers"].([]any)[0].(map[string]any)["url"]; url != "http://api:3001" {
		t.Errorf("destino de /api: %v", url)
	}
	// El backend necesita el Host original (p. ej. para saber qué subdominio es).
	if lb("site-1-loc1")["passHostHeader"] != true {
		t.Error("la ubicación conserva el Host")
	}
}
