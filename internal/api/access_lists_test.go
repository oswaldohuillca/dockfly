package api

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestAccessLists(t *testing.T) {
	ts, _, dynDir := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	for name, body := range map[string]string{
		"vacía":          `{"name":"x"}`,
		"IP inválida":    `{"name":"x","allow_ips":["999.1.1.1"]}`,
		"sin contraseña": `{"name":"x","users":[{"username":"a"}]}`,
		"usuario con :":  `{"name":"x","users":[{"username":"a:b","password":"secreto"}]}`,
	} {
		if code, _ := c.do("POST", "/api/access-lists", body); code != 400 {
			t.Errorf("%s: esperaba 400, obtuve %d", name, code)
		}
	}
	code, res := c.do("POST", "/api/access-lists", `{"name":"Equipo","users":[{"username":"oswa","password":"secreto1"}],"allow_ips":["10.0.0.5","192.168.1.77/24"]}`)
	if code != 201 {
		t.Fatalf("crear: %d %v", code, res)
	}
	_, lists := c.doList("GET", "/api/access-lists")
	l := lists[0].(map[string]any)
	if strings.Contains(fmt.Sprint(l), "$2a$") {
		t.Fatal("los hashes nunca deben salir del servidor")
	}
	if fmt.Sprint(l["allow_ips"]) != "[10.0.0.5/32 192.168.1.0/24]" {
		t.Errorf("las IPs deben normalizarse a rangos: %v", l["allow_ips"])
	}

	if code, _ := c.do("POST", "/api/proxy-hosts", `{"domains":["a.test"],"target_type":"external","forward_host":"1.1.1.1","forward_port":80,"access_list_id":99}`); code != 400 {
		t.Errorf("una lista inexistente debe rechazarse: %d", code)
	}
	if code, _ := c.do("POST", "/api/proxy-hosts", `{"domains":["a.test"],"target_type":"external","forward_host":"1.1.1.1","forward_port":80,"access_list_id":1}`); code != 201 {
		t.Fatal("crear host protegido")
	}
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	for _, want := range []string{"acl-1-ip", "acl-1-auth", "10.0.0.5/32", "oswa:$2a$", "removeHeader: true"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("la configuración de Traefik debe contener %q:\n%s", want, cfg)
		}
	}

	// Editar sin contraseña conserva la que había.
	before := string(cfg)
	if code, _ := c.do("PUT", "/api/access-lists/1", `{"name":"Equipo","users":[{"username":"oswa"}],"allow_ips":["10.0.0.5"]}`); code != 200 {
		t.Fatal("editar")
	}
	cfg, _ = os.ReadFile(dynDir + "/dockfly.yml")
	hash := func(s string) string { i := strings.Index(s, "oswa:$2a$"); return s[i : i+60] }
	if hash(string(cfg)) != hash(before) {
		t.Error("editar sin contraseña debe conservar el hash")
	}
	if code, _ := c.do("DELETE", "/api/access-lists/1", ""); code != 409 {
		t.Errorf("borrar una lista en uso: esperaba 409, obtuve %d", code)
	}
}
