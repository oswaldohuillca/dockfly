package api

import (
	"fmt"
	"testing"
)

// Git accounts belong to the admin: only the admin lists their repos and
// links a new one; a deployer can't, but a deployer can change the branch or
// the build of one that is already linked.
func TestDeployerCannotLinkRepos(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_good"}`, gh.URL))
	admin.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	admin.do("POST", "/api/sites", `{"name":"other","domains":["other.test"]}`)
	link := `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site"}`
	if code, body := admin.do("PUT", "/api/sites/1/git", link); code != 200 {
		t.Fatalf("the admin links: %d %v", code, body)
	}

	_, dinv := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"deployer"}`)
	_, vinv := admin.do("POST", "/api/users", `{"name":"Mira","email":"mira@test.com","role":"viewer"}`)
	dev, viewer := newClient(t, ts), newClient(t, ts)
	accept(t, dev, dinv["link"].(string), "dev-secret")
	accept(t, viewer, vinv["link"].(string), "mira-secret")

	if code, _ := viewer.do("GET", "/api/git/connections/1/repos", ""); code != 403 {
		t.Errorf("a viewer must not list repos: %d", code)
	}
	if code, _ := dev.doList("GET", "/api/git/connections/1/repos"); code != 403 {
		t.Errorf("a deployer must not list repos either (admin only): %d", code)
	}
	if code, body := dev.do("PUT", "/api/sites/2/git", link); code != 403 || body["code"] != "repo_link_admin_only" {
		t.Errorf("a deployer must not link a new repo: %d %v", code, body)
	}
	rebuild := `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site","build_command":"echo hello"}`
	if code, body := dev.do("PUT", "/api/sites/1/git", rebuild); code != 200 {
		t.Errorf("a deployer changes the build of an already linked repo: %d %v", code, body)
	}
	app := `{"name":"api","source":"git","connection_id":1,"repo":"oswa/web","branch":"main","port":8080}`
	if code, body := dev.do("POST", "/api/apps", app); code != 403 || body["code"] != "repo_link_admin_only" {
		t.Errorf("a deployer must not create apps from a repo: %d %v", code, body)
	}
}

// A site's locations publish any target, just like a proxy host: only the
// admin changes them (and the access list). A deployer does edit the rest.
func TestDeployerCannotChangeSiteRouting(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/access-lists", `{"name":"office","allow_ips":["10.0.0.0/8"]}`)
	if code, body := admin.do("POST", "/api/sites", `{"name":"web","domains":["web.test"],"access_list_id":1}`); code != 201 {
		t.Fatalf("creating a site: %d %v", code, body)
	}
	_, inv := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"deployer"}`)
	dev := newClient(t, ts)
	accept(t, dev, inv["link"].(string), "dev-secret")

	loc := `{"name":"web","domains":["web.test"],"access_list_id":1,"locations":[{"path":"/x","target_type":"external","forward_scheme":"http","forward_host":"169.254.169.254","forward_port":80,"access":"public"}]}`
	if code, body := dev.do("PUT", "/api/sites/1", loc); code != 403 || body["code"] != "site_routing_admin_only" {
		t.Errorf("a deployer must not add locations: %d %v", code, body)
	}
	if code, _ := dev.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"]}`); code != 403 {
		t.Errorf("a deployer must not remove the access list: %d", code)
	}
	if code, body := dev.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"access_list_id":1,"spa":true}`); code != 200 {
		t.Errorf("a deployer does edit everything else: %d %v", code, body)
	}
	if code, _ := dev.do("POST", "/api/sites", `{"name":"other","domains":["other.test"],"locations":[{"path":"/x","target_type":"external","forward_scheme":"http","forward_host":"10.0.0.1","forward_port":80,"access":"public"}]}`); code != 403 {
		t.Errorf("a deployer must not create sites with locations: %d", code)
	}
	if code, _ := admin.do("PUT", "/api/sites/1", loc); code != 200 {
		t.Errorf("the admin can: %d", code)
	}
}

// A Git account an app is using can't be deleted: the app would be left
// without a repository (ON DELETE SET NULL) and with an orphan webhook.
func TestGitConnectionInUseByApp(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_good"}`, gh.URL))
	if code, body := admin.do("POST", "/api/apps", `{"name":"api","source":"git","connection_id":1,"repo":"oswa/web","branch":"main","port":8080}`); code != 201 {
		t.Fatalf("creating an app: %d %v", code, body)
	}
	code, body := admin.do("DELETE", "/api/git/connections/1", "")
	if code != 409 || body["code"] != "git_connection_in_use" || body["params"].(map[string]any)["sites"] != "api" {
		t.Errorf("deleting an account in use by an app: %d %v", code, body)
	}
}
