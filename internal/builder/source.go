package builder

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"
)

// buildUID es el usuario sin privilegios con el que corre el build. En
// oven/bun es "bun" y en node, "node".
const buildUID = 1000

// repack convierte el .tar.gz de GitHub/GitLab en el tar que se copia al
// contenedor: descarta la carpeta raíz ("usuario-repo-sha/"), coloca todo
// bajo workspace/ y asigna los archivos al usuario del build, para que este
// pueda escribir (node_modules, dist…) sin ser root.
func repack(gz io.Reader, w io.Writer, maxBytes int64) error {
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("el código descargado no es un .tar.gz: %w", err)
	}
	tr := tar.NewReader(zr)
	tw := tar.NewWriter(w)

	// Docker crea como root las carpetas que no vienen en el tar, y el build
	// no podría escribir en ellas. Por eso emitimos siempre cada carpeta
	// antes de su contenido, con el usuario del build.
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
			return fmt.Errorf("leyendo el código: %w", err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir, tar.TypeSymlink:
		default:
			continue // cabeceras pax globales, hardlinks, dispositivos…
		}
		_, rest, ok := strings.Cut(hdr.Name, "/")
		rest = strings.TrimPrefix(path.Clean("/"+rest), "/")
		if !ok || rest == "" {
			continue
		}
		total += hdr.Size
		if total > maxBytes {
			return fmt.Errorf("el repositorio supera %d MiB", maxBytes>>20)
		}
		name := "workspace/" + rest
		if hdr.Typeflag == tar.TypeDir {
			// La carpeta se emite (una sola vez) con mkdir.
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

// cappedWriter deja de escribir al llegar a max bytes, para que un build
// muy ruidoso no llene el disco.
type cappedWriter struct {
	w         io.Writer
	remaining int64
	truncated bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.remaining <= 0 {
		if !c.truncated {
			c.truncated = true
			io.WriteString(c.w, "\n… log truncado: se alcanzó el tamaño máximo\n")
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

// shellQuote envuelve s entre comillas simples para sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// script genera el comando del contenedor: cada paso se anuncia en el log y
// el primero que falle detiene el build.
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
