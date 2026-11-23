package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dockfly/dockfly/internal/store"
)

func TestRequiredRole(t *testing.T) {
	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/api/proxy-hosts", store.RoleViewer},
		{"GET", "/api/sites/1", store.RoleViewer},
		{"GET", "/api/builds/3/logs", store.RoleViewer},
		{"GET", "/api/settings", store.RoleViewer},
		{"PUT", "/api/auth/password", store.RoleViewer},
		{"POST", "/api/auth/2fa/setup", store.RoleViewer},
		{"GET", "/api/sites/1/git/env", store.RoleDeployer},
		{"GET", "/api/sites/1/deploy-hook", store.RoleDeployer},
		{"GET", "/api/apps/2/env", store.RoleDeployer},
		{"GET", "/api/apps/2/backups", store.RoleViewer},
		{"GET", "/api/apps/2/backups/7/download", store.RoleDeployer},
		{"POST", "/api/apps/2/backups/7/restore", store.RoleDeployer},
		{"PUT", "/api/backup-storage", store.RoleAdmin},
		{"POST", "/api/sites", store.RoleDeployer},
		{"POST", "/api/sites/1/builds", store.RoleDeployer},
		{"POST", "/api/apps/2/restart", store.RoleDeployer},
		{"POST", "/api/app-deployments/5/cancel", store.RoleDeployer},
		{"DELETE", "/api/sites/1/previews/x", store.RoleDeployer},
		{"POST", "/api/proxy-hosts", store.RoleAdmin},
		{"PUT", "/api/settings", store.RoleAdmin},
		{"POST", "/api/git/connections", store.RoleAdmin},
		{"GET", "/api/git/connections", store.RoleViewer},
		{"GET", "/api/git/connections/1/repos", store.RoleAdmin},
		{"GET", "/api/git/connections/1/branches", store.RoleAdmin},
		{"GET", "/api/backup", store.RoleAdmin},
		{"GET", "/api/users", store.RoleAdmin},
		{"GET", "/api/users/", store.RoleAdmin},
		{"POST", "/api/notifications/1/test", store.RoleAdmin},
		{"PUT", "/api/dns-providers", store.RoleAdmin},
		{"GET", "/api/system/update", store.RoleViewer},
		{"POST", "/api/system/update", store.RoleAdmin},
		// A lookalike prefix doesn't pass as a deployment route.
		{"POST", "/api/sitesx", store.RoleAdmin},
	} {
		if got := requiredRole(tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s: %s, want %s", tc.method, tc.path, got, tc.want)
		}
	}
}

// accept accepts an invitation link with a fresh client.
func accept(t *testing.T, c *client, link, password string) (int, map[string]any) {
	t.Helper()
	i := strings.Index(link, invitePath)
	if i < 0 {
		t.Fatalf("link without %s: %s", invitePath, link)
	}
	token := link[i+len(invitePath):]
	return c.do("POST", "/api/invite/"+token, fmt.Sprintf(`{"password":%q}`, password))
}

