// Package pages despliega y sirve sitios estáticos, al estilo de Cloudflare Pages.
package pages

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits acota lo que puede ocupar un despliegue una vez descomprimido.
type Limits struct {
	MaxFiles int
	MaxBytes int64
}

var DefaultLimits = Limits{MaxFiles: 20_000, MaxBytes: 1 << 30} // 1 GiB

// ErrInvalid agrupa los errores causados por el contenido subido (y no por
// el servidor): el API los devuelve como 400.
var ErrInvalid = errors.New("despliegue inválido")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Staging es un despliegue en preparación. Los archivos se escriben en un
// directorio temporal y solo se publican (Storage.Commit) si todo fue bien.
type Staging struct {
	Dir    string
	root   *os.Root
	limits Limits
	files  int
	bytes  int64
}

// Add escribe un archivo en el despliegue. name es la ruta relativa dentro
// del sitio, por ejemplo "assets/app.js".
func (s *Staging) Add(name string, r io.Reader) error {
	rel, ok := cleanPath(name)
	if !ok {
		return nil // archivos ocultos del sistema (.DS_Store, __MACOSX…): se ignoran
	}
	if s.files >= s.limits.MaxFiles {
		return invalid("más de %d archivos", s.limits.MaxFiles)
	}
	if dir := path.Dir(rel); dir != "." {
		if err := s.root.MkdirAll(dir, 0o755); err != nil {
			return invalid("ruta %q: %v", rel, err)
		}
	}
	// os.Root impide escribir fuera del directorio del despliegue, aunque la
	// ruta o un symlink intenten escapar.
	f, err := s.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return invalid("archivo duplicado: %s", rel)
	}
	if err != nil {
		return invalid("ruta %q: %v", rel, err)
	}
	// Leemos como máximo un byte más de lo permitido para detectar el exceso
	// sin descomprimir el resto (protección frente a zip bombs).
	remaining := s.limits.MaxBytes - s.bytes
	n, err := io.Copy(f, io.LimitReader(r, remaining+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("escribiendo %s: %w", rel, err)
	}
	if n > remaining {
		return invalid("el despliegue supera %d MiB descomprimido", s.limits.MaxBytes>>20)
	}
	s.files++
	s.bytes += n
	return nil
}

// AddZip extrae un archivo .zip completo en el despliegue.
func (s *Staging) AddZip(ra io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return invalid("no es un archivo .zip válido")
	}
	for _, zf := range zr.File {
		// Solo archivos regulares: los directorios se crean al escribir y los
		// symlinks se descartan.
		if !zf.Mode().IsRegular() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return invalid("%s: %v", zf.Name, err)
		}
		err = s.Add(zf.Name, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// AddTar extrae un tar en el despliegue. Descarta el primer componente de
// cada ruta (la carpeta raíz del tar) y, si prefix no está vacío, solo
// incluye lo que hay dentro de prefix. Por ejemplo, con prefix "dist":
//
//	web-abc123/dist/index.html  →  index.html
//	web-abc123/src/main.ts      →  (ignorado)
func (s *Staging) AddTar(r io.Reader, prefix string) error {
	prefix = strings.Trim(path.Clean("/"+prefix), "/")
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return invalid("tar inválido: %v", err)
		}
		// Solo archivos regulares: ni symlinks ni dispositivos.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		_, rest, ok := strings.Cut(strings.TrimPrefix(hdr.Name, "./"), "/")
		if !ok {
			continue
		}
		if prefix != "" {
			if rest, ok = strings.CutPrefix(rest, prefix+"/"); !ok {
				continue
			}
		}
		if err := s.Add(rest, tr); err != nil {
			return err
		}
	}
}

// Finish valida el resultado. Si todo el contenido quedó dentro de una única
// carpeta (p. ej. un zip de "dist/"), la sube un nivel para que index.html
// quede en la raíz.
func (s *Staging) Finish() (files int, bytes int64, err error) {
	if s.files == 0 {
		return 0, 0, invalid("no se recibió ningún archivo")
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return 0, 0, err
	}
	if len(entries) == 1 && entries[0].IsDir() && entries[0].Name() != ".well-known" {
		if err := hoist(s.Dir, entries[0].Name()); err != nil {
			return 0, 0, err
		}
	}
	return s.files, s.bytes, nil
}

func (s *Staging) close() { s.root.Close() }

// hoist mueve el contenido de dir/sub a dir. La carpeta se aparta primero
// fuera de dir, para que ningún archivo del usuario pueda chocar con ella.
func hoist(dir, sub string) error {
	tmp := dir + ".hoist" // hermano de dir: también lo limpia CleanStaging
	if err := os.Rename(filepath.Join(dir, sub), tmp); err != nil {
		return err
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(tmp, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(tmp)
}

// cleanPath normaliza una ruta subida a una ruta relativa segura. Devuelve
// ok=false para archivos que no deben publicarse.
func cleanPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/") // zips creados en Windows
	if strings.ContainsRune(name, 0) {
		return "", false
	}
	// Anclar en "/" antes de limpiar elimina cualquier "../" inicial.
	rel := strings.TrimPrefix(path.Clean("/"+name), "/")
	if rel == "" {
		return "", false
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "__MACOSX" || seg == ".DS_Store" || seg == "Thumbs.db" || seg == ".git" {
			return "", false
		}
	}
	return rel, true
}
