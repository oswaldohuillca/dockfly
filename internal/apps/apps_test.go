package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

func TestSplitCommand(t *testing.T) {
	cases := map[string][]string{
		"":                                   nil,
		"npm start":                          {"npm", "start"},
		`sh -c 'echo "hello world"; exit 1'`: {"sh", "-c", `echo "hello world"; exit 1`},
		`node  server.js   --port=3000 `:     {"node", "server.js", "--port=3000"},
		`echo ""`:                            {"echo", ""},
	}
	for in, want := range cases {
		got, err := SplitCommand(in)
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("SplitCommand(%q) = %q, %v; wanted %q", in, got, err, want)
		}
	}
	if _, err := SplitCommand(`echo "unclosed`); err == nil {
		t.Error("an unclosed quote must be an error")
	}
}

func TestBuildContext(t *testing.T) {
	var src bytes.Buffer
	gz := gzip.NewWriter(&src)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"repo-sha/apps/api/Dockerfile": "FROM x", "repo-sha/apps/api/main.go": "m", "repo-sha/apps/web/x": "no", "repo-sha/README": "no"} {
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		io.WriteString(tw, body)
	}
	tw.Close()
	gz.Close()
	var out bytes.Buffer
	if err := buildContext(&src, &out, "apps/api"); err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(&out)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "Dockerfile,main.go" && strings.Join(names, ",") != "main.go,Dockerfile" {
		t.Errorf("the context must contain apps/api only, at the root: %v", names)
	}
}

// --- With a real Docker ---

const testNetwork = "dockfly-apps-test"

type env struct {
	st    *store.Store
	m     *Manager
	dc    *docker.Client
	box   *secrets.Box
	apply atomic.Int32
}

func setup(t *testing.T) *env {
	t.Helper()
	if os.Getenv("DOCKFLY_DOCKER_TESTS") != "1" {
		t.Skip("set DOCKFLY_DOCKER_TESTS=1 to run the deployments against a real Docker")
	}
	dc := docker.New("/var/run/docker.sock")
	ctx := context.Background()
	if !dc.Available(ctx) {
		t.Skip("Docker is not available")
	}
	createNetwork(t, dc)
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{9}, 32))
	e := &env{st: st, dc: dc, box: box}
	e.m = New(Config{
		Store: st, Docker: dc, Secrets: box, Providers: gitauth.New(st, box), LogDir: filepath.Join(dir, "logs"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Network: testNetwork,
		Apply: func(context.Context) error { e.apply.Add(1); return nil },
	})
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go e.m.Run(runCtx)
	return e
}

// createNetwork creates the test network if it does not exist (via the API).
func createNetwork(t *testing.T, dc *docker.Client) {
	t.Helper()
	if err := dc.CreateNetwork(context.Background(), testNetwork); err != nil {
		t.Fatal(err)
	}
}

func (e *env) app(t *testing.T, a *store.App) *store.App {
	t.Helper()
	if a.Domains == nil {
		a.Domains = []string{}
	}
	if a.Volumes == nil {
		a.Volumes = []store.AppVolume{}
	}
	if err := e.st.CreateApp(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		got, err := e.st.GetApp(context.Background(), a.ID)
		if err == nil {
			e.m.Remove(context.Background(), got, true)
		}
	})
	return a
}

func (e *env) deploy(t *testing.T, appID int64) (*store.AppDeployment, string) {
	t.Helper()
	d, err := e.m.Enqueue(context.Background(), appID, "manual", gitprovider.Commit{}, "")
	if err != nil {
		t.Fatal(err)
	}
	// The deployment goes "live" before the previous version is retired, so
	// we wait for the log's last line, which marks the real end.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(e.m.LogPath(d.ID))
		if strings.Contains(string(log), "\n✔ ") || strings.Contains(string(log), "\n✘ ") {
			got, _ := e.st.GetAppDeployment(context.Background(), d.ID)
			return got, string(log)
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("the deployment did not finish")
	return nil, ""
}

func (e *env) running(t *testing.T, appID int64) []string {
	t.Helper()
	ids, err := e.dc.ContainersWithLabel(context.Background(), LabelApp+"="+itoa(appID))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, id := range ids {
		info, _ := e.dc.InspectContainer(context.Background(), id)
		if info != nil && info.Running {
			names = append(names, info.Name)
		}
	}
	return names
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestRollingDeployHasNoDowntime(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "web-rolling", Source: store.AppSourceImage, Image: "traefik/whoami", Port: 80, HealthPath: "/"})

	d1, log := e.deploy(t, app.ID)
	if d1.Status != store.DeployLive {
		t.Fatalf("first deployment: %s (%s)\n%s", d1.Status, d1.Error, log)
	}
	if !strings.Contains(log, "HTTP response on port 80/") {
		t.Errorf("the health path must be checked:\n%s", log)
	}

	// During the second deploy there are always exactly one or two
	// containers running: never zero (that would be downtime).
	var sawZero atomic.Bool
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
				if len(e.running(t, app.ID)) == 0 {
					sawZero.Store(true)
				}
			}
		}
	}()
	d2, log := e.deploy(t, app.ID)
	close(stop)
	if d2.Status != store.DeployLive {
		t.Fatalf("second deployment: %s (%s)\n%s", d2.Status, d2.Error, log)
	}
	if sawZero.Load() {
		t.Error("there was a moment with no container running: the deploy was meant to have no downtime")
	}
	if names := e.running(t, app.ID); len(names) != 1 || names[0] != ContainerName(app, d2.ID) {
		t.Errorf("after the deploy only the new one must remain: %v\n%s", names, log)
	}
	old, _ := e.st.GetAppDeployment(context.Background(), d1.ID)
	got, _ := e.st.GetApp(context.Background(), app.ID)
	if old.Status != store.DeployReplaced || *got.CurrentDeploymentID != d2.ID || e.apply.Load() < 2 {
		t.Errorf("state after the handover: old=%s current=%v apply=%d", old.Status, got.CurrentDeploymentID, e.apply.Load())
	}
}

