package dockerproxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCreate(t *testing.T) {
	const project = "dockfly-web"
	protected := []string{"/srv/dockfly/data"}
	ok := func(hostConfig string) string {
		return `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":` + hostConfig + `}`
	}
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{
		{"normal", ok(`{"Binds":["dockfly-web_db:/var/lib/postgresql/data"],"NetworkMode":"dockfly-web_default"}`), true},
		{"anónimo y ruta propia", ok(`{"Mounts":[{"Type":"volume","Source":""},{"Type":"bind","Source":"/srv/apps/web"}]}`), true},
		{"otro proyecto", `{"Labels":{"com.docker.compose.project":"dockfly-otra"},"HostConfig":{}}`, false},
		{"sin etiqueta", `{"HostConfig":{}}`, false},
		{"privileged", ok(`{"Privileged":true}`), false},
		{"cap_add", ok(`{"CapAdd":["SYS_ADMIN"]}`), false},
		{"devices", ok(`{"Devices":[{"PathOnHost":"/dev/sda"}]}`), false},
		{"gpus", ok(`{"DeviceRequests":[{"Count":-1}]}`), false},
		{"pid host", ok(`{"PidMode":"host"}`), false},
		{"red del host", ok(`{"NetworkMode":"host"}`), false},
		{"seccomp", ok(`{"SecurityOpt":["seccomp=unconfined"]}`), false},
		{"socket", ok(`{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}`), false},
		{"raíz", ok(`{"Binds":["/:/host"]}`), false},
		{"etc", ok(`{"Mounts":[{"Type":"bind","Source":"/etc/../etc/shadow"}]}`), false},
		{"socket en otra ruta", ok(`{"Binds":["/srv/podman.sock:/s"]}`), false},
		{"datos de Dockfly", ok(`{"Binds":["/srv/dockfly/data/dockfly.db:/x"]}`), false},
		{"carpeta con los datos de Dockfly", ok(`{"Binds":["/srv:/srv"]}`), false},
		{"volumen ajeno", ok(`{"Binds":["dockfly_data:/data"]}`), false},
		{"volumen ajeno por Mounts", ok(`{"Mounts":[{"Type":"volume","Source":"dockfly-otra_db"}]}`), false},
		{"volumes_from", ok(`{"VolumesFrom":["dockfly"]}`), false},
	} {
		err := checkCreate([]byte(tc.body), project, protected)
		if (err == nil) != tc.allowed {
			t.Errorf("%s: err=%v, permitido=%v", tc.name, err, tc.allowed)
		}
	}
}

func TestCheckRoute(t *testing.T) {
	for path, allowed := range map[string]bool{
		"/v1.47/containers/json":     true,
		"/v1.47/images/create":       true,
		"/v1.47/build":               true,
		"/session":                   true,
		"/_ping":                     true,
		"/v1.47/swarm/init":          false,
		"/v1.47/secrets":             false,
		"/v1.47/plugins/pull":        false,
		"/v1.47/containers/../swarm": false,
		"/v1.47/system/df":           false,
		"/v1.47/networks/x/connect":  true,
	} {
		r := httptest.NewRequest("POST", "http://d"+path, nil)
		if err := checkRoute(r, cleanRoute(path)); (err == nil) != allowed {
			t.Errorf("%s: err=%v, permitido=%v", path, err, allowed)
		}
	}
	r := httptest.NewRequest("POST", "http://d/v1.47/build?networkmode=host", nil)
	if checkRoute(r, "/build") == nil {
		t.Error("un build con la red del host no debe permitirse")
	}
}

