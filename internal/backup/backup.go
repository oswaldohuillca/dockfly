// Package backup exporta y restaura todo el estado de Dockfly: base de
// datos, claves, sitios publicados y certificados de Let's Encrypt.
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

// Version del formato de la copia. Se incrementa si cambia la estructura.
const Version = 1

const (
	manifestName = "dockfly-backup.json"
	pendingDir   = "restore-pending" // copia validada, se aplica al arrancar
	stagingDir   = ".restore-staging"
	// Una copia de 10 GiB descomprimida es sospechosa: protege de tar bombs.
	maxRestoreBytes = 10 << 30
)

// Manifest describe una copia.
type Manifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Dockfly   string    `json:"dockfly"` // versión de Dockfly que la generó
}

// Archivos y carpetas de /data que forman parte de la copia. Lo demás
// (logs de builds, configuración de Traefik, temporales) se regenera.
var (
	files = []string{"secret.key", "encryption.key", "traefik/acme.json"}
	dirs  = []string{"sites"}
)

// Snapshotter crea una copia consistente de la base de datos.
type Snapshotter interface {
	Snapshot(ctx context.Context, path string) error
}

// Create escribe la copia como .tar.gz en w.
func Create(ctx context.Context, db Snapshotter, dataDir, dockflyVersion string, w io.Writer) error {
	snap, err := os.CreateTemp(dataDir, ".backup-*.db")
	if err != nil {
		return err
	}
	snapPath := snap.Name()
	snap.Close()
	os.Remove(snapPath) // VACUUM INTO exige que el destino no exista
	defer os.Remove(snapPath)
	if err := db.Snapshot(ctx, snapPath); err != nil {
		return fmt.Errorf("copiando la base de datos: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	manifest, _ := json.MarshalIndent(Manifest{Version: Version, CreatedAt: time.Now().UTC(), Dockfly: dockflyVersion}, "", "  ")
	if err := writeBytes(tw, manifestName, manifest, 0o644); err != nil {
		return err
	}
	if err := writeFile(tw, snapPath, "dockfly.db"); err != nil {
		return err
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
			// Las subidas a medias no forman parte del estado.
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

var ErrInvalid = errors.New("copia de seguridad inválida")

// Stage valida una copia y la deja preparada en dataDir/restore-pending. No
// toca los datos en uso: se aplica con ApplyPending al arrancar.
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
		return nil, fmt.Errorf("%w: no es un .tar.gz", ErrInvalid)
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
			continue // solo restauramos lo que una copia de Dockfly puede contener
		}
		total += hdr.Size
		if total > maxRestoreBytes {
			return nil, fmt.Errorf("%w: demasiado grande", ErrInvalid)
		}
		if name == manifestName {
			manifest = new(Manifest)
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(manifest); err != nil {
				return nil, fmt.Errorf("%w: manifiesto ilegible", ErrInvalid)
			}
			continue
		}
		hasDB = hasDB || name == "dockfly.db"
		if dir := path.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		// os.Root: ninguna ruta del tar puede escribir fuera de la carpeta.
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
		return nil, fmt.Errorf("%w: no es una copia de Dockfly (falta %s)", ErrInvalid, manifestName)
	case manifest.Version > Version:
		return nil, fmt.Errorf("%w: la copia es de una versión más nueva de Dockfly (%s); actualiza antes de restaurarla", ErrInvalid, manifest.Dockfly)
	case !hasDB:
		return nil, fmt.Errorf("%w: falta la base de datos", ErrInvalid)
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

// ApplyPending aplica una copia preparada por Stage. Debe llamarse al
// arrancar, antes de abrir la base de datos. Los datos que había se
// conservan en dataDir/before-restore-<fecha>, por si hubiera que volver.
func ApplyPending(dataDir string) (applied bool, keptAt string, err error) {
	pending := filepath.Join(dataDir, pendingDir)
	if _, err := os.Stat(pending); errors.Is(err, fs.ErrNotExist) {
		return false, "", nil
	}
	keep := filepath.Join(dataDir, "before-restore-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Join(keep, "traefik"), 0o700); err != nil {
		return false, "", err
	}
	// La base de datos en uso, con su WAL, y todo lo que la copia reemplaza.
	current := append([]string{"dockfly.db", "dockfly.db-wal", "dockfly.db-shm"}, files...)
	current = append(current, dirs...)
	for _, name := range current {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, filepath.Join(keep, name)); err != nil {
				return false, "", fmt.Errorf("apartando %s: %w", name, err)
			}
		}
	}
	entries, err := os.ReadDir(pending)
	if err != nil {
		return false, "", err
	}
	for _, e := range entries {
		dst := filepath.Join(dataDir, e.Name())
		if e.IsDir() {
			// traefik/ ya existe (lo crea Dockfly): movemos su contenido.
			if err := mergeDir(filepath.Join(pending, e.Name()), dst); err != nil {
				return false, "", err
			}
			continue
		}
		if err := os.Rename(filepath.Join(pending, e.Name()), dst); err != nil {
			return false, "", err
		}
	}
	return true, keep, os.RemoveAll(pending)
}

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
