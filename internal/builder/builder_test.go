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

// makeTarball mimics GitHub's .tar.gz: everything under "user-repo-sha/" and
// with the pax global header that GitHub adds with the commit.
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
	return fakeGitHubStatuses(t, tarball, nil)
}

// statusLog keeps the commit statuses the fake GitHub receives.
type statusLog struct {
	mu   sync.Mutex
	list []map[string]string
}

func (l *statusLog) all() []map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]string(nil), l.list...)
}

func fakeGitHubStatuses(t *testing.T, tarball []byte, statuses *statusLog) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_test" {
			w.WriteHeader(401)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/repos/oswa/web/statuses/abc123def" && statuses != nil {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			statuses.mu.Lock()
			statuses.list = append(statuses.list, body)
			statuses.mu.Unlock()
			w.WriteHeader(201)
			return
		}
		switch r.URL.Path {
		case "/repos/oswa/web/commits/feature/login":
			io.WriteString(w, `{"sha":"abc123def","commit":{"message":"Login"}}`)
		case "/repos/oswa/web/commits/main":
			io.WriteString(w, `{"sha":"abc123def","commit":{"message":"New homepage"}}`)
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

// wait waits for the build to finish and returns its final state and its log.
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
	t.Fatal("the build did not finish in time")
	return nil, ""
}

// With no build commands Docker is not needed: the repo is published as is.
func TestPlainHTMLWithoutDocker(t *testing.T) {
	gh := fakeGitHub(t, makeTarball(t, map[string]string{
		"public/index.html": "hello", "README.md": "not published",
	}))
	e := setup(t, gh.URL, store.SiteGit{OutputDir: "public"}, nil, nil)

	b, err := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", "", gitprovider.Commit{})
	if err != nil {
		t.Fatal(err)
	}
	got, log := e.wait(t, b.ID)
	if got.Status != store.BuildSuccess {
		t.Fatalf("status %s (%s)\n%s", got.Status, got.Error, log)
	}
	if got.CommitSHA != "abc123def" || got.CommitMessage != "New homepage" {
		t.Errorf("a manual build must resolve the latest commit: %+v", got)
	}
	dir := e.pages.DeploymentDir(e.site.ID, *got.DeploymentID)
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != "hello" {
		t.Errorf("published index.html: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("only the contents of the output directory must be published")
	}
}

// A preview publishes its own deployment without touching the production one
// and marks the commit with the result.
func TestPreviewBuildAndStatus(t *testing.T) {
	var statuses statusLog
	gh := fakeGitHubStatuses(t, makeTarball(t, map[string]string{"index.html": "branch"}), &statuses)
	e := setup(t, gh.URL, store.SiteGit{}, nil, nil)
	e.mgr.cfg.StatusURL = func(_ context.Context, _ *store.Site, b *store.Build, depID int64) string {
		if depID != 0 {
			return "https://" + b.Preview + "--web.previews.test"
		}
		return "https://panel.test/sites/1"
	}

	b, err := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerPush, "feature/login", "feature-login", gitprovider.Commit{})
	if err != nil {
		t.Fatal(err)
	}
	got, log := e.wait(t, b.ID)
	if got.Status != store.BuildSuccess {
		t.Fatalf("status %s (%s)\n%s", got.Status, got.Error, log)
	}
	dep, err := e.st.GetDeployment(context.Background(), e.site.ID, *got.DeploymentID)
	if err != nil || dep.Preview != "feature-login" || dep.Branch != "feature/login" {
		t.Fatalf("preview deployment: %+v %v", dep, err)
	}
	if prod, _ := e.st.ListDeployments(context.Background(), e.site.ID); len(prod) != 0 {
		t.Errorf("the preview must not show up among the production deployments: %+v", prod)
	}
	if prev, _ := e.st.ListPreviews(context.Background(), e.site.ID); len(prev) != 1 || prev[0].ID != dep.ID {
		t.Errorf("previews: %+v", prev)
	}

	st := statuses.all()
	if len(st) != 2 {
		t.Fatalf("expected 2 statuses (pending and result): %+v", st)
	}
	if st[0]["state"] != "pending" || st[0]["context"] != "dockfly/web (preview)" {
		t.Errorf("first status: %+v", st[0])
	}
	if st[1]["state"] != "success" || st[1]["target_url"] != "https://feature-login--web.previews.test" {
		t.Errorf("final status: %+v", st[1])
	}
}

