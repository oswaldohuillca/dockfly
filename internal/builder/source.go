package builder

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// buildUID is the unprivileged user the build runs as. In oven/bun it is
// "bun" and in node, "node".
const buildUID = 1000

// repack turns the .tar.gz from GitHub/GitLab into the tar that is copied into
// the container: it drops the root folder ("user-repo-sha/"), puts everything
// under workspace/ and assigns the files to the build user, so that it can
// write (node_modules, dist…) without being root.
func repack(gz io.Reader, w io.Writer, maxBytes int64) error {
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("the downloaded code is not a .tar.gz: %w", err)
	}
	tr := tar.NewReader(zr)
	tw := tar.NewWriter(w)

	// Docker creates the folders missing from the tar as root, and the build
	// would not be able to write in them. That is why every folder is always
	// emitted before its contents, owned by the build user.
	seen := map[string]bool{}
	mkdir := func(dir string) error {
		var missing []string
		for d := dir; d != "." && !seen[d]; d = path.Dir(d) {
			missing = append(missing, d)
		}
		for i := len(missing) - 1; i >= 0; i-- {
			seen[missing[i]] = true
			if err := tw.WriteHeader(&tar.Header{Name: missing[i] + "/", Typeflag: tar.TypeDir, Mode: 0o755,
				Uid: buildUID, Gid: buildUID}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := mkdir("workspace"); err != nil {
		return err
	}
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the code: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir, tar.TypeSymlink:
		default:
			continue // pax global headers, hardlinks, devices…
		}
		_, rest, ok := strings.Cut(hdr.Name, "/")
		rest = strings.TrimPrefix(path.Clean("/"+rest), "/")
		if !ok || rest == "" {
			continue
		}
		total += hdr.Size
		if total > maxBytes {
			return fmt.Errorf("the repository is larger than %d MiB", maxBytes>>20)
		}
		name := "workspace/" + rest
		if hdr.Typeflag == tar.TypeDir {
			// The folder is emitted (just once) by mkdir.
			if err := mkdir(name); err != nil {
				return err
			}
			continue
		}
		if err := mkdir(path.Dir(name)); err != nil {
			return err
		}
		out := &tar.Header{
			Name:     name,
			Typeflag: hdr.Typeflag,
			Linkname: hdr.Linkname,
			Size:     hdr.Size,
			Mode:     hdr.Mode & 0o777,
			ModTime:  hdr.ModTime,
			Uid:      buildUID,
			Gid:      buildUID,
		}
		if err := tw.WriteHeader(out); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.Copy(tw, tr); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

// CappedWriter stops writing once it reaches MaxLogBytes, so that a very
// noisy build (or app deploy) cannot fill up the disk.
type CappedWriter struct {
	w         io.Writer
	remaining int64
	truncated bool
}

// NewCappedWriter caps w at MaxLogBytes.
func NewCappedWriter(w io.Writer) *CappedWriter { return &CappedWriter{w: w, remaining: MaxLogBytes} }

// PruneLogs deletes the build and deploy logs in dir older than maxAge. They
// weigh up to 5 MiB each and used to be kept forever.
func PruneLogs(dir string, maxAge time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".log") && time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (c *CappedWriter) Write(p []byte) (int, error) {
	if c.remaining <= 0 {
		if !c.truncated {
			c.truncated = true
			io.WriteString(c.w, "\n… log truncated: maximum size reached\n")
		}
		return len(p), nil
	}
	n := int64(len(p))
	if n > c.remaining {
		p = p[:c.remaining]
	}
	c.remaining -= int64(len(p))
	if _, err := c.w.Write(p); err != nil {
		return 0, err
	}
	return int(n), nil
}

// shellQuote wraps s in single quotes for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// script builds the container command: every step is announced in the log and
// the first one that fails stops the build.
func script(install, build string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, cmd := range []string{install, build} {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		fmt.Fprintf(&b, "printf '\\n$ %%s\\n' %s\n%s\n", shellQuote(cmd), cmd)
	}
	return b.String()
}
