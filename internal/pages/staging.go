// Package pages deploys and serves static sites, in the style of Cloudflare Pages.
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

// Limits bounds how much a deployment may take up once uncompressed.
type Limits struct {
	MaxFiles int
	MaxBytes int64
}

var DefaultLimits = Limits{MaxFiles: 20_000, MaxBytes: 1 << 30} // 1 GiB

// ErrInvalid groups the errors caused by the uploaded content (and not by the
// server): the API returns them as 400.
var ErrInvalid = errors.New("invalid deployment")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Staging is a deployment being prepared. The files are written to a
// temporary directory and only published (Storage.Commit) if all went well.
type Staging struct {
	Dir    string
	root   *os.Root
	limits Limits
	files  int
	bytes  int64
}

// Add writes a file into the deployment. name is the path relative to the
// site, for example "assets/app.js".
func (s *Staging) Add(name string, r io.Reader) error {
	rel, ok := cleanPath(name)
	if !ok {
		return nil // hidden system files (.DS_Store, __MACOSX…): ignored
	}
	if s.files >= s.limits.MaxFiles {
		return invalid("more than %d files", s.limits.MaxFiles)
	}
	if dir := path.Dir(rel); dir != "." {
		if err := s.root.MkdirAll(dir, 0o755); err != nil {
			return invalid("path %q: %v", rel, err)
		}
	}
	// os.Root prevents writing outside the deployment directory, even if the
	// path or a symlink tries to escape.
	f, err := s.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return invalid("duplicate file: %s", rel)
	}
	if err != nil {
		return invalid("path %q: %v", rel, err)
	}
	// Read at most one byte more than allowed, to spot the excess without
	// decompressing the rest (protection against zip bombs).
	remaining := s.limits.MaxBytes - s.bytes
	n, err := io.Copy(f, io.LimitReader(r, remaining+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if n > remaining {
		return invalid("the deployment is larger than %d MiB uncompressed", s.limits.MaxBytes>>20)
	}
	s.files++
	s.bytes += n
	return nil
}

// AddZip extracts a whole .zip file into the deployment.
func (s *Staging) AddZip(ra io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return invalid("not a valid .zip file")
	}
	for _, zf := range zr.File {
		// Regular files only: directories are created on write and symlinks
		// are discarded.
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

// AddTar extracts a tar into the deployment. It drops the first component of
// every path (the tar's root folder) and, if prefix is not empty, only
// includes what is inside prefix. For example, with prefix "dist":
//
//	web-abc123/dist/index.html  →  index.html
//	web-abc123/src/main.ts      →  (ignored)
func (s *Staging) AddTar(r io.Reader, prefix string) error {
	prefix = strings.Trim(path.Clean("/"+prefix), "/")
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return invalid("invalid tar: %v", err)
		}
		// Regular files only: no symlinks, no devices.
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

// Finish validates the result. If all the content ended up inside a single
// folder (e.g. a zip of "dist/"), it moves it up one level so that index.html
// sits at the root.
func (s *Staging) Finish() (files int, bytes int64, err error) {
	if s.files == 0 {
		return 0, 0, invalid("no file was received")
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

// hoist moves the contents of dir/sub into dir. The folder is first set aside
// outside dir, so that no user file can collide with it.
func hoist(dir, sub string) error {
	tmp := dir + ".hoist" // sibling of dir: CleanStaging cleans it up too
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

// cleanPath normalizes an uploaded path into a safe relative path. It returns
// ok=false for files that must not be published.
func cleanPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/") // zips created on Windows
	if strings.ContainsRune(name, 0) {
		return "", false
	}
	// Anchoring at "/" before cleaning strips any leading "../".
	rel := strings.TrimPrefix(path.Clean("/"+name), "/")
	if rel == "" || len(rel) > 1024 || strings.Count(rel, "/") > 64 {
		return "", false
	}
	for seg := range strings.SplitSeq(rel, "/") {
		if len(seg) > 255 || seg == "__MACOSX" || seg == ".DS_Store" || seg == "Thumbs.db" || seg == ".git" {
			return "", false
		}
	}
	return rel, true
}