// A preview's queued builds do not cancel the production ones.
func TestSupersedeKeepsOtherPreviews(t *testing.T) {
	e := setup(t, "http://127.0.0.1:1", store.SiteGit{}, nil, nil)
	ctx := context.Background()
	mk := func(preview string) *store.Build {
		b := &store.Build{SiteID: e.site.ID, Status: store.BuildQueued, Trigger: store.TriggerPush, Preview: preview}
		if err := e.st.CreateBuild(ctx, b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	prod, prev1 := mk(""), mk("x")
	prev2 := mk("x")
	ids, err := e.st.SupersedeQueued(ctx, e.site.ID, prev2.ID)
	if err != nil || len(ids) != 1 || ids[0] != prev1.ID {
		t.Fatalf("only the earlier build of the same preview must be canceled: %v %v", ids, err)
	}
	if b, _ := e.st.GetBuild(ctx, prod.ID); b.Status == store.BuildCanceled {
		t.Errorf("the production build must not be canceled: %s", b.Status)
	}
}

func dockerClient(t *testing.T) *docker.Client {
	t.Helper()
	if os.Getenv("DOCKFLY_DOCKER_TESTS") != "1" {
		t.Skip("set DOCKFLY_DOCKER_TESTS=1 to run the builds against a real Docker")
	}
	dc := docker.New("/var/run/docker.sock")
	if !dc.Available(context.Background()) {
		t.Skip("Docker is unavailable")
	}
	return dc
}

func TestBuildWithBun(t *testing.T) {
	dc := dockerClient(t)
	pkg := `{"name":"web","scripts":{"build":"mkdir -p dist && cp index.html dist/ && echo \"$API_URL\" > dist/env.txt && id -u > dist/uid.txt"}}`
	gh := fakeGitHub(t, makeTarball(t, map[string]string{
		"app/package.json": pkg,
		"app/index.html":   "<h1>from bun</h1>",
	}))
	e := setup(t, gh.URL, store.SiteGit{
		RootDir: "app", InstallCommand: DefaultInstall, BuildCommand: DefaultBuild, OutputDir: "dist", BuildImage: DefaultImage,
	}, map[string]string{"API_URL": "https://api.example.com"}, dc)

	b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerPush, "main", "",
		gitprovider.Commit{SHA: "abc123def", Message: "New homepage"})
	got, log := e.wait(t, b.ID)
	if got.Status != store.BuildSuccess {
		t.Fatalf("status %s (%s)\n%s", got.Status, got.Error, log)
	}
	dir := e.pages.DeploymentDir(e.site.ID, *got.DeploymentID)
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b))
	}
	if read("index.html") != "<h1>from bun</h1>" {
		t.Errorf("index.html: %q", read("index.html"))
	}
	if read("env.txt") != "https://api.example.com" {
		t.Errorf("the encrypted environment variables must reach the build: %q", read("env.txt"))
	}
	if read("uid.txt") != "1000" {
		t.Errorf("the build must run unprivileged (uid 1000), it ran as %q", read("uid.txt"))
	}
	for _, want := range []string{"$ bun install", "$ bun run build", "✔ Published"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log must contain %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "ghp_test") {
		t.Error("the token must not appear in the log")
	}
	dep, _ := e.st.GetDeployment(context.Background(), e.site.ID, *got.DeploymentID)
	if dep.Source != "github" || dep.CommitSHA != "abc123def" || dep.Branch != "main" {
		t.Errorf("the deployment must remember its commit: %+v", dep)
	}
	assertNoBuildContainers(t, dc)
}

