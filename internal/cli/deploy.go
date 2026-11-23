// Package cli implements the subcommands used from outside the server (a
// laptop, continuous integration) against the Dockfly API.
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

const usage = `usage: dockfly deploy <directory> --site <name> [--url URL] [--token TOKEN]

Publishes a directory (dist/, for example) as a new deployment of the site.

  --site   name (or id) of the site
  --url    panel address (or DOCKFLY_URL), e.g. https://dockfly.mydomain.com
  --token  API token (or DOCKFLY_TOKEN); create one in My account → API tokens
`

// Deploy implements "dockfly deploy". It returns the exit code.
func Deploy(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	site := fl.String("site", "", "")
	base := fl.String("url", os.Getenv("DOCKFLY_URL"), "")
	token := fl.String("token", os.Getenv("DOCKFLY_TOKEN"), "")
	// The directory may come before or after the options.
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

// findSite looks the site up by name or by id.
func (c *client) findSite(nameOrID string) (*site, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+"/api/sites", nil)
	if err != nil {
		return nil, err
	}
	var list []site
	if err := c.do(req, &list); err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", c.base, err)
	}
	id, _ := strconv.ParseInt(nameOrID, 10, 64)
	for i := range list {
		if list[i].Name == nameOrID || list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("there is no site called %q", nameOrID)
}

// skip are the files that must never be published.
func skip(name string) bool {
	return name == ".git" || name == ".DS_Store" || name == "Thumbs.db"
}

func (c *client) deploy(dir, siteName string, stdout io.Writer) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	s, err := c.findSite(siteName)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "▶ Uploading %s to %s…\n", dir, s.Name)

	// The files are streamed: no temporary .zip needed.
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
				return nil // directories, symlinks…
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
	fmt.Fprintf(stdout, "✔ Deployment #%d published: %d files (%s)\n", dep.ID, dep.FileCount, humanBytes(dep.TotalBytes))
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