func TestVolumesSurviveAndRecreate(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "db-recreate", Source: store.AppSourceImage, Image: "alpine:3.20",
		Command: `sh -c 'date +%s%N >> /data/starts; sleep 3600'`,
		Volumes: []store.AppVolume{{Name: "data", Path: "/data"}}})

	for i := range 2 {
		d, log := e.deploy(t, app.ID)
		if d.Status != store.DeployLive {
			t.Fatalf("deployment %d: %s (%s)\n%s", i+1, d.Status, d.Error, log)
		}
		if i == 1 && !strings.Contains(log, "is stopped before starting the new one") {
			t.Errorf("with volumes the recreate strategy must be used:\n%s", log)
		}
	}
	// Both starts wrote to the same volume: the data persists.
	got, _ := e.st.GetApp(context.Background(), app.ID)
	cur, _ := e.st.GetAppDeployment(context.Background(), *got.CurrentDeploymentID)
	out := e.exec(t, cur.Container, "wc", "-l", "/data/starts")
	if !strings.HasPrefix(strings.TrimSpace(out), "2") {
		t.Errorf("the volume must keep the data across deployments: %q", out)
	}
	if names := e.running(t, app.ID); len(names) != 1 {
		t.Errorf("with volumes there must never be two instances: %v", names)
	}
}

func TestFailedDeployKeepsPreviousVersion(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "api-fails", Source: store.AppSourceImage, Image: "alpine:3.20", Command: "sleep 3600"})
	good, _ := e.deploy(t, app.ID)
	if good.Status != store.DeployLive {
		t.Fatalf("good deployment: %s %s", good.Status, good.Error)
	}

	app.Command = `sh -c 'echo "DATABASE_URL is missing"; exit 3'`
	e.st.UpdateApp(context.Background(), app)
	bad, log := e.deploy(t, app.ID)
	if bad.Status != store.DeployFailed || !strings.Contains(bad.Error, "code 3") {
		t.Errorf("the broken deploy must fail with its exit code: %s %q", bad.Status, bad.Error)
	}
	if !strings.Contains(log, "DATABASE_URL is missing") {
		t.Errorf("the log must include the output of the container that failed:\n%s", log)
	}
	if names := e.running(t, app.ID); len(names) != 1 || names[0] != ContainerName(app, good.ID) {
		t.Errorf("the previous version must keep serving: %v", names)
	}
	got, _ := e.st.GetApp(context.Background(), app.ID)
	if *got.CurrentDeploymentID != good.ID {
		t.Error("a failed deploy must not change the live version")
	}
}

func TestDeployFromDockerfile(t *testing.T) {
	e := setup(t)
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"oswa-api-c1/service/Dockerfile": "FROM alpine:3.20\nRUN echo 'built by dockfly' > /message\nCMD [\"sh\", \"-c\", \"cat /message; sleep 3600\"]\n",
	} {
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		io.WriteString(tw, body)
	}
	tw.Close()
	gz.Close()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/oswa/api/commits/main":
			io.WriteString(w, `{"sha":"c1","commit":{"message":"API"}}`)
		case "/repos/oswa/api/tarball/c1":
			w.Write(tgz.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	defer gh.Close()
	conn := &store.GitConnection{Provider: "github", BaseURL: gh.URL, Username: "oswa", TokenEnc: e.box.Seal([]byte("t"))}
	e.st.CreateGitConnection(context.Background(), conn)

	app := e.app(t, &store.App{Name: "api-git", Source: store.AppSourceGit, GitConnectionID: &conn.ID, Repo: "oswa/api", Branch: "main", RootDir: "service"})
	d, log := e.deploy(t, app.ID)
	if d.Status != store.DeployLive || d.Image != "dockfly/api-git:"+itoa(d.ID) || d.CommitSHA != "c1" {
		t.Fatalf("deploy from a Dockerfile: %+v\n%s", d, log)
	}
	if out := e.logsOf(t, d.Container); !strings.Contains(out, "built by dockfly") {
		t.Errorf("the container must run the image that was built: %q", out)
	}
}

