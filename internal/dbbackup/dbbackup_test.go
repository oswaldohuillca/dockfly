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
			t.Errorf("Engine(%q) = %q, quería %q", image, got, want)
		}
	}
}

// --- Con Docker real ---

type dbCase struct {
	engine, image string
	env           []string
	// write escribe un dato; read lo lee ("" si no está).
	write, read, wipe []string
}

var cases = []dbCase{
	{
		engine: Postgres, image: "postgres:17-alpine",
		env:   []string{"POSTGRES_PASSWORD=secreto", "POSTGRES_USER=app", "POSTGRES_DB=app"},
		write: []string{"psql", "-U", "app", "-d", "app", "-c", "CREATE TABLE notas (t text); INSERT INTO notas VALUES ('hola desde postgres')"},
		read:  []string{"psql", "-U", "app", "-d", "app", "-tAc", "SELECT t FROM notas"},
		wipe:  []string{"psql", "-U", "app", "-d", "app", "-c", "DROP TABLE notas"},
	},
	{
		engine: MySQL, image: "mariadb:11",
		env:   []string{"MARIADB_ROOT_PASSWORD=secreto", "MARIADB_DATABASE=app"},
		write: []string{"mariadb", "-uroot", "-psecreto", "app", "-e", "CREATE TABLE notas (t text); INSERT INTO notas VALUES ('hola desde mariadb')"},
		read:  []string{"mariadb", "-uroot", "-psecreto", "app", "-N", "-e", "SELECT t FROM notas"},
		wipe:  []string{"mariadb", "-uroot", "-psecreto", "app", "-e", "DROP TABLE notas"},
	},
	{
		engine: Redis, image: "redis:7-alpine",
		write: []string{"redis-cli", "SET", "nota", "hola desde redis"},
		read:  []string{"redis-cli", "GET", "nota"},
	},
}

func TestBackupAndRestore(t *testing.T) {
	if os.Getenv("DOCKFLY_DOCKER_TESTS") != "1" {
		t.Skip("define DOCKFLY_DOCKER_TESTS=1 para probar con bases de datos reales")
	}
	dc := docker.New("/var/run/docker.sock")
	ctx := context.Background()
	if !dc.Available(ctx) {
		t.Skip("Docker no está disponible")
	}
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	local := &objstore.Local{Dir: filepath.Join(dir, "copias")}
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
			// Esperar a que acepte conexiones (las imágenes arrancan dos veces al inicializarse).
			deadline := time.Now().Add(2 * time.Minute)
			for {
				if _, code := exec(tc.write); code == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("la base de datos no arrancó")
				}
				time.Sleep(time.Second)
			}
			want, _ := exec(tc.read)
			if !strings.HasPrefix(want, "hola desde") {
				t.Fatalf("dato escrito: %q", want)
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
				t.Errorf("dos copias a la vez: %v", err)
			}
			for b.Status == store.DBBackupRunning {
				time.Sleep(300 * time.Millisecond)
				b, _ = st.GetDBBackup(ctx, app.ID, b.ID)
			}
			if b.Status != store.DBBackupSuccess || b.Size < 64 || b.Location != local.Dir {
				t.Fatalf("copia: %+v", b)
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
				t.Fatal("el dato debía haberse borrado")
			}
			if err := m.Restore(ctx, app, b); err != nil {
				t.Fatal(err)
			}
			if got, _ := exec(tc.read); got != want {
				t.Errorf("tras restaurar: %q, quería %q", got, want)
			}
		})
	}
}
