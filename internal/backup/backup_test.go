package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dockfly/dockfly/internal/store"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(src, "dockfly.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Con la base de datos abierta, este cambio está en el WAL y no en
	// dockfly.db: es justo lo que se perdería copiando el archivo a mano.
	st.CreateSite(ctx, &store.Site{Name: "web", Domains: []string{"web.com"}})
	if fi, err := os.Stat(filepath.Join(src, "dockfly.db-wal")); err != nil || fi.Size() == 0 {
		t.Fatalf("el test necesita datos en el WAL: %v", err)
	}
	write(t, filepath.Join(src, "secret.key"), "clave-sesion")
	write(t, filepath.Join(src, "encryption.key"), "clave-cifrado")
	write(t, filepath.Join(src, "traefik/acme.json"), `{"certs":1}`)
	write(t, filepath.Join(src, "sites/1/7/index.html"), "hola")
	write(t, filepath.Join(src, "sites/1/.staging-x/basura"), "no")
	write(t, filepath.Join(src, "builds/1.log"), "no")

	var buf bytes.Buffer
	if err := Create(ctx, st, src, "1.2.3", &buf); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Restaurar en otra instalación que ya tenía datos propios.
	dst := t.TempDir()
	write(t, filepath.Join(dst, "dockfly.db"), "vieja")
	write(t, filepath.Join(dst, "secret.key"), "vieja")
	os.MkdirAll(filepath.Join(dst, "traefik"), 0o755)
	m, err := Stage(bytes.NewReader(buf.Bytes()), dst)
	if err != nil || m.Dockfly != "1.2.3" {
		t.Fatalf("Stage: %+v %v", m, err)
	}
	// Preparar la copia no toca los datos en uso.
	if b, _ := os.ReadFile(filepath.Join(dst, "secret.key")); string(b) != "vieja" {
		t.Fatal("Stage no debe modificar los datos actuales")
	}
	applied, kept, err := ApplyPending(dst)
	if err != nil || !applied {
		t.Fatalf("ApplyPending: %v %v", applied, err)
	}
	if again, _, _ := ApplyPending(dst); again {
		t.Error("una copia solo se aplica una vez")
	}

	st2, err := store.Open(ctx, filepath.Join(dst, "dockfly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if sites, _ := st2.ListSites(ctx); len(sites) != 1 || sites[0].Name != "web" {
		t.Errorf("los datos que estaban en el WAL deben restaurarse: %v", sites)
	}
	for name, want := range map[string]string{
		"secret.key": "clave-sesion", "encryption.key": "clave-cifrado",
		"traefik/acme.json": `{"certs":1}`, "sites/1/7/index.html": "hola",
	} {
		if b, err := os.ReadFile(filepath.Join(dst, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dst, "traefik/acme.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("acme.json debe conservar sus permisos 0600 (Traefik lo exige): %v", fi.Mode().Perm())
	}
	for _, name := range []string{"sites/1/.staging-x", "builds/1.log"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err == nil {
			t.Errorf("%s no debe formar parte de la copia", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(kept, "secret.key")); string(b) != "vieja" {
		t.Error("los datos anteriores deben conservarse en before-restore-*")
	}
}

func makeTarGz(t *testing.T, entries map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestStageRejectsBadArchives(t *testing.T) {
	cases := map[string]map[string]string{
		"sin manifiesto":        {"dockfly.db": "x"},
		"sin base de datos":     {manifestName: `{"version":1}`},
		"de una versión futura": {manifestName: `{"version":99,"dockfly":"9.0"}`, "dockfly.db": "x"},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Stage(bytes.NewReader(makeTarGz(t, entries)), dir); !errors.Is(err, ErrInvalid) {
				t.Errorf("esperaba ErrInvalid, obtuve %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, pendingDir)); err == nil {
				t.Error("una copia inválida no debe quedar preparada")
			}
		})
	}
	if _, err := Stage(strings.NewReader("no es gzip"), t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Errorf("basura: %v", err)
	}

	// Rutas maliciosas y archivos ajenos a Dockfly se ignoran.
	dir := t.TempDir()
	archive := makeTarGz(t, map[string]string{
		manifestName: `{"version":1}`, "dockfly.db": "x",
		"../../fuera.txt": "malo", "sites/../../../etc/x": "malo", "otro/archivo": "ajeno",
	})
	if _, err := Stage(bytes.NewReader(archive), dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../fuera.txt", "restore-pending/otro", "restore-pending/fuera.txt"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s no debería existir", p)
		}
	}
}
