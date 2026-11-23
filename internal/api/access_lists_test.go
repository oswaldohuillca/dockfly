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
		"empty":             `{"name":"x"}`,
		"invalid IP":        `{"name":"x","allow_ips":["999.1.1.1"]}`,
		"no password":       `{"name":"x","users":[{"username":"a"}]}`,
		"username with \":": `{"name":"x","users":[{"username":"a:b","password":"secret"}]}`,
	} {
		if code, _ := c.do("POST", "/api/access-lists", body); code != 400 {
			t.Errorf("%s: expected 400, got %d", name, code)
		}
	}
	code, res := c.do("POST", "/api/access-lists", `{"name":"Team","users":[{"username":"oswa","password":"secret1"}],"allow_ips":["10.0.0.5","192.168.1.77/24"]}`)
	if code != 201 {
		t.Fatalf("create: %d %v", code, res)
	}
	_, lists := c.doList("GET", "/api/access-lists")
	l := lists[0].(map[string]any)
	if strings.Contains(fmt.Sprint(l), "$2a$") {
		t.Fatal("the hashes must never leave the server")
	}
	if fmt.Sprint(l["allow_ips"]) != "[10.0.0.5/32 192.168.1.0/24]" {
		t.Errorf("the IPs must be normalized to ranges: %v", l["allow_ips"])
	}

	if code, _ := c.do("POST", "/api/proxy-hosts", `{"domains":["a.test"],"target_type":"external","forward_host":"1.1.1.1","forward_port":80,"access_list_id":99}`); code != 400 {
		t.Errorf("a list that doesn't exist must be rejected: %d", code)
	}
	if code, _ := c.do("POST", "/api/proxy-hosts", `{"domains":["a.test"],"target_type":"external","forward_host":"1.1.1.1","forward_port":80,"access_list_id":1}`); code != 201 {
		t.Fatal("create a protected host")
	}
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	for _, want := range []string{"acl-1-ip", "acl-1-auth", "10.0.0.5/32", "oswa:$2a$", "removeHeader: true"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("the Traefik configuration must contain %q:\n%s", want, cfg)
		}
	}

	// Editing without a password keeps the previous one.
	before := string(cfg)
	if code, _ := c.do("PUT", "/api/access-lists/1", `{"name":"Team","users":[{"username":"oswa"}],"allow_ips":["10.0.0.5"]}`); code != 200 {
		t.Fatal("edit")
	}
	cfg, _ = os.ReadFile(dynDir + "/dockfly.yml")
	hash := func(s string) string { i := strings.Index(s, "oswa:$2a$"); return s[i : i+60] }
	if hash(string(cfg)) != hash(before) {
		t.Error("editing without a password must keep the hash")
	}
	if code, _ := c.do("DELETE", "/api/access-lists/1", ""); code != 409 {
		t.Errorf("deleting a list that is in use: expected 409, got %d", code)
	}
}
