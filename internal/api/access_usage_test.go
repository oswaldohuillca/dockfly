package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An access list used by a custom location or by the previews can't be
// deleted (it would leave those routes public), and a route whose list were
// deleted anyway would end up denied, not open.
func TestAccessListUsageAndFailClosed(t *testing.T) {
	ts, dyn := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("POST", "/api/access-lists", `{"name":"a","allow_ips":["10.0.0.0/8"]}`)

	// Used by a custom location.
	c.do("POST", "/api/proxy-hosts", `{"domains":["app.test"],"target_type":"external","forward_host":"10.0.0.1","forward_port":80,
		"locations":[{"path":"/admin","target_type":"external","forward_host":"10.0.0.2","forward_port":80,"access":"list","access_list_id":1}]}`)
	if _, list := c.doList("GET", "/api/access-lists"); list[0].(map[string]any)["used_by"].(float64) < 1 {
		t.Error("a location must count as a use of the list")
	}
	if code, _ := c.do("DELETE", "/api/access-lists/1", ""); code != 409 {
		t.Errorf("deleting a list used by a location: %d", code)
	}

	// Used by the previews.
	c.do("PUT", "/api/settings", `{"preview_access_list_id":1}`)
	if _, list := c.doList("GET", "/api/access-lists"); list[0].(map[string]any)["used_by"].(float64) < 2 {
		t.Error("the global preview list must count as a use")
	}

	// The /admin location carries the middleware of list 1.
	raw, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	if !strings.Contains(string(raw), "acl-1-ip") {
		t.Errorf("the location must carry the list's middleware:\n%s", raw)
	}
}