func TestUsersAndRoles(t *testing.T) {
	ts, _ := testServer(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)

	if code, _ := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"boss"}`); code != 400 {
		t.Errorf("unknown role: %d", code)
	}
	code, inv := admin.do("POST", "/api/users", `{"name":"Dev","email":"Dev@Test.com","role":"deployer"}`)
	if code != 201 || !strings.HasPrefix(inv["link"].(string), "https://dockfly.example.com/invite/") ||
		inv["user"].(map[string]any)["pending"] != true {
		t.Fatalf("inviting: %d %v", code, inv)
	}
	if code, _ := admin.do("POST", "/api/users", `{"name":"Otro","email":"dev@test.com","role":"viewer"}`); code != 409 {
		t.Errorf("duplicate email: %d", code)
	}
	_, vinv := admin.do("POST", "/api/users", `{"name":"Mira","email":"mira@test.com","role":"viewer"}`)

	// An invited user can't sign in until they accept the invitation.
	dev := newClient(t, ts)
	if code, _ := dev.do("POST", "/api/auth/login", `{"email":"dev@test.com","password":""}`); code != 401 {
		t.Errorf("login without a password: %d", code)
	}
	link := inv["link"].(string)
	token := link[strings.Index(link, invitePath)+len(invitePath):]
	if code, body := dev.do("GET", "/api/invite/"+token, ""); code != 200 || body["email"] != "dev@test.com" || body["new"] != true {
		t.Errorf("viewing the invitation: %d %v", code, body)
	}
	if code, _ := accept(t, dev, link, "short"); code != 400 {
		t.Errorf("short password: %d", code)
	}
	if code, body := accept(t, dev, link, "dev-secret"); code != 200 || body["role"] != "deployer" {
		t.Fatalf("accepting: %d %v", code, body)
	}
	if code, _ := accept(t, newClient(t, ts), link, "other-password"); code != 404 {
		t.Errorf("the invitation only works once: %d", code)
	}
	viewer := newClient(t, ts)
	accept(t, viewer, vinv["link"].(string), "mira-secret")

	// Permissions.
	check := func(name string, c *client, method, path, body string, want int) {
		t.Helper()
		if code, resp := c.do(method, path, body); code != want {
			t.Errorf("%s %s %s: %d, want %d (%v)", name, method, path, code, want, resp)
		}
	}
	check("deployer", dev, "POST", "/api/sites", `{"name":"web","domains":["web.test"]}`, 201)
	check("deployer", dev, "PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"spa":true}`, 200)
	check("deployer", dev, "GET", "/api/proxy-hosts", "", 200)
	check("deployer", dev, "POST", "/api/proxy-hosts", `{"domains":["x.test"],"forward_host":"a","forward_port":80}`, 403)
	check("deployer", dev, "GET", "/api/users", "", 403)
	check("deployer", dev, "GET", "/api/backup", "", 403)
	check("viewer", viewer, "GET", "/api/sites/1", "", 200)
	check("viewer", viewer, "PUT", "/api/sites/1", `{"name":"web","domains":["web.test"]}`, 403)
	check("viewer", viewer, "GET", "/api/sites/1/git/env", "", 403)
	check("viewer", viewer, "GET", "/api/auth/2fa", "", 200)

	// There is always one administrator left.
	check("admin", admin, "PUT", "/api/users/1", `{"name":"Admin","role":"viewer"}`, 409)
	check("admin", admin, "DELETE", "/api/users/1", "", 409)
	check("admin", admin, "PUT", "/api/users/2", `{"name":"Dev","role":"admin"}`, 200)
	check("admin", admin, "PUT", "/api/users/1", `{"name":"Admin","role":"viewer"}`, 200)
	// The role change takes effect at once, without signing in again.
	check("ex-admin", admin, "GET", "/api/users", "", 403)
	check("new admin", dev, "GET", "/api/users", "", 200)
	check("new admin", dev, "DELETE", "/api/users/2", "", 409)

	// Changing the password closes the other sessions (but not the current one).
	other := newClient(t, ts)
	other.do("POST", "/api/auth/login", `{"email":"dev@test.com","password":"dev-secret"}`)
	check("other session", other, "GET", "/api/auth/me", "", 200)
	check("dev", dev, "PUT", "/api/auth/password", `{"current":"wrong","new":"new-password"}`, 403)
	check("dev", dev, "PUT", "/api/auth/password", `{"current":"dev-secret","new":"new-password"}`, 204)
	check("dev", dev, "GET", "/api/auth/me", "", 200)
	check("other session", other, "GET", "/api/auth/me", "", 401)

	// Password reset link: single use.
	code, rl := dev.do("POST", "/api/users/3/link", "")
	if code != 200 {
		t.Fatalf("reset link: %d %v", code, rl)
	}
	reset := newClient(t, ts)
	if code, _ := accept(t, reset, rl["link"].(string), "mira-nueva"); code != 200 {
		t.Errorf("resetting: %d", code)
	}
	check("viewer with the old password", viewer, "GET", "/api/auth/me", "", 401)
	if code, _ := accept(t, newClient(t, ts), rl["link"].(string), "otra-mas"); code != 404 {
		t.Errorf("the reset link only works once: %d", code)
	}

	// Deleting a user closes their session.
	check("admin", dev, "DELETE", "/api/users/3", "", 204)
	check("deleted", reset, "GET", "/api/auth/me", "", 401)
	if code, list := dev.doList("GET", "/api/users"); code != http.StatusOK || len(list) != 2 {
		t.Errorf("users: %d %v", code, list)
	}
}
