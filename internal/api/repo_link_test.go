package api

import (
	"fmt"
	"testing"
)

// Las cuentas Git son del admin: solo el admin lista sus repos y enlaza uno
// nuevo; un deployer no, pero sí cambia la rama o el build de uno ya enlazado.
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
	if code, _ := dev.doList("GET", "/api/git/connections/1/repos"); code != 403 {
		t.Errorf("un deployer tampoco lista repos (solo admin): %d", code)
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

// Las ubicaciones de un sitio publican cualquier destino, como un proxy host:
// solo el admin las cambia (y la lista de acceso). El deployer sí edita el resto.
func TestDeployerCannotChangeSiteRouting(t *testing.T) {
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/access-lists", `{"name":"oficina","allow_ips":["10.0.0.0/8"]}`)
	if code, body := admin.do("POST", "/api/sites", `{"name":"web","domains":["web.test"],"access_list_id":1}`); code != 201 {
		t.Fatalf("crear sitio: %d %v", code, body)
	}
	_, inv := admin.do("POST", "/api/users", `{"name":"Dev","email":"dev@test.com","role":"deployer"}`)
	dev := newClient(t, ts)
	accept(t, dev, inv["link"].(string), "dev-secreto")

	loc := `{"name":"web","domains":["web.test"],"access_list_id":1,"locations":[{"path":"/x","target_type":"external","forward_scheme":"http","forward_host":"169.254.169.254","forward_port":80,"access":"public"}]}`
	if code, body := dev.do("PUT", "/api/sites/1", loc); code != 403 || body["code"] != "site_routing_admin_only" {
		t.Errorf("un deployer no añade ubicaciones: %d %v", code, body)
	}
	if code, _ := dev.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"]}`); code != 403 {
		t.Errorf("un deployer no quita la lista de acceso: %d", code)
	}
	if code, body := dev.do("PUT", "/api/sites/1", `{"name":"web","domains":["web.test"],"access_list_id":1,"spa":true}`); code != 200 {
		t.Errorf("un deployer sí edita lo demás: %d %v", code, body)
	}
	if code, _ := dev.do("POST", "/api/sites", `{"name":"otro","domains":["otro.test"],"locations":[{"path":"/x","target_type":"external","forward_scheme":"http","forward_host":"10.0.0.1","forward_port":80,"access":"public"}]}`); code != 403 {
		t.Errorf("un deployer no crea sitios con ubicaciones: %d", code)
	}
	if code, _ := admin.do("PUT", "/api/sites/1", loc); code != 200 {
		t.Errorf("el admin sí: %d", code)
	}
}

// Una cuenta de Git que usa una app no se puede borrar: la app se quedaría
// sin repositorio (ON DELETE SET NULL) y con el webhook huérfano.
func TestGitConnectionInUseByApp(t *testing.T) {
	gh := newFakeGitHub(t)
	ts, _, _ := testServerWithPages(t)
	admin := newClient(t, ts)
	admin.do("POST", "/api/setup", adminJSON)
	admin.do("POST", "/api/git/connections", fmt.Sprintf(`{"provider":"github","base_url":%q,"token":"ghp_bueno"}`, gh.URL))
	if code, body := admin.do("POST", "/api/apps", `{"name":"api","source":"git","connection_id":1,"repo":"oswa/web","branch":"main","port":8080}`); code != 201 {
		t.Fatalf("crear app: %d %v", code, body)
	}
	code, body := admin.do("DELETE", "/api/git/connections/1", "")
	if code != 409 || body["code"] != "git_connection_in_use" || body["params"].(map[string]any)["sites"] != "api" {
		t.Errorf("borrar una cuenta en uso por una app: %d %v", code, body)
	}
}