func TestStopStartRemove(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "lifecycle", Source: store.AppSourceImage, Image: "alpine:3.20", Command: "sleep 3600",
		Volumes: []store.AppVolume{{Name: "data", Path: "/data"}}})
	e.deploy(t, app.ID)
	ctx := context.Background()
	got, _ := e.st.GetApp(ctx, app.ID)

	if err := e.m.Stop(ctx, got); err != nil {
		t.Fatal(err)
	}
	if len(e.running(t, app.ID)) != 0 {
		t.Error("Stop must stop the container")
	}
	got, _ = e.st.GetApp(ctx, app.ID)
	if !got.Stopped {
		t.Error("the app must be left marked as stopped")
	}
	if err := e.m.Start(ctx, got); err != nil || len(e.running(t, app.ID)) != 1 {
		t.Errorf("Start: %v", err)
	}
	if err := e.m.Remove(ctx, got, true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.dc.VolumeExists(ctx, VolumeName(got, got.Volumes[0])); ok {
		t.Error("Remove with data must delete the volumes")
	}
}

// exec runs a command in a test container and returns its output.
func (e *env) exec(t *testing.T, container string, cmd ...string) string {
	t.Helper()
	out, err := e.dc.Exec(context.Background(), container, cmd)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) logsOf(t *testing.T, container string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rc, err := e.dc.Logs(ctx, container)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var b bytes.Buffer
	docker.Demux(rc, &b)
	return b.String()
}

func TestRelativeBinds(t *testing.T) {
	yml := []byte(`
services:
  web:
    image: x
    volumes:
      - ./uploads:/app/uploads
      - data:/data
      - /srv/absolute:/abs
      - type: bind
        source: ../config
        target: /config
`)
	got := relativeBinds(yml)
	if len(got) != 2 || !strings.Contains(strings.Join(got, "\n"), "./uploads") || !strings.Contains(strings.Join(got, "\n"), "../config") {
		t.Errorf("only the relative paths must be detected: %v", got)
	}
}

func TestComposeApp(t *testing.T) {
	e := setup(t)
	compose := `services:
  web:
    image: traefik/whoami
  cache:
    image: redis:7-alpine
    volumes:
      - data:/data
volumes:
  data: {}
`
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "oswa-stack-c1/docker-compose.yml", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(compose))})
	io.WriteString(tw, compose)
	tw.Close()
	gz.Close()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/oswa/stack/commits/main":
			io.WriteString(w, `{"sha":"c1","commit":{"message":"Stack"}}`)
		case "/repos/oswa/stack/tarball/c1":
			w.Write(tgz.Bytes())
		default:
			w.WriteHeader(404)
		}
	}))
	defer gh.Close()
	conn := &store.GitConnection{Provider: "github", BaseURL: gh.URL, Username: "oswa", TokenEnc: e.box.Seal([]byte("t"))}
	e.st.CreateGitConnection(context.Background(), conn)
	e.m.cfg.HostDockerSocket = "/var/run/docker.sock"

	app := e.app(t, &store.App{Name: "stack", Source: store.AppSourceCompose, GitConnectionID: &conn.ID,
		Repo: "oswa/stack", Branch: "main", ComposeService: "web", Port: 80})
	d, log := e.deploy(t, app.ID)
	if d.Status != store.DeployLive {
		t.Fatalf("deploy with Compose: %s (%s)\n%s", d.Status, d.Error, log)
	}
	if d.Container != "dockfly-stack-web-1" {
		t.Errorf("the web container must be the one that takes traffic: %q", d.Container)
	}
	info, err := e.dc.InspectContainer(context.Background(), d.Container)
	if err != nil || info.IPs[testNetwork] == "" {
		t.Errorf("the web service must be on Dockfly's network: %v %v", info.IPs, err)
	}
	ctx := context.Background()
	got, _ := e.st.GetApp(ctx, app.ID)
	if names, _ := e.m.projectContainers(ctx, got); len(names) != 2 {
		t.Errorf("the project must have 2 containers: %v", names)
	}
	if err := e.m.Remove(ctx, got, true); err != nil {
		t.Fatal(err)
	}
	if names, _ := e.m.projectContainers(ctx, got); len(names) != 0 {
		t.Errorf("compose down must delete the containers: %v", names)
	}
	if ok, _ := e.dc.VolumeExists(ctx, "dockfly-stack_data"); ok {
		t.Error("with data, compose down must delete the volumes")
	}
}
