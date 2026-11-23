package dbbackup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/objstore"
	"github.com/dockfly/dockfly/internal/store"
)

func TestEngine(t *testing.T) {
	for image, want := range map[string]string{
		"postgres:17":                     Postgres,
		"postgres":                        Postgres,
		"docker.io/library/postgres:16":   Postgres,
		"postgis/postgis:16-3.4":          Postgres,
		"pgvector/pgvector:pg17":          Postgres,
		"bitnami/postgresql:latest":       Postgres,
		"mysql:8.4":                       MySQL,
		"mariadb:11@sha256:abc":           MySQL,
		"redis:7-alpine":                  Redis,
		"valkey/valkey:8":                 Redis,
		"registry.example.com:5000/redis": Redis,
		"nginx:alpine":                    "",
		"ghcr.io/user/postgres-backup":    "",
	} {
		if got := Engine(image); got != want {
			t.Errorf("Engine(%q) = %q, wanted %q", image, got, want)
		}
	}
}

// --- With a real Docker ---

type dbCase struct {
	engine, image string
	env           []string
	// write writes a value; read reads it back ("" if it is not there).
	write, read, wipe []string
}

var cases = []dbCase{
	{
		engine: Postgres, image: "postgres:17-alpine",
		env:   []string{"POSTGRES_PASSWORD=secret", "POSTGRES_USER=app", "POSTGRES_DB=app"},
		write: []string{"psql", "-U", "app", "-d", "app", "-c", "CREATE TABLE notes (t text); INSERT INTO notes VALUES ('hello from postgres')"},
		read:  []string{"psql", "-U", "app", "-d", "app", "-tAc", "SELECT t FROM notes"},
		wipe:  []string{"psql", "-U", "app", "-d", "app", "-c", "DROP TABLE notes"},
	},
	{
		engine: MySQL, image: "mariadb:11",
		env:   []string{"MARIADB_ROOT_PASSWORD=secret", "MARIADB_DATABASE=app"},
		write: []string{"mariadb", "-uroot", "-psecret", "app", "-e", "CREATE TABLE notes (t text); INSERT INTO notes VALUES ('hello from mariadb')"},
		read:  []string{"mariadb", "-uroot", "-psecret", "app", "-N", "-e", "SELECT t FROM notes"},
		wipe:  []string{"mariadb", "-uroot", "-psecret", "app", "-e", "DROP TABLE notes"},
	},
	{
		engine: Redis, image: "redis:7-alpine",
		write: []string{"redis-cli", "SET", "note", "hello from redis"},
		read:  []string{"redis-cli", "GET", "note"},
	},
}

func TestBackupAndRestore(t *testing.T) {
	if os.Getenv("DOCKFLY_DOCKER_TESTS") != "1" {
		t.Skip("set DOCKFLY_DOCKER_TESTS=1 to test against real databases")
	}
	dc := docker.New("/var/run/docker.sock")
	ctx := context.Background()
	if !dc.Available(ctx) {
		t.Skip("Docker is unavailable")
	}
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	local := &objstore.Local{Dir: filepath.Join(dir, "backups")}
	m := &Manager{
		Store: st, Docker: dc, TmpDir: filepath.Join(dir, "tmp"), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Storage: func(context.Context) (objstore.Store, error) { return local, nil },
	}

	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			if ok, _ := dc.ImageExists(ctx, tc.image); !ok {
				if err := dc.PullImage(ctx, tc.image, io.Discard); err != nil {
					t.Fatal(err)
				}
			}
			name := fmt.Sprintf("dockfly-test-db-%s-%d", tc.engine, time.Now().UnixNano())
			id, err := dc.CreateContainer(ctx, docker.ContainerSpec{Name: name, Image: tc.image, Env: tc.env, KeepCaps: true})
			if err != nil {
				t.Fatal(err)
			}
			defer dc.RemoveContainer(context.Background(), id)
			if err := dc.StartContainer(ctx, id); err != nil {
				t.Fatal(err)
			}
			exec := func(cmd []string) (string, int) {
				var out strings.Builder
				code, err := dc.ExecStream(ctx, id, cmd, nil, &out, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSpace(out.String()), code
			}
			// Wait until it accepts connections (the images start twice while
			// initializing).
			deadline := time.Now().Add(2 * time.Minute)
			for {
				if _, code := exec(tc.write); code == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the database did not start")
				}
				time.Sleep(time.Second)
			}
			want, _ := exec(tc.read)
			if !strings.HasPrefix(want, "hello from") {
				t.Fatalf("written value: %q", want)
			}

			app := &store.App{Name: tc.engine, Source: store.AppSourceImage, Image: tc.image}
			if err := st.CreateApp(ctx, app); err != nil {
				t.Fatal(err)
			}
			d := &store.AppDeployment{AppID: app.ID, Status: store.DeployRunning, Container: name}
			if err := st.CreateAppDeployment(ctx, d); err != nil {
				t.Fatal(err)
			}
			if err := st.GoLive(ctx, app.ID, d.ID); err != nil {
				t.Fatal(err)
			}
			app, _ = st.GetApp(ctx, app.ID)

			b, err := m.Start(ctx, app, "manual")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Start(ctx, app, "manual"); err != ErrBusy {
				t.Errorf("two backups at once: %v", err)
			}
			for b.Status == store.DBBackupRunning {
				time.Sleep(300 * time.Millisecond)
				b, _ = st.GetDBBackup(ctx, app.ID, b.ID)
			}
			if b.Status != store.DBBackupSuccess || b.Size < 64 || b.Location != local.Dir {
				t.Fatalf("backup: %+v", b)
			}

			if !CanRestore(tc.engine) {
				rc, err := m.Open(ctx, b)
				if err != nil {
					t.Fatal(err)
				}
				rc.Close()
				return
			}
			exec(tc.wipe)
			if got, _ := exec(tc.read); got == want {
				t.Fatal("the value should have been deleted")
			}
			if err := m.Restore(ctx, app, b); err != nil {
				t.Fatal(err)
			}
			if got, _ := exec(tc.read); got != want {
				t.Errorf("after restoring: %q, wanted %q", got, want)
			}
			// Before restoring, whatever was there got backed up.
			if list, _ := st.ListDBBackups(ctx, app.ID); len(list) != 2 || list[0].Status != store.DBBackupSuccess {
				t.Errorf("the backup taken before the restore is missing: %+v", list)
			}
			// A backup cut off halfway is not taken as restored.
			path := filepath.Join(local.Dir, b.ObjectKey)
			raw, _ := os.ReadFile(path)
			if err := os.WriteFile(path, raw[:len(raw)/2], 0o600); err != nil {
				t.Fatal(err)
			}
			if err := m.Restore(ctx, app, b); err == nil {
				t.Error("restoring a truncated backup must fail")
			}
		})
	}
}
