// Package backup exports and restores all of Dockfly's state: database,
// keys, published sites and Let's Encrypt certificates.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Version of the backup format. It is bumped whenever the layout changes.
const Version = 1

const (
	manifestName = "dockfly-backup.json"
	pendingDir   = "restore-pending" // validated backup, applied on startup
	stagingDir   = ".restore-staging"
	// A backup that unpacks to 10 GiB is suspicious: guards against tar bombs.
	maxRestoreBytes = 10 << 30
)

// Manifest describes a backup.
type Manifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Dockfly   string    `json:"dockfly"` // Dockfly version that created it
}

// Files and directories under /data that are part of the backup. Everything
// else (build logs, Traefik configuration, temporary files) is regenerated.
// db-backups holds the database dumps kept locally: without them, the rows in
// the restored database would point at files that do not exist.
var (
	files = []string{"secret.key", "encryption.key", "traefik/acme.json"}
	dirs  = []string{"sites", "db-backups"}
)

// Snapshotter creates a consistent copy of the database.
type Snapshotter interface {
	Snapshot(ctx context.Context, path string) error
}

// Create writes the backup as a .tar.gz to w.
func Create(ctx context.Context, db Snapshotter, dataDir, dockflyVersion string, w io.Writer) error {
	snap, err := os.CreateTemp(dataDir, ".backup-*.db")
	if err != nil {
		return err
	}
	snapPath := snap.Name()
	snap.Close()
	os.Remove(snapPath) // VACUUM INTO requires the destination not to exist
	defer os.Remove(snapPath)
	// With PostgreSQL or MySQL the backup carries only the files (keys, sites,
	// certificates): the database is dumped with its own server's tools.
	hasDB := true
	if err := db.Snapshot(ctx, snapPath); errors.Is(err, errors.ErrUnsupported) {
		hasDB = false
	} else if err != nil {
		return fmt.Errorf("copying the database: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	manifest, _ := json.MarshalIndent(Manifest{Version: Version, CreatedAt: time.Now().UTC(), Dockfly: dockflyVersion}, "", "  ")
	if err := writeBytes(tw, manifestName, manifest, 0o644); err != nil {
		return err
	}
	if hasDB {
		if err := writeFile(tw, snapPath, "dockfly.db"); err != nil {
			return err
		}
	}
	for _, f := range files {
		if err := writeFile(tw, filepath.Join(dataDir, f), f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for _, d := range dirs {
		root := filepath.Join(dataDir, d)
		err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Half-finished uploads are not part of the state.
			if strings.HasPrefix(e.Name(), ".staging-") {
				if e.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !e.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(dataDir, p)
			return writeFile(tw, p, filepath.ToSlash(rel))
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeBytes(tw *tar.Writer, name string, data []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeFile(tw *tar.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: name, Mode: int64(fi.Mode().Perm()), Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

var ErrInvalid = errors.New("invalid backup")

// Stage validates a backup and leaves it ready in dataDir/restore-pending. It
// does not touch the data in use: ApplyPending applies it on startup.
func Stage(r io.Reader, dataDir string) (*Manifest, error) {
	tmp := filepath.Join(dataDir, stagingDir)
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	root, err := os.OpenRoot(tmp)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: not a .tar.gz", ErrInvalid)
	}
	tr := tar.NewReader(gz)
	var manifest *Manifest
	var hasDB bool
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		if !allowed(name) {
			continue // only restore what a Dockfly backup is allowed to contain
		}
		total += hdr.Size
		if total > maxRestoreBytes {
			return nil, fmt.Errorf("%w: too big", ErrInvalid)
		}
		if name == manifestName {
			manifest = new(Manifest)
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(manifest); err != nil {
				return nil, fmt.Errorf("%w: unreadable manifest", ErrInvalid)
			}
			continue
		}
		hasDB = hasDB || name == "dockfly.db"
		if dir := path.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		// os.Root: no path in the tar can write outside the directory.
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fs.FileMode(hdr.Mode).Perm()|0o600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(f, io.LimitReader(tr, hdr.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
	}
	switch {
	case manifest == nil:
		return nil, fmt.Errorf("%w: not a Dockfly backup (%s is missing)", ErrInvalid, manifestName)
	case manifest.Version > Version:
		return nil, fmt.Errorf("%w: the backup comes from a newer version of Dockfly (%s); upgrade before restoring it", ErrInvalid, manifest.Dockfly)
	case !hasDB:
		return nil, fmt.Errorf("%w: the database is missing", ErrInvalid)
	}

	pending := filepath.Join(dataDir, pendingDir)
	os.RemoveAll(pending)
	if err := os.Rename(tmp, pending); err != nil {
		return nil, err
	}
	ok = true
	return manifest, nil
}

func allowed(name string) bool {
	if name == manifestName || name == "dockfly.db" {
		return true
	}
	for _, f := range files {
		if name == f {
			return true
		}
	}
	for _, d := range dirs {
		if strings.HasPrefix(name, d+"/") {
			return true
		}
	}
	return false
}

// ApplyPending applies a backup staged by Stage. It must be called on
// startup, before opening the database. The data that was there is kept in
// dataDir/before-restore-<date>, in case it has to be rolled back.
//
// It can be retried if it is interrupted halfway (OOM, restart): it reuses
// the same backup directory, does not set aside twice what was already set
// aside (which by then would be the restored data) and moves the database last.
func ApplyPending(dataDir string) (applied bool, keptAt string, err error) {
	pending := filepath.Join(dataDir, pendingDir)
	if _, err := os.Stat(pending); errors.Is(err, fs.ErrNotExist) {
		return false, "", nil
	}
	marker := filepath.Join(pending, keepMarker)
	keep := filepath.Join(dataDir, "before-restore-"+time.Now().UTC().Format("20060102-150405"))
	if raw, err := os.ReadFile(marker); err == nil {
		keep = filepath.Join(dataDir, string(raw))
	} else if err := os.WriteFile(marker, []byte(filepath.Base(keep)), 0o600); err != nil {
		return false, "", err
	}
	if err := os.MkdirAll(filepath.Join(keep, "traefik"), 0o700); err != nil {
		return false, "", err
	}
	// The database in use, with its WAL, and everything the backup replaces.
	current := append([]string{"dockfly.db", "dockfly.db-wal", "dockfly.db-shm"}, files...)
	current = append(current, dirs...)
	for _, name := range current {
		src, dst := filepath.Join(dataDir, name), filepath.Join(keep, name)
		if _, err := os.Stat(dst); err == nil {
			continue // already set aside by an earlier attempt
		}
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return false, "", fmt.Errorf("setting aside %s: %w", name, err)
			}
		}
	}
	entries, err := os.ReadDir(pending)
	if err != nil {
		return false, "", err
	}
	for _, e := range entries {
		// dockfly.db goes last (below): as long as it is still in pending, a
		// restart comes back through here.
		if e.Name() == keepMarker || e.Name() == "dockfly.db" {
			continue
		}
		dst := filepath.Join(dataDir, e.Name())
		if e.IsDir() {
			// traefik/ already exists (Dockfly creates it): move its contents.
			if err := mergeDir(filepath.Join(pending, e.Name()), dst); err != nil {
				return false, "", err
			}
			continue
		}
		if err := os.Rename(filepath.Join(pending, e.Name()), dst); err != nil {
			return false, "", err
		}
	}
	if err := os.Rename(filepath.Join(pending, "dockfly.db"), filepath.Join(dataDir, "dockfly.db")); err != nil &&
		!errors.Is(err, fs.ErrNotExist) {
		return false, "", err
	}
	return true, keep, os.RemoveAll(pending)
}

// keepMarker records, inside pending, which backup directory was chosen.
const keepMarker = ".keep"

func mergeDir(src, dst string) error {
	if _, err := os.Stat(dst); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(src, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := mergeDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := os.Rename(s, d); err != nil {
			return err
		}
	}
	return nil
}
