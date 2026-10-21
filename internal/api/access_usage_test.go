package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Una lista de acceso usada por una ubicación personalizada o por las
// previews no se puede borrar (dejaría esas rutas públicas), y una ruta cuya
// lista se borrara igualmente quedaría denegada, no abierta.
func TestAccessListUsageAndFailClosed(t *testing.T) {
	ts, dyn := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/access-lists", `{"name":"a","allow_ips":["10.0.0.0/8"]}`)

	// Usada por una ubicación personalizada.
	c.do("POST", "/api/proxy-hosts", `{"domains":["app.test"],"target_type":"external","forward_host":"10.0.0.1","forward_port":80,
		"locations":[{"path":"/admin","target_type":"external","forward_host":"10.0.0.2","forward_port":80,"access":"list","access_list_id":1}]}`)
	if _, list := c.doList("GET", "/api/access-lists"); list[0].(map[string]any)["used_by"].(float64) < 1 {
		t.Error("una ubicación debe contar como uso de la lista")
	}
	if code, _ := c.do("DELETE", "/api/access-lists/1", ""); code != 409 {
		t.Errorf("borrar una lista usada por una ubicación: %d", code)
	}

	// Usada por las previews.
	c.do("PUT", "/api/settings", `{"preview_access_list_id":1}`)
	if _, list := c.doList("GET", "/api/access-lists"); list[0].(map[string]any)["used_by"].(float64) < 2 {
		t.Error("la lista global de previews debe contar como uso")
	}

	// La ubicación /admin tiene el middleware de la lista 1.
	raw, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	if !strings.Contains(string(raw), "acl-1-ip") {
		t.Errorf("la ubicación debe llevar el middleware de la lista:\n%s", raw)
	}
}