// fakeDocker escucha en un socket unix como el Docker real.
func fakeDocker(t *testing.T) (dial func(context.Context) (net.Conn, error), seen chan string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	seen = make(chan string, 20)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/mio/json"):
			io.WriteString(w, `{"Config":{"Labels":{"com.docker.compose.project":"dockfly-web"}}}`)
			return
		case strings.HasSuffix(r.URL.Path, "/containers/dockfly/json"):
			io.WriteString(w, `{"Config":{"Labels":{}}}`)
			return
		case strings.HasSuffix(r.URL.Path, "/containers/fantasma/json"):
			w.WriteHeader(404)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/networks/4a38"):
			io.WriteString(w, `{"Name":"dockfly-web_default","Labels":{"com.docker.compose.project":"dockfly-web"}}`)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/networks/9f01"):
			io.WriteString(w, `{"Name":"dockfly","Labels":{}}`)
			return
		}
		seen <- r.Method + " " + r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			seen <- "body " + string(body)
		}
		io.WriteString(w, `{"ok":true}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}, seen
}

func TestProxy(t *testing.T) {
	dial, seen := fakeDocker(t)
	p := &Proxy{Dial: dial, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ts := httptest.NewServer(p.Handler("dockfly-web"))
	defer ts.Close()

	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, _ := do("GET", "/v1.47/containers/json", ""); code != 200 || <-seen != "GET /v1.47/containers/json" {
		t.Errorf("listar: %d", code)
	}
	create := `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"Binds":["dockfly-web_db:/d"]}}`
	if code, _ := do("POST", "/v1.47/containers/create?name=x", create); code != 200 {
		t.Errorf("crear: %d", code)
	}
	if got := <-seen; got != "POST /v1.47/containers/create" {
		t.Errorf("crear llegó como %q", got)
	}
	if got := <-seen; got != "body "+create {
		t.Errorf("el cuerpo debe llegar intacto: %q", got)
	}
	code, body := do("POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"Privileged":true}}`)
	var msg struct{ Message string }
	json.Unmarshal([]byte(body), &msg)
	if code != 403 || !strings.Contains(msg.Message, "privilegiados") {
		t.Errorf("privileged: %d %s", code, body)
	}
	if code, _ := do("POST", "/v1.47/containers/mio/start", ""); code != 200 || <-seen != "POST /v1.47/containers/mio/start" {
		t.Errorf("arrancar un contenedor del proyecto: %d", code)
	}
	if code, _ := do("GET", "/v1.47/containers/dockfly/archive?path=/data", ""); code != 403 {
		t.Errorf("copiar de un contenedor ajeno: %d", code)
	}
	if code, _ := do("POST", "/v1.47/containers/fantasma/stop", ""); code != 200 || <-seen != "POST /v1.47/containers/fantasma/stop" {
		t.Errorf("un contenedor inexistente lo responde Docker: %d", code)
	}
	for _, raw := range []string{"/v1.47/containers/mio/../dockfly/archive", "/v1.47/containers/mio%2F..%2Fdockfly/json", "/v1.47//swarm"} {
		req, _ := http.NewRequest("GET", ts.URL, nil)
		req.URL.Opaque = raw // tal cual, sin que el cliente la normalice
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("ruta no canónica %s: %d", raw, resp.StatusCode)
		}
	}
	// Volúmenes y redes: solo los del proyecto.
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-web_db"}`, 200},
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-otra_db"}`, 403},
		{"DELETE", "/v1.47/volumes/dockfly-web_db", "", 200},
		{"DELETE", "/v1.47/volumes/dockfly_data", "", 403},
		{"GET", "/v1.47/volumes/dockfly_data", "", 200},
		{"POST", "/v1.47/volumes/prune", "", 403},
		{"POST", "/v1.47/containers/prune", "", 403},
		{"POST", "/v1.47/networks/create", `{"Name":"dockfly-web_default"}`, 200},
		{"POST", "/v1.47/networks/create", `{"Name":"red-ajena"}`, 403},
		{"POST", "/v1.47/networks/dockfly/connect", `{"Container":"mio"}`, 200},
		{"POST", "/v1.47/networks/dockfly-web_default/connect", `{"Container":"dockfly"}`, 403},
		{"DELETE", "/v1.47/networks/4a38", "", 200},
		{"DELETE", "/v1.47/networks/9f01", "", 403},
	} {
		code, body := do(tc.method, tc.path, tc.body)
		if code != tc.want {
			t.Errorf("%s %s %s: %d, quería %d (%s)", tc.method, tc.path, tc.body, code, tc.want, body)
		}
		if code == 200 {
			<-seen
			if tc.body != "" {
				<-seen
			}
		}
	}
	if code, _ := do("POST", "/v1.47/swarm/init", "{}"); code != 403 {
		t.Errorf("swarm: %d", code)
	}
}
