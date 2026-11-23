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
		{"anonymous volume and own path", ok(`{"Mounts":[{"Type":"volume","Source":""},{"Type":"bind","Source":"/srv/apps/web"}]}`), true},
		{"another project", `{"Labels":{"com.docker.compose.project":"dockfly-other"},"HostConfig":{}}`, false},
		{"no label", `{"HostConfig":{}}`, false},
		{"privileged", ok(`{"Privileged":true}`), false},
		{"cap_add", ok(`{"CapAdd":["SYS_ADMIN"]}`), false},
		{"devices", ok(`{"Devices":[{"PathOnHost":"/dev/sda"}]}`), false},
		{"gpus", ok(`{"DeviceRequests":[{"Count":-1}]}`), false},
		{"pid host", ok(`{"PidMode":"host"}`), false},
		{"host network", ok(`{"NetworkMode":"host"}`), false},
		{"seccomp", ok(`{"SecurityOpt":["seccomp=unconfined"]}`), false},
		{"socket", ok(`{"Binds":["/var/run/docker.sock:/var/run/docker.sock"]}`), false},
		{"root", ok(`{"Binds":["/:/host"]}`), false},
		{"etc", ok(`{"Mounts":[{"Type":"bind","Source":"/etc/../etc/shadow"}]}`), false},
		{"socket under another path", ok(`{"Binds":["/srv/podman.sock:/s"]}`), false},
		{"Dockfly data", ok(`{"Binds":["/srv/dockfly/data/dockfly.db:/x"]}`), false},
		{"directory holding the Dockfly data", ok(`{"Binds":["/srv:/srv"]}`), false},
		{"volume of another app", ok(`{"Binds":["dockfly_data:/data"]}`), false},
		{"volume of another app through Mounts", ok(`{"Mounts":[{"Type":"volume","Source":"dockfly-other_db"}]}`), false},
		{"volumes_from", ok(`{"VolumesFrom":["dockfly"]}`), false},
		// C2: sharing namespaces with another (non-project) container.
		{"pid container (the proxy checks it against Docker)", ok(`{"PidMode":"container:dockfly-web-db-1"}`), true},
		{"network container of the project", ok(`{"NetworkMode":"container:dockfly-web-api-1"}`), true},
		{"network of another app", ok(`{"NetworkMode":"other_net"}`), false},
		{"Dockfly network", ok(`{"NetworkMode":"dockfly"}`), true},
		{"endpoint on another app's network", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"NetworkingConfig":{"EndpointsConfig":{"dockfly-web-db_default":{}}}}`, false},
		{"endpoint on its own network and Dockfly's", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"NetworkingConfig":{"EndpointsConfig":{"dockfly-web_default":{},"dockfly":{}}}}`, true},
		{"ipc shareable", ok(`{"IpcMode":"shareable"}`), true},
		// A1: HostConfig fields outside the allowlist.
		{"sysctls", ok(`{"Sysctls":{"net.ipv4.ip_forward":"1"}}`), false},
		{"empty maskedpaths (unmasks /proc)", ok(`{"MaskedPaths":[]}`), false},
		{"shortened maskedpaths", ok(`{"MaskedPaths":["/proc/kcore"]}`), false},
		{"oomkilldisable", ok(`{"OomKillDisable":true}`), false},
		{"cgroupparent", ok(`{"CgroupParent":"/"}`), false},
		{"runtime of another kind", ok(`{"Runtime":"custom"}`), false},
		{"tmpfs", ok(`{"Tmpfs":{"/run":""}}`), false},
		{"deviceCgroupRules", ok(`{"DeviceCgroupRules":["c 1:3 rwm"]}`), false},
		{"unknown future field", ok(`{"FutureEscapeField":true}`), false},
		// Mounts with a local VolumeOptions.DriverConfig: a bind to the host
		// without going through /volumes/create (the volume is created on the
		// fly inside the create).
		{"mount local volume binding /", ok(`{"Mounts":[{"Type":"volume","Source":"dockfly-web_x","VolumeOptions":{"DriverConfig":{"Name":"local","Options":{"type":"none","o":"bind","device":"/"}}}}]}`), false},
		{"mount anonymous volume binding /etc", ok(`{"Mounts":[{"Type":"volume","Source":"","VolumeOptions":{"DriverConfig":{"Options":{"device":"/etc"}}}}]}`), false},
		{"mount volume binding the Dockfly data", ok(`{"Mounts":[{"Type":"volume","Source":"dockfly-web_x","VolumeOptions":{"DriverConfig":{"Options":{"device":"/srv/dockfly/data"}}}}]}`), false},
		{"mount volume with a device under its own path", ok(`{"Mounts":[{"Type":"volume","Source":"dockfly-web_x","VolumeOptions":{"DriverConfig":{"Options":{"device":"/srv/apps/web"}}}}]}`), true},
		// Unmasking /proc with a lowercased key (Docker is case-insensitive).
		{"lowercased maskedpaths", ok(`{"maskedpaths":[]}`), false},
		{"lowercased readonlypaths", ok(`{"readonlypaths":[]}`), false},
		{"maskedpaths duplicated under another case", ok(`{"MaskedPaths":null,"maskedpaths":[]}`), false},
		{"lowercased sysctls", ok(`{"sysctls":{"net.ipv4.ip_forward":"1"}}`), false},
		// Mounting an ANCESTOR of a sensitive path (/var/lib ⊃ /var/lib/docker).
		{"bind to /var/lib", ok(`{"Binds":["/var/lib:/x"]}`), false},
		{"bind to /var", ok(`{"Mounts":[{"Type":"bind","Source":"/var"}]}`), false},
		{"device to /var/lib", ok(`{"Mounts":[{"Type":"volume","Source":"dockfly-web_x","VolumeOptions":{"DriverConfig":{"Options":{"device":"/var/lib"}}}}]}`), false},
		// uts/userns/cgroup set to host: refused just like pid/ipc/network.
		{"uts host", ok(`{"UTSMode":"host"}`), false},
		{"userns host", ok(`{"UsernsMode":"host"}`), false},
		{"cgroupns host", ok(`{"CgroupnsMode":"host"}`), false},
		// What Compose really sends: empty standard fields and defaults.
		{"typical compose", ok(`{"RestartPolicy":{"Name":"unless-stopped"},"Memory":134217728,"NetworkMode":"dockfly-web_default","CpuRealtimePeriod":0,"CpuRealtimeRuntime":0,"CgroupParent":"","Runtime":"","Cgroup":"","OomKillDisable":false,"Sysctls":null,"MaskedPaths":null,"ReadonlyPaths":null,"Tmpfs":null}`), true},
		{"default maskedpaths", ok(`{"MaskedPaths":["/proc/asound","/proc/acpi","/proc/kcore","/proc/keys","/proc/latency_stats","/proc/timer_list","/proc/timer_stats","/proc/sched_debug","/proc/scsi","/sys/firmware","/sys/devices/virtual/powercap"]}`), true},
		{"explicit runc", ok(`{"Runtime":"runc"}`), true},
		{"ordinary allowed fields", ok(`{"RestartPolicy":{"Name":"always"},"Memory":536870912,"CapDrop":["ALL"],"ReadonlyRootfs":true,"Init":true,"Ulimits":[{"Name":"nofile","Soft":1024,"Hard":2048}]}`), true},
	} {
		_, err := checkCreate([]byte(tc.body), project, "dockfly", protected)
		if (err == nil) != tc.allowed {
			t.Errorf("%s: err=%v, allowed=%v", tc.name, err, tc.allowed)
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
			t.Errorf("%s: err=%v, allowed=%v", path, err, allowed)
		}
	}
	r := httptest.NewRequest("POST", "http://d/v1.47/build?networkmode=host", nil)
	if checkRoute(r, "/build") == nil {
		t.Error("a build on the host network must not be allowed")
	}
}

