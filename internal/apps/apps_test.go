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
		"":                                  nil,
		"npm start":                         {"npm", "start"},
		`sh -c 'echo "hola mundo"; exit 1'`: {"sh", "-c", `echo "hola mundo"; exit 1`},
		`node  server.js   --port=3000 `:    {"node", "server.js", "--port=3000"},
		`echo ""`:                           {"echo", ""},
	}
	for in, want := range cases {
		got, err := SplitCommand(in)
		if err != nil || strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("SplitCommand(%q) = %q, %v; esperaba %q", in, got, err, want)
		}
	}
	if _, err := SplitCommand(`echo "sin cerrar`); err == nil {
		t.Error("las comillas sin cerrar deben dar error")
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
		t.Errorf("el contexto debe contener solo apps/api, en la raíz: %v", names)
	}
}

// --- Con Docker real ---

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
		t.Skip("define DOCKFLY_DOCKER_TESTS=1 para ejecutar los despliegues con Docker real")
	}
	dc := docker.New("/var/run/docker.sock")
	ctx := context.Background()
	if !dc.Available(ctx) {
		t.Skip("Docker no está disponible")
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

// createNetwork crea la red de pruebas si no existe (mediante la API).
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
	// El despliegue pasa a "live" antes de retirar la versión anterior: se
	// espera a la última línea del log, que marca el final de verdad.
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(e.m.LogPath(d.ID))
		if strings.Contains(string(log), "\n✔ ") || strings.Contains(string(log), "\n✘ ") {
			got, _ := e.st.GetAppDeployment(context.Background(), d.ID)
			return got, string(log)
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("el despliegue no terminó")
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
		t.Fatalf("primer despliegue: %s (%s)\n%s", d1.Status, d1.Error, log)
	}
	if !strings.Contains(log, "respuesta HTTP en el puerto 80/") {
		t.Errorf("debe comprobarse la ruta de salud:\n%s", log)
	}

	// Durante el segundo despliegue, siempre hay exactamente uno o dos
	// contenedores en marcha: nunca cero (eso sería un corte).
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
		t.Fatalf("segundo despliegue: %s (%s)\n%s", d2.Status, d2.Error, log)
	}
	if sawZero.Load() {
		t.Error("hubo un momento sin ningún contenedor en marcha: el despliegue debía ser sin corte")
	}
	if names := e.running(t, app.ID); len(names) != 1 || names[0] != ContainerName(app, d2.ID) {
		t.Errorf("tras el despliegue solo debe quedar el nuevo: %v\n%s", names, log)
	}
	old, _ := e.st.GetAppDeployment(context.Background(), d1.ID)
	got, _ := e.st.GetApp(context.Background(), app.ID)
	if old.Status != store.DeployReplaced || *got.CurrentDeploymentID != d2.ID || e.apply.Load() < 2 {
		t.Errorf("estado tras el relevo: viejo=%s actual=%v apply=%d", old.Status, got.CurrentDeploymentID, e.apply.Load())
	}
}

func TestVolumesSurviveAndRecreate(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "db-recreate", Source: store.AppSourceImage, Image: "alpine:3.20",
		Command: `sh -c 'date +%s%N >> /data/arranques; sleep 3600'`,
		Volumes: []store.AppVolume{{Name: "data", Path: "/data"}}})

	for i := range 2 {
		d, log := e.deploy(t, app.ID)
		if d.Status != store.DeployLive {
			t.Fatalf("despliegue %d: %s (%s)\n%s", i+1, d.Status, d.Error, log)
		}
		if i == 1 && !strings.Contains(log, "se detiene la versión actual antes") {
			t.Errorf("con volúmenes debe usarse la estrategia de recreación:\n%s", log)
		}
	}
	// Los dos arranques escribieron en el mismo volumen: los datos persisten.
	got, _ := e.st.GetApp(context.Background(), app.ID)
	cur, _ := e.st.GetAppDeployment(context.Background(), *got.CurrentDeploymentID)
	out := e.exec(t, cur.Container, "wc", "-l", "/data/arranques")
	if !strings.HasPrefix(strings.TrimSpace(out), "2") {
		t.Errorf("el volumen debe conservar los datos entre despliegues: %q", out)
	}
	if names := e.running(t, app.ID); len(names) != 1 {
		t.Errorf("con volúmenes nunca debe haber dos instancias: %v", names)
	}
}

