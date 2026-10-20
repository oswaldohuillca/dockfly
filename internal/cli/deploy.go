// Package cli implementa los subcomandos que se usan desde fuera del
// servidor (el portátil, la integración continua) contra la API de Dockfly.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const usage = `uso: dockfly deploy <carpeta> --site <nombre> [--url URL] [--token TOKEN]

Publica una carpeta (p. ej. dist/) como un despliegue nuevo del sitio.

  --site   nombre (o id) del sitio
  --url    dirección del panel (o DOCKFLY_URL), p. ej. https://dockfly.midominio.com
  --token  token de API (o DOCKFLY_TOKEN); créalo en Mi cuenta → Tokens de API
`

// Deploy implementa "dockfly deploy". Devuelve el código de salida.
func Deploy(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	site := fl.String("site", "", "")
	base := fl.String("url", os.Getenv("DOCKFLY_URL"), "")
	token := fl.String("token", os.Getenv("DOCKFLY_TOKEN"), "")
	// La carpeta puede ir antes o después de las opciones.
	var dir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	if err := fl.Parse(args); err != nil {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if dir == "" && fl.NArg() > 0 {
		dir = fl.Arg(0)
	}
	if dir == "" || *site == "" || *base == "" || *token == "" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	c := &client{base: strings.TrimRight(*base, "/"), token: *token, http: &http.Client{Timeout: 30 * time.Minute}}
	if err := c.deploy(dir, *site, stdout); err != nil {
		fmt.Fprintln(stderr, "✘", err)
		return 1
	}
	return 0
}

type client struct {
	base, token string
	http        *http.Client
}

type site struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	SSL     bool     `json:"ssl"`
}

type deployment struct {
	ID         int64 `json:"id"`
	FileCount  int   `json:"file_count"`
	TotalBytes int64 `json:"total_bytes"`
}

func (c *client) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "dockfly-cli")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// findSite busca el sitio por nombre o por id.
func (c *client) findSite(nameOrID string) (*site, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+"/api/sites", nil)
	if err != nil {
		return nil, err
	}
	var list []site
	if err := c.do(req, &list); err != nil {
		return nil, fmt.Errorf("conectando con %s: %w", c.base, err)
	}
	id, _ := strconv.ParseInt(nameOrID, 10, 64)
	for i := range list {
		if list[i].Name == nameOrID || list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("no hay ningún sitio llamado %q", nameOrID)
}

// skip son archivos que nunca deben publicarse.
func skip(name string) bool {
	return name == ".git" || name == ".DS_Store" || name == "Thumbs.db"
}

func (c *client) deploy(dir, siteName string, stdout io.Writer) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s no es una carpeta", dir)
	}
	s, err := c.findSite(siteName)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "▶ Subiendo %s a %s…\n", dir, s.Name)

	// Los archivos se envían en streaming: no hace falta un .zip temporal.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if skip(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // carpetas, enlaces simbólicos…
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			part, err := mw.CreateFormFile("file:"+filepath.ToSlash(rel), filepath.Base(rel))
			if err != nil {
				return err
			}
			_, err = io.Copy(part, f)
			return err
		})
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/sites/%d/deployments", c.base, s.ID), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Dockfly-Upload", "1")
	var dep deployment
	if err := c.do(req, &dep); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "✔ Despliegue #%d publicado: %d archivos (%s)\n", dep.ID, dep.FileCount, humanBytes(dep.TotalBytes))
	if len(s.Domains) > 0 && !strings.HasPrefix(s.Domains[0], "*.") {
		scheme := "http"
		if s.SSL {
			scheme = "https"
		}
		fmt.Fprintf(stdout, "  %s://%s\n", scheme, s.Domains[0])
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
