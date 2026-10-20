package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

// makeTarball imita el .tar.gz de GitHub: todo bajo "usuario-repo-sha/" y
// con la cabecera pax global que GitHub añade con el commit.
func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header",
		PAXRecords: map[string]string{"comment": "abc123"}})
	tw.WriteHeader(&tar.Header{Name: "oswa-web-abc123/", Typeflag: tar.TypeDir, Mode: 0o775})
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: "oswa-web-abc123/" + name, Typeflag: tar.TypeReg, Mode: 0o664, Size: int64(len(content))})
		io.WriteString(tw, content)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func fakeGitHub(t *testing.T, tarball []byte) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_test" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/repos/oswa/web/commits/main":
			io.WriteString(w, `{"sha":"abc123def","commit":{"message":"Portada nueva"}}`)
		case "/repos/oswa/web/tarball/abc123def":
			w.Write(tarball)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type env struct {
	st        *store.Store
	mgr       *Manager
	site      *store.Site
	pages     *pages.Storage
	mu        sync.Mutex
	published []int64
}

func setup(t *testing.T, ghURL string, git store.SiteGit, buildEnv map[string]string, dc *docker.Client) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{7}, 32))

	conn := &store.GitConnection{Provider: gitprovider.GitHub, BaseURL: ghURL, Username: "oswa", TokenEnc: box.Seal([]byte("ghp_test"))}
	st.CreateGitConnection(ctx, conn)
	site := &store.Site{Name: "web", Domains: []string{"web.test"}}
	st.CreateSite(ctx, site)
	git.GitConnectionID, git.Repo, git.Branch = &conn.ID, "oswa/web", "main"
	if buildEnv != nil {
		raw, _ := json.Marshal(buildEnv)
		git.BuildEnvEnc = box.Seal(raw)
	}
	if err := st.UpdateSiteGit(ctx, site.ID, &git); err != nil {
		t.Fatal(err)
	}

	e := &env{st: st, site: site, pages: &pages.Storage{Root: filepath.Join(dir, "sites"), Limits: pages.DefaultLimits}}
	e.mgr = New(Config{
		Store: st, Docker: dc, Secrets: box, Providers: gitauth.New(st, box), Pages: e.pages, LogDir: filepath.Join(dir, "logs"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Publish: func(ctx context.Context, siteID, depID int64) error {
			e.mu.Lock()
			e.published = append(e.published, depID)
			e.mu.Unlock()
			return st.SetActiveDeployment(ctx, siteID, &depID)
		},
		Timeout: 3 * time.Minute, MemoryMB: 512, CPUs: 1,
	})
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go e.mgr.Run(runCtx)
	return e
}