func TestFailedDeployKeepsPreviousVersion(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "api-falla", Source: store.AppSourceImage, Image: "alpine:3.20", Command: "sleep 3600"})
	good, _ := e.deploy(t, app.ID)
	if good.Status != store.DeployLive {
		t.Fatalf("despliegue bueno: %s %s", good.Status, good.Error)
	}

	app.Command = `sh -c 'echo "falta DATABASE_URL"; exit 3'`
	e.st.UpdateApp(context.Background(), app)
	bad, log := e.deploy(t, app.ID)
	if bad.Status != store.DeployFailed || !strings.Contains(bad.Error, "código 3") {
		t.Errorf("el despliegue roto debe fallar con su código: %s %q", bad.Status, bad.Error)
	}
	if !strings.Contains(log, "falta DATABASE_URL") {
		t.Errorf("el log debe incluir la salida del contenedor que falló:\n%s", log)
	}
	if names := e.running(t, app.ID); len(names) != 1 || names[0] != ContainerName(app, good.ID) {
		t.Errorf("la versión anterior debe seguir sirviendo: %v", names)
	}
	got, _ := e.st.GetApp(context.Background(), app.ID)
	if *got.CurrentDeploymentID != good.ID {
		t.Error("un despliegue fallido no debe cambiar la versión activa")
	}
}

func TestDeployFromDockerfile(t *testing.T) {
	e := setup(t)
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"oswa-api-c1/servicio/Dockerfile": "FROM alpine:3.20\nRUN echo 'construido por dockfly' > /mensaje\nCMD [\"sh\", \"-c\", \"cat /mensaje; sleep 3600\"]\n",
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

	app := e.app(t, &store.App{Name: "api-git", Source: store.AppSourceGit, GitConnectionID: &conn.ID, Repo: "oswa/api", Branch: "main", RootDir: "servicio"})
	d, log := e.deploy(t, app.ID)
	if d.Status != store.DeployLive || d.Image != "dockfly/api-git:"+itoa(d.ID) || d.CommitSHA != "c1" {
		t.Fatalf("despliegue desde Dockerfile: %+v\n%s", d, log)
	}
	if out := e.logsOf(t, d.Container); !strings.Contains(out, "construido por dockfly") {
		t.Errorf("el contenedor debe ejecutar la imagen construida: %q", out)
	}
}

func TestStopStartRemove(t *testing.T) {
	e := setup(t)
	app := e.app(t, &store.App{Name: "ciclo", Source: store.AppSourceImage, Image: "alpine:3.20", Command: "sleep 3600",
		Volumes: []store.AppVolume{{Name: "datos", Path: "/datos"}}})
	e.deploy(t, app.ID)
	ctx := context.Background()
	got, _ := e.st.GetApp(ctx, app.ID)

	if err := e.m.Stop(ctx, got); err != nil {
		t.Fatal(err)
	}
	if len(e.running(t, app.ID)) != 0 {
		t.Error("Stop debe detener el contenedor")
	}
	got, _ = e.st.GetApp(ctx, app.ID)
	if !got.Stopped {
		t.Error("la app debe quedar marcada como detenida")
	}
	if err := e.m.Start(ctx, got); err != nil || len(e.running(t, app.ID)) != 1 {
		t.Errorf("Start: %v", err)
	}
	if err := e.m.Remove(ctx, got, true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.dc.VolumeExists(ctx, VolumeName(got, got.Volumes[0])); ok {
		t.Error("Remove con datos debe borrar los volúmenes")
	}
}

// exec ejecuta un comando en un contenedor de prueba y devuelve su salida.
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
      - ./subidas:/app/subidas
      - datos:/data
      - /srv/absoluto:/abs
      - type: bind
        source: ../config
        target: /config
`)
	got := relativeBinds(yml)
	if len(got) != 2 || !strings.Contains(strings.Join(got, "\n"), "./subidas") || !strings.Contains(strings.Join(got, "\n"), "../config") {
		t.Errorf("deben detectarse solo las rutas relativas: %v", got)
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
      - datos:/data
volumes:
  datos: {}
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
		t.Fatalf("despliegue con Compose: %s (%s)\n%s", d.Status, d.Error, log)
	}
	if d.Container != "dockfly-stack-web-1" {
		t.Errorf("el contenedor web debe ser el que recibe tráfico: %q", d.Container)
	}
	info, err := e.dc.InspectContainer(context.Background(), d.Container)
	if err != nil || info.IPs[testNetwork] == "" {
		t.Errorf("el servicio web debe estar en la red de Dockfly: %v %v", info.IPs, err)
	}
	ctx := context.Background()
	got, _ := e.st.GetApp(ctx, app.ID)
	if names, _ := e.m.projectContainers(ctx, got); len(names) != 2 {
		t.Errorf("el proyecto debe tener 2 contenedores: %v", names)
	}
	if err := e.m.Remove(ctx, got, true); err != nil {
		t.Fatal(err)
	}
	if names, _ := e.m.projectContainers(ctx, got); len(names) != 0 {
		t.Errorf("compose down debe borrar los contenedores: %v", names)
	}
	if ok, _ := e.dc.VolumeExists(ctx, "dockfly-stack_datos"); ok {
		t.Error("con datos, compose down debe borrar los volúmenes")
	}
}
