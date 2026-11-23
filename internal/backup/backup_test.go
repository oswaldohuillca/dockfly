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
	// With the database open, this change lives in the WAL and not in
	// dockfly.db: exactly what copying the file by hand would lose.
	st.CreateSite(ctx, &store.Site{Name: "web", Domains: []string{"web.com"}})
	if fi, err := os.Stat(filepath.Join(src, "dockfly.db-wal")); err != nil || fi.Size() == 0 {
		t.Fatalf("the test needs data in the WAL: %v", err)
	}
	write(t, filepath.Join(src, "secret.key"), "session-key")
	write(t, filepath.Join(src, "encryption.key"), "encryption-key")
	write(t, filepath.Join(src, "traefik/acme.json"), `{"certs":1}`)
	write(t, filepath.Join(src, "sites/1/7/index.html"), "hello")
	write(t, filepath.Join(src, "sites/1/.staging-x/junk"), "no")
	write(t, filepath.Join(src, "builds/1.log"), "no")
	write(t, filepath.Join(src, "db-backups/pg/pg-1.sql.gz"), "dump")

	var buf bytes.Buffer
	if err := Create(ctx, st, src, "1.2.3", &buf); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Restore into another installation that already had data of its own.
	dst := t.TempDir()
	write(t, filepath.Join(dst, "dockfly.db"), "old")
	write(t, filepath.Join(dst, "secret.key"), "old")
	os.MkdirAll(filepath.Join(dst, "traefik"), 0o755)
	m, err := Stage(bytes.NewReader(buf.Bytes()), dst)
	if err != nil || m.Dockfly != "1.2.3" {
		t.Fatalf("Stage: %+v %v", m, err)
	}
	// Staging the backup does not touch the data in use.
	if b, _ := os.ReadFile(filepath.Join(dst, "secret.key")); string(b) != "old" {
		t.Fatal("Stage must not modify the current data")
	}
	applied, kept, err := ApplyPending(dst)
	if err != nil || !applied {
		t.Fatalf("ApplyPending: %v %v", applied, err)
	}
	if again, _, _ := ApplyPending(dst); again {
		t.Error("a backup is only applied once")
	}

	st2, err := store.Open(ctx, filepath.Join(dst, "dockfly.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if sites, _ := st2.ListSites(ctx); len(sites) != 1 || sites[0].Name != "web" {
		t.Errorf("the data that was in the WAL must be restored: %v", sites)
	}
	for name, want := range map[string]string{
		"secret.key": "session-key", "encryption.key": "encryption-key",
		"traefik/acme.json": `{"certs":1}`, "sites/1/7/index.html": "hello",
		"db-backups/pg/pg-1.sql.gz": "dump",
	} {
		if b, err := os.ReadFile(filepath.Join(dst, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dst, "traefik/acme.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("acme.json must keep its 0600 permissions (Traefik requires it): %v", fi.Mode().Perm())
	}
	for _, name := range []string{"sites/1/.staging-x", "builds/1.log"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err == nil {
			t.Errorf("%s must not be part of the backup", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(kept, "secret.key")); string(b) != "old" {
		t.Error("the previous data must be kept in before-restore-*")
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
		"no manifest":    {"dockfly.db": "x"},
		"no database":    {manifestName: `{"version":1}`},
		"future version": {manifestName: `{"version":99,"dockfly":"9.0"}`, "dockfly.db": "x"},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Stage(bytes.NewReader(makeTarGz(t, entries)), dir); !errors.Is(err, ErrInvalid) {
				t.Errorf("expected ErrInvalid, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, pendingDir)); err == nil {
				t.Error("an invalid backup must not be left staged")
			}
		})
	}
	if _, err := Stage(strings.NewReader("not gzip"), t.TempDir()); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage: %v", err)
	}

	// Malicious paths and files foreign to Dockfly are ignored.
	dir := t.TempDir()
	archive := makeTarGz(t, map[string]string{
		manifestName: `{"version":1}`, "dockfly.db": "x",
		"../../outside.txt": "bad", "sites/../../../etc/x": "bad", "other/file": "foreign",
	})
	if _, err := Stage(bytes.NewReader(archive), dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../outside.txt", "restore-pending/other", "restore-pending/outside.txt"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s should not exist", p)
		}
	}
}

// If the process dies halfway through applying the backup, the next startup
// finishes the job without setting aside what was already restored and
// without overwriting the old data.
func TestApplyPendingResumes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "dockfly.db"), "old")
	write(t, filepath.Join(dir, "secret.key"), "old")
	write(t, filepath.Join(dir, pendingDir, "dockfly.db"), "new")
	write(t, filepath.Join(dir, pendingDir, "secret.key"), "new")

	// First attempt interrupted: it set the old data aside and moved
	// secret.key, but died before moving the database.
	keep := "before-restore-1"
	write(t, filepath.Join(dir, pendingDir, keepMarker), keep)
	os.MkdirAll(filepath.Join(dir, keep), 0o700)
	os.Rename(filepath.Join(dir, "dockfly.db"), filepath.Join(dir, keep, "dockfly.db"))
	os.Rename(filepath.Join(dir, "secret.key"), filepath.Join(dir, keep, "secret.key"))
	os.Rename(filepath.Join(dir, pendingDir, "secret.key"), filepath.Join(dir, "secret.key"))

	applied, kept, err := ApplyPending(dir)
	if err != nil || !applied || filepath.Base(kept) != keep {
		t.Fatalf("ApplyPending: %v %s %v", applied, kept, err)
	}
	for path, want := range map[string]string{
		"dockfly.db": "new", "secret.key": "new",
		keep + "/dockfly.db": "old", keep + "/secret.key": "old",
	} {
		if b, _ := os.ReadFile(filepath.Join(dir, path)); string(b) != want {
			t.Errorf("%s = %q, wanted %q", path, b, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, pendingDir)); !os.IsNotExist(err) {
		t.Error("the pending backup must disappear")
	}
}

type noSnapshot struct{}

func (noSnapshot) Snapshot(context.Context, string) error { return errors.ErrUnsupported }

// With PostgreSQL or MySQL the backup carries the files but not the database.
func TestBackupWithoutLocalDatabase(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "secret.key"), "key")
	var buf bytes.Buffer
	if err := Create(context.Background(), noSnapshot{}, dir, "test", &buf); err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for tr := tar.NewReader(gz); ; {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names[hdr.Name] = true
	}
	if !names["secret.key"] || names["dockfly.db"] {
		t.Fatalf("unexpected contents: %v", names)
	}
}