// fakeDocker listens on a unix socket just like the real Docker.
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
		case strings.HasSuffix(r.URL.Path, "/containers/mine/json"):
			io.WriteString(w, `{"Config":{"Labels":{"com.docker.compose.project":"dockfly-web"}}}`)
			return
		case strings.HasSuffix(r.URL.Path, "/containers/dockfly-web-db-postgres-1/json"):
			io.WriteString(w, `{"Config":{"Labels":{"com.docker.compose.project":"dockfly-web-db"}}}`)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/networks/dockfly"):
			io.WriteString(w, `{"Name":"dockfly","Labels":{}}`)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/networks/dockfly-web_default"):
			io.WriteString(w, `{"Name":"dockfly-web_default","Labels":{"com.docker.compose.project":"dockfly-web"}}`)
			return
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/networks/dockfly-web-db_default"):
			io.WriteString(w, `{"Name":"dockfly-web-db_default","Labels":{"com.docker.compose.project":"dockfly-web-db"}}`)
			return
		case strings.HasSuffix(r.URL.Path, "/containers/dockfly/json"):
			io.WriteString(w, `{"Config":{"Labels":{}}}`)
			return
		case strings.HasSuffix(r.URL.Path, "/containers/ghost/json"):
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
	p := &Proxy{Dial: dial, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Network: "dockfly", Protected: []string{"/srv/dockfly/data"}}
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
		t.Errorf("list: %d", code)
	}
	create := `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"Binds":["dockfly-web_db:/d"]}}`
	if code, _ := do("POST", "/v1.47/containers/create?name=x", create); code != 200 {
		t.Errorf("create: %d", code)
	}
	if got := <-seen; got != "POST /v1.47/containers/create" {
		t.Errorf("create arrived as %q", got)
	}
	if got := <-seen; got != "body "+create {
		t.Errorf("the body must arrive untouched: %q", got)
	}
	code, body := do("POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"Privileged":true}}`)
	var msg struct{ Message string }
	json.Unmarshal([]byte(body), &msg)
	if code != 403 || !strings.Contains(msg.Message, "privileged") {
		t.Errorf("privileged: %d %s", code, body)
	}
	if code, _ := do("POST", "/v1.47/containers/mine/start", ""); code != 200 || <-seen != "POST /v1.47/containers/mine/start" {
		t.Errorf("start a container of the project: %d", code)
	}
	if code, _ := do("GET", "/v1.47/containers/dockfly/archive?path=/data", ""); code != 403 {
		t.Errorf("copy from another app's container: %d", code)
	}
	if code, _ := do("POST", "/v1.47/containers/ghost/stop", ""); code != 200 || <-seen != "POST /v1.47/containers/ghost/stop" {
		t.Errorf("a non-existent container is Docker's to answer: %d", code)
	}
	for _, raw := range []string{"/v1.47/containers/mine/../dockfly/archive", "/v1.47/containers/mine%2F..%2Fdockfly/json", "/v1.47//swarm"} {
		req, _ := http.NewRequest("GET", ts.URL, nil)
		req.URL.Opaque = raw // as is, without the client normalising it
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("non-canonical path %s: %d", raw, resp.StatusCode)
		}
	}
	// Volumes and networks: only the project's own.
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-web_db"}`, 200},
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-other_db"}`, 403},
		// A local volume with a device is a bind to the host: it would mount /
		// or the Dockfly data going around checkMount (there it is only a name).
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-web_x","Driver":"local","DriverOpts":{"type":"none","o":"bind","device":"/"}}`, 403},
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-web_x","DriverOpts":{"o":"bind","device":"/srv/dockfly/data"}}`, 403},
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-web_x","DriverOpts":{"Device":"/etc"}}`, 403},
		{"POST", "/v1.47/volumes/create", `{"Name":"dockfly-web_x","DriverOpts":{"device":"/srv/apps/web"}}`, 200},
		{"DELETE", "/v1.47/volumes/dockfly-web_db", "", 200},
		{"DELETE", "/v1.47/volumes/dockfly_data", "", 403},
		{"GET", "/v1.47/volumes/dockfly_data", "", 200},
		{"POST", "/v1.47/volumes/prune", "", 403},
		{"POST", "/v1.47/containers/prune", "", 403},
		{"POST", "/v1.47/networks/create", `{"Name":"dockfly-web_default"}`, 200},
		{"POST", "/v1.47/networks/create", `{"Name":"other-net"}`, 403},
		{"POST", "/v1.47/networks/dockfly/connect", `{"Container":"mine"}`, 200},
		{"POST", "/v1.47/networks/dockfly-web_default/connect", `{"Container":"dockfly"}`, 403},
		// Another app whose name starts the same ("web-db"): neither its network nor its containers.
		{"POST", "/v1.47/networks/dockfly-web-db_default/connect", `{"Container":"mine"}`, 403},
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"NetworkMode":"container:dockfly-web-db-postgres-1"}}`, 403},
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"PidMode":"container:mine"}}`, 200},
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"PidMode":"container:dockfly"}}`, 403},
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"IpcMode":"container:dockfly"}}`, 403},
		// uts/userns/cgroup: container:<x> only for the project's containers.
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"UTSMode":"container:mine"}}`, 200},
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"UTSMode":"container:dockfly"}}`, 403},
		{"POST", "/v1.47/containers/create", `{"Labels":{"com.docker.compose.project":"dockfly-web"},"HostConfig":{"CgroupnsMode":"container:dockfly"}}`, 403},
		// exec: privileged is not allowed (it would grant every capability).
		{"POST", "/v1.47/containers/mine/exec", `{"Privileged":true}`, 403},
		{"POST", "/v1.47/containers/mine/exec", `{"privileged":true}`, 403},
		{"POST", "/v1.47/containers/mine/exec", `{"Cmd":["ls"]}`, 200},
		{"POST", "/v1.47/containers/dockfly/exec", `{"Cmd":["ls"]}`, 403},
		{"DELETE", "/v1.47/networks/4a38", "", 200},
		{"DELETE", "/v1.47/networks/9f01", "", 403},
	} {
		code, body := do(tc.method, tc.path, tc.body)
		if code != tc.want {
			t.Errorf("%s %s %s: %d, want %d (%s)", tc.method, tc.path, tc.body, code, tc.want, body)
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