// wait espera a que el build termine y devuelve su estado final y su log.
func (e *env) wait(t *testing.T, id int64) (*store.Build, string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		b, err := e.st.GetBuild(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if b.Finished() {
			logBytes, _ := os.ReadFile(e.mgr.LogPath(id))
			return b, string(logBytes)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("el build no terminó a tiempo")
	return nil, ""
}

// Sin comandos de build no hace falta Docker: se publica el repo tal cual.
func TestPlainHTMLWithoutDocker(t *testing.T) {
	gh := fakeGitHub(t, makeTarball(t, map[string]string{
		"public/index.html": "hola", "README.md": "no se publica",
	}))
	e := setup(t, gh.URL, store.SiteGit{OutputDir: "public"}, nil, nil)

	b, err := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", gitprovider.Commit{})
	if err != nil {
		t.Fatal(err)
	}
	got, log := e.wait(t, b.ID)
	if got.Status != store.BuildSuccess {
		t.Fatalf("estado %s (%s)\n%s", got.Status, got.Error, log)
	}
	if got.CommitSHA != "abc123def" || got.CommitMessage != "Portada nueva" {
		t.Errorf("un build manual debe resolver el último commit: %+v", got)
	}
	dir := e.pages.DeploymentDir(e.site.ID, *got.DeploymentID)
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != "hola" {
		t.Errorf("index.html publicado: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("solo debe publicarse el contenido del directorio de salida")
	}
}

func dockerClient(t *testing.T) *docker.Client {
	t.Helper()
	if os.Getenv("DOCKFLY_DOCKER_TESTS") != "1" {
		t.Skip("define DOCKFLY_DOCKER_TESTS=1 para ejecutar los builds con Docker real")
	}
	dc := docker.New("/var/run/docker.sock")
	if !dc.Available(context.Background()) {
		t.Skip("Docker no está disponible")
	}
	return dc
}

func TestBuildWithBun(t *testing.T) {
	dc := dockerClient(t)
	pkg := `{"name":"web","scripts":{"build":"mkdir -p dist && cp index.html dist/ && echo \"$API_URL\" > dist/env.txt && id -u > dist/uid.txt"}}`
	gh := fakeGitHub(t, makeTarball(t, map[string]string{
		"app/package.json": pkg,
		"app/index.html":   "<h1>desde bun</h1>",
	}))
	e := setup(t, gh.URL, store.SiteGit{
		RootDir: "app", InstallCommand: DefaultInstall, BuildCommand: DefaultBuild, OutputDir: "dist", BuildImage: DefaultImage,
	}, map[string]string{"API_URL": "https://api.ejemplo.com"}, dc)

	b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerPush, "main",
		gitprovider.Commit{SHA: "abc123def", Message: "Portada nueva"})
	got, log := e.wait(t, b.ID)
	if got.Status != store.BuildSuccess {
		t.Fatalf("estado %s (%s)\n%s", got.Status, got.Error, log)
	}
	dir := e.pages.DeploymentDir(e.site.ID, *got.DeploymentID)
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	if read("index.html") != "<h1>desde bun</h1>" {
		t.Errorf("index.html: %q", read("index.html"))
	}
	if read("env.txt") != "https://api.ejemplo.com" {
		t.Errorf("las variables de entorno cifradas deben llegar al build: %q", read("env.txt"))
	}
	if read("uid.txt") != "1000" {
		t.Errorf("el build debe correr sin privilegios (uid 1000), corrió como %q", read("uid.txt"))
	}
	for _, want := range []string{"$ bun install", "$ bun run build", "✔ Publicado"} {
		if !strings.Contains(log, want) {
			t.Errorf("el log debe contener %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "ghp_test") {
		t.Error("el token no debe aparecer en el log")
	}
	dep, _ := e.st.GetDeployment(context.Background(), e.site.ID, *got.DeploymentID)
	if dep.Source != "github" || dep.CommitSHA != "abc123def" || dep.Branch != "main" {
		t.Errorf("el despliegue debe recordar su commit: %+v", dep)
	}
	assertNoBuildContainers(t, dc)
}

func TestBuildFailures(t *testing.T) {
	dc := dockerClient(t)
	gh := fakeGitHub(t, makeTarball(t, map[string]string{"package.json": `{"name":"x"}`}))

	cases := []struct {
		name, build, output, wantErr, wantLog string
	}{
		{"código de salida", "echo 'algo falló' >&2; exit 3", "dist", "el build terminó con código 3", "algo falló"},
		{"sin carpeta de salida", "echo ok", "dist", `no se encontró la carpeta de salida "dist"`, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, gh.URL, store.SiteGit{BuildCommand: c.build, OutputDir: c.output, BuildImage: DefaultImage}, nil, dc)
			b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", gitprovider.Commit{SHA: "abc123def"})
			got, log := e.wait(t, b.ID)
			if got.Status != store.BuildFailed || !strings.Contains(got.Error, c.wantErr) {
				t.Errorf("esperaba fallo %q, obtuve %s %q\n%s", c.wantErr, got.Status, got.Error, log)
			}
			if !strings.Contains(log, c.wantLog) {
				t.Errorf("la salida del comando (stderr incluido) debe estar en el log:\n%s", log)
			}
			if len(e.published) != 0 {
				t.Error("un build fallido no debe publicar nada")
			}
		})
	}
	assertNoBuildContainers(t, dc)
}

func TestCancelRunningBuild(t *testing.T) {
	dc := dockerClient(t)
	gh := fakeGitHub(t, makeTarball(t, map[string]string{"package.json": `{}`}))
	e := setup(t, gh.URL, store.SiteGit{BuildCommand: "echo empezando; sleep 120", OutputDir: "dist", BuildImage: DefaultImage}, nil, dc)

	b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", gitprovider.Commit{SHA: "abc123def"})
	// Esperamos a que el contenedor esté ejecutando el comando.
	for i := 0; i < 300; i++ {
		if log, _ := os.ReadFile(e.mgr.LogPath(b.ID)); strings.Contains(string(log), "empezando") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	start := time.Now()
	if err := e.mgr.Cancel(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.wait(t, b.ID)
	if got.Status != store.BuildCanceled {
		t.Errorf("esperaba cancelado, obtuve %s (%s)", got.Status, got.Error)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("cancelar debe detener el contenedor de inmediato, tardó %s", time.Since(start))
	}
	assertNoBuildContainers(t, dc)
}

func assertNoBuildContainers(t *testing.T, dc *docker.Client) {
	t.Helper()
	ids, err := dc.ContainersWithLabel(context.Background(), LabelBuild)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("quedaron %d contenedores de build sin borrar", len(ids))
		for _, id := range ids {
			dc.RemoveContainer(context.Background(), id)
		}
	}
}

func TestBuildCacheIsReused(t *testing.T) {
	dc := dockerClient(t)
	pkg := `{"name":"web","dependencies":{"vite":"7.1.0"},"scripts":{"build":"mkdir -p dist && echo ok > dist/index.html"}}`
	gh := fakeGitHub(t, makeTarball(t, map[string]string{"package.json": pkg}))
	e := setup(t, gh.URL, store.SiteGit{InstallCommand: DefaultInstall, BuildCommand: DefaultBuild, OutputDir: "dist", BuildImage: DefaultImage}, nil, dc)
	t.Cleanup(func() { e.mgr.ClearCache(context.Background(), e.site.ID) })
	e.mgr.ClearCache(context.Background(), e.site.ID) // por si quedó de otra ejecución

	installTime := regexp.MustCompile(`packages installed \[([0-9.]+m?s)\]`)
	var logs [2]string
	for i := range 2 {
		b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", gitprovider.Commit{SHA: "abc123def"})
		got, log := e.wait(t, b.ID)
		if got.Status != store.BuildSuccess {
			t.Fatalf("build %d: %s (%s)\n%s", i+1, got.Status, got.Error, log)
		}
		logs[i] = log
		if m := installTime.FindStringSubmatch(log); m != nil {
			t.Logf("build %d: bun install tardó %s", i+1, m[1])
		}
	}
	if !strings.Contains(logs[0], "Caché de dependencias creada") {
		t.Errorf("el primer build debe crear la caché:\n%s", logs[0])
	}
	if strings.Contains(logs[1], "Caché de dependencias") {
		t.Errorf("el segundo build debe reutilizar la caché sin recrearla:\n%s", logs[1])
	}
	if ok, _ := dc.VolumeExists(context.Background(), CacheVolume(e.site.ID)); !ok {
		t.Error("el volumen de caché debe existir tras el build")
	}
	if err := e.mgr.ClearCache(context.Background(), e.site.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := dc.VolumeExists(context.Background(), CacheVolume(e.site.ID)); ok {
		t.Error("ClearCache debe borrar el volumen")
	}
	assertNoBuildContainers(t, dc)
}
