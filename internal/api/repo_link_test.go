package api

import (
	"fmt"
	"testing"
)

// Las cuentas Git son del admin: un deployer no puede enlazar con ellas un
// repositorio nuevo (ni listar sus repos si es viewer), pero sí cambiar la
// rama o el build de uno ya enlazado.
func TestDeployerCannotLinkRepos(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_bueno"}`, gh.URL))
	admin.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	admin.do("POST", "/api/sites", `{"name":"otro","domains":["otro.test"]}`)
	link := `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site"}`
	if code, body := admin.do("PUT", "/api/sites/1/git", link); code != 200 {
		t.Fatalf("el admin enlaza: %d %v", code, body)
	}

	_, dinv := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"deployer"}`)
	_, vinv := admin.do("POST", "/api/users", `{"name":"Mira","email":"mira@test.com","role":"viewer"}`)
	dev, viewer := newClient(t, ts), newClient(t, ts)
	accept(t, dev, dinv["link"].(string), "dev-secreto")
	accept(t, viewer, vinv["link"].(string), "mira-secreto")

	if code, _ := viewer.do("GET", "/api/git/connections/1/repos", ""); code != 403 {
		t.Errorf("un viewer no lista repos: %d", code)
	}
	if code, _ := dev.doList("GET", "/api/git/connections/1/repos"); code != 200 {
		t.Errorf("un deployer sí lista repos: %d", code)
	}
	if code, body := dev.do("PUT", "/api/sites/2/git", link); code != 403 || body["code"] != "repo_link_admin_only" {
		t.Errorf("un deployer no enlaza un repo nuevo: %d %v", code, body)
	}
	rebuild := `{"connection_id":1,"repo":"oswa/web","branch":"main","output_dir":"site","build_command":"echo hola"}`
	if code, body := dev.do("PUT", "/api/sites/1/git", rebuild); code != 200 {
		t.Errorf("un deployer cambia el build del repo ya enlazado: %d %v", code, body)
	}
	app := `{"name":"api","source":"git","connection_id":1,"repo":"oswa/web","branch":"main","port":8080}`
	if code, body := dev.do("POST", "/api/apps", app); code != 403 || body["code"] != "repo_link_admin_only" {
		t.Errorf("un deployer no crea apps desde un repo: %d %v", code, body)
	}
}
