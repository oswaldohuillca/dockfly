// Package gitprovider habla con GitHub y GitLab: listar repositorios,
// descargar el código de un commit y gestionar los webhooks de push.
package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	GitHub = "github"
	GitLab = "gitlab"
)

type Repo struct {
	FullName      string `json:"full_name"` // "usuario/repo" o "grupo/subgrupo/proyecto"
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	WebURL        string `json:"web_url"`
}

type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
}

// Push es un evento de push recibido por webhook.
type Push struct {
	Branch string
	Commit Commit
}

type Provider interface {
	// CurrentUser valida el token y devuelve el nombre de usuario.
	CurrentUser(ctx context.Context) (string, error)
	ListRepos(ctx context.Context, query string) ([]Repo, error)
	ListBranches(ctx context.Context, repo string) ([]string, error)
	LatestCommit(ctx context.Context, repo, branch string) (Commit, error)
	// Tarball descarga el código del commit como .tar.gz. El contenido va
	// dentro de una única carpeta raíz, que el llamador debe descartar.
	Tarball(ctx context.Context, repo, sha string) (io.ReadCloser, error)
	CreateWebhook(ctx context.Context, repo, hookURL, secret string) (id string, err error)
	DeleteWebhook(ctx context.Context, repo, id string) error
}

// DefaultBaseURL es la URL de la API pública de cada proveedor.
func DefaultBaseURL(provider string) string {
	if provider == GitLab {
		return "https://gitlab.com/api/v4"
	}
	return "https://api.github.com"
}

// NormalizeBaseURL acepta la URL de la instancia o la de su API. Para
// GitLab propio basta con "https://gitlab.miempresa.com"; para GitHub
// Enterprise, "https://github.miempresa.com".
func NormalizeBaseURL(provider, raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return DefaultBaseURL(provider), nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("URL inválida: %q", raw)
	}
	switch {
	case provider == GitLab && !strings.HasSuffix(u.Path, "/api/v4"):
		u.Path += "/api/v4"
	case provider == GitHub && u.Host != "api.github.com" && !strings.HasSuffix(u.Path, "/api/v3"):
		u.Path += "/api/v3" // GitHub Enterprise Server
	}
	return u.String(), nil
}

func New(provider, baseURL, token string) (Provider, error) {
	c := &client{base: strings.TrimRight(baseURL, "/"), token: token, http: apiHTTP, provider: provider}
	switch provider {
	case GitHub:
		return &github{c}, nil
	case GitLab:
		return &gitlab{c}, nil
	}
	return nil, fmt.Errorf("proveedor desconocido: %q", provider)
}

var (
	apiHTTP = &http.Client{Timeout: 30 * time.Second}
	// Las descargas de código pueden tardar: su límite lo pone el contexto.
	downloadHTTP = &http.Client{}
)

// APIError es un error devuelto por GitHub o GitLab.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "token inválido o caducado"
	case http.StatusForbidden:
		return "el token no tiene permisos suficientes: " + e.Message
	case http.StatusNotFound:
		return "no encontrado, o el token no tiene acceso"
	}
	return fmt.Sprintf("error %d: %s", e.Status, e.Message)
}

func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

type client struct {
	base     string
	token    string
	provider string
	http     *http.Client
}

func (c *client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = strings.NewReader(string(b))
	}
	// La ruta puede llevar "%2F" (IDs de proyecto de GitLab): NewRequest la
	// conserva tal cual en RawPath.
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "Dockfly")
	if c.provider == GitLab {
		req.Header.Set("PRIVATE-TOKEN", c.token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	return req, nil
}

// do ejecuta una petición a la API y decodifica la respuesta JSON en out.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out)
}

// download pide un archivo binario (el tarball del código).
func (c *client) download(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	// GitHub redirige a codeload.github.com con un token temporal en la URL;
	// Go no reenvía la cabecera Authorization a otro dominio, así que el
	// token real no sale de la API.
	resp, err := downloadHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode < 300 {
		return nil
	}
	var e struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e)
	msg := e.Message
	if msg == "" {
		msg = e.Error
	}
	return &APIError{Status: resp.StatusCode, Message: msg}
}

// firstLine devuelve la primera línea de un mensaje de commit.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