func TestBuildFailures(t *testing.T) {
	dc := dockerClient(t)
	gh := fakeGitHub(t, makeTarball(t, map[string]string{"package.json": `{"name":"x"}`}))

	cases := []struct {
		name, build, output, wantErr, wantLog string
	}{
		{"exit code", "echo 'something failed' >&2; exit 3", "dist", "the build exited with code 3", "something failed"},
		{"missing output folder", "echo ok", "dist", `output folder "dist" not found`, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t, gh.URL, store.SiteGit{BuildCommand: c.build, OutputDir: c.output, BuildImage: DefaultImage}, nil, dc)
			b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", "", gitprovider.Commit{SHA: "abc123def"})
			got, log := e.wait(t, b.ID)
			if got.Status != store.BuildFailed || !strings.Contains(got.Error, c.wantErr) {
				t.Errorf("expected failure %q, got %s %q\n%s", c.wantErr, got.Status, got.Error, log)
			}
			if !strings.Contains(log, c.wantLog) {
				t.Errorf("the command output (stderr included) must be in the log:\n%s", log)
			}
			if len(e.published) != 0 {
				t.Error("a failed build must not publish anything")
			}
		})
	}
	assertNoBuildContainers(t, dc)
}

func TestCancelRunningBuild(t *testing.T) {
	dc := dockerClient(t)
	gh := fakeGitHub(t, makeTarball(t, map[string]string{"package.json": `{}`}))
	e := setup(t, gh.URL, store.SiteGit{BuildCommand: "echo starting; sleep 120", OutputDir: "dist", BuildImage: DefaultImage}, nil, dc)

	b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", "", gitprovider.Commit{SHA: "abc123def"})
	// Wait until the container is actually running the command.
	for i := 0; i < 300; i++ {
		if log, _ := os.ReadFile(e.mgr.LogPath(b.ID)); strings.Contains(string(log), "starting") {
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
		t.Errorf("expected canceled, got %s (%s)", got.Status, got.Error)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("canceling must stop the container right away, it took %s", time.Since(start))
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
		t.Errorf("%d build containers were left behind", len(ids))
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
	e.mgr.ClearCache(context.Background(), e.site.ID) // in case one was left from another run

	installTime := regexp.MustCompile(`packages installed \[([0-9.]+m?s)\]`)
	var logs [2]string
	for i := range 2 {
		b, _ := e.mgr.Enqueue(context.Background(), e.site.ID, store.TriggerManual, "main", "", gitprovider.Commit{SHA: "abc123def"})
		got, log := e.wait(t, b.ID)
		if got.Status != store.BuildSuccess {
			t.Fatalf("build %d: %s (%s)\n%s", i+1, got.Status, got.Error, log)
		}
		logs[i] = log
		if m := installTime.FindStringSubmatch(log); m != nil {
			t.Logf("build %d: bun install took %s", i+1, m[1])
		}
	}
	if !strings.Contains(logs[0], "Dependency cache created") {
		t.Errorf("the first build must create the cache:\n%s", logs[0])
	}
	if strings.Contains(logs[1], "Dependency cache") {
		t.Errorf("the second build must reuse the cache instead of recreating it:\n%s", logs[1])
	}
	if ok, _ := dc.VolumeExists(context.Background(), CacheVolume(e.site.ID)); !ok {
		t.Error("the cache volume must exist after the build")
	}
	if err := e.mgr.ClearCache(context.Background(), e.site.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := dc.VolumeExists(context.Background(), CacheVolume(e.site.ID)); ok {
		t.Error("ClearCache must delete the volume")
	}
	assertNoBuildContainers(t, dc)
}

func TestPruneLogs(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, "1.log"), filepath.Join(dir, "app-2.log")
	os.WriteFile(old, []byte("x"), 0o644)
	os.WriteFile(fresh, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644)
	past := time.Now().Add(-40 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(filepath.Join(dir, "other.txt"), past, past)
	PruneLogs(dir, 30*24*time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the old log must be deleted")
	}
	for _, keep := range []string{fresh, filepath.Join(dir, "other.txt")} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s must not be deleted", keep)
		}
	}
}
