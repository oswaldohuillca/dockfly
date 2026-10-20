package gitprovider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type github struct{ *client }

func (g *github) CurrentUser(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	err := g.do(ctx, http.MethodGet, "/user", nil, &u)
	return u.Login, err
}

type ghRepo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
}

func (g *github) ListRepos(ctx context.Context, query string) ([]Repo, error) {
	if g.opts.Installation {
		return g.installationRepos(ctx, query)
	}
	query = strings.ToLower(query)
	var out []Repo
	// Hasta 300 repos, ordenados por actividad reciente: suficiente para
	// elegir en el panel, y la búsqueda filtra sobre ellos.
	for page := 1; page <= 3; page++ {
		var raw []ghRepo
		path := "/user/repos?per_page=100&sort=updated&page=" + strconv.Itoa(page)
		if err := g.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			if query == "" || strings.Contains(strings.ToLower(r.FullName), query) {
				out = append(out, Repo{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private, WebURL: r.HTMLURL})
			}
		}
		if len(raw) < 100 {
			break
		}
	}
	return out, nil
}

// installationRepos lista los repos a los que el usuario dio acceso al
// instalar la GitHub App.
func (g *github) installationRepos(ctx context.Context, query string) ([]Repo, error) {
	query = strings.ToLower(query)
	var out []Repo
	for page := 1; page <= 3; page++ {
		var raw struct {
			Repositories []ghRepo `json:"repositories"`
		}
		path := "/installation/repositories?per_page=100&page=" + strconv.Itoa(page)
		if err := g.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw.Repositories {
			if query == "" || strings.Contains(strings.ToLower(r.FullName), query) {
				out = append(out, Repo{FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private, WebURL: r.HTMLURL})
			}
		}
		if len(raw.Repositories) < 100 {
			break
		}
	}
	return out, nil
}

func (g *github) ListBranches(ctx context.Context, repo string) ([]string, error) {
	var raw []struct {
		Name string `json:"name"`
	}
	if err := g.do(ctx, http.MethodGet, "/repos/"+repoPath(repo)+"/branches?per_page=100", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]string, len(raw))
	for i, b := range raw {
		out[i] = b.Name
	}
	return out, nil
}

func (g *github) LatestCommit(ctx context.Context, repo, branch string) (Commit, error) {
	var raw struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	err := g.do(ctx, http.MethodGet, "/repos/"+repoPath(repo)+"/commits/"+url.PathEscape(branch), nil, &raw)
	return Commit{SHA: raw.SHA, Message: firstLine(raw.Commit.Message)}, err
}

func (g *github) Tarball(ctx context.Context, repo, sha string) (io.ReadCloser, error) {
	return g.download(ctx, "/repos/"+repoPath(repo)+"/tarball/"+url.PathEscape(sha))
}

func (g *github) CreateWebhook(ctx context.Context, repo, hookURL, secret string) (string, error) {
	body := map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": map[string]any{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"},
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := g.do(ctx, http.MethodPost, "/repos/"+repoPath(repo)+"/hooks", body, &out); err != nil {
		return "", err
	}
	return strconv.FormatInt(out.ID, 10), nil
}

func (g *github) DeleteWebhook(ctx context.Context, repo, id string) error {
	err := g.do(ctx, http.MethodDelete, "/repos/"+repoPath(repo)+"/hooks/"+url.PathEscape(id), nil, nil)
	if IsNotFound(err) {
		return nil // ya no existía
	}
	return err
}

// repoPath escapa cada parte de "usuario/repo" por separado.
func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return url.PathEscape(owner) + "/" + url.PathEscape(name)
}

var ErrBadSignature = errors.New("firma del webhook inválida")

// VerifyGitHubSignature comprueba la firma HMAC-SHA256 de un webhook.
func VerifyGitHubSignature(h http.Header, body []byte, secret string) error {
	sig, found := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
	if !found || secret == "" {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return ErrBadSignature
	}
	return nil
}

// ParseGitHubWebhook verifica la firma HMAC-SHA256 y extrae el push.
// Devuelve ok=false para eventos que no son un push a una rama (ping, tags,
// ramas borradas…), que deben responderse con 2xx pero ignorarse.
func ParseGitHubWebhook(h http.Header, body []byte, secret string) (push Push, ok bool, err error) {
	if err := VerifyGitHubSignature(h, body, secret); err != nil {
		return Push{}, false, err
	}
	return ParseGitHubPush(h, body)
}

// ParseGitHubPush extrae el push de un webhook ya verificado.
func ParseGitHubPush(h http.Header, body []byte) (push Push, ok bool, err error) {
	if h.Get("X-GitHub-Event") != "push" {
		return Push{}, false, nil
	}
	var p struct {
		Ref        string `json:"ref"`
		Deleted    bool   `json:"deleted"`
		HeadCommit *struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"head_commit"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Push{}, false, err
	}
	branch, isBranch := strings.CutPrefix(p.Ref, "refs/heads/")
	if !isBranch || p.Deleted || p.HeadCommit == nil {
		return Push{}, false, nil
	}
	return Push{
		Branch:         branch,
		Commit:         Commit{SHA: p.HeadCommit.ID, Message: firstLine(p.HeadCommit.Message)},
		Repo:           p.Repository.FullName,
		InstallationID: p.Installation.ID,
	}, true, nil
}

// ParseGitHubInstallationDeleted indica si el webhook (ya verificado) avisa
// de que alguien desinstaló la GitHub App de su cuenta.
func ParseGitHubInstallationDeleted(h http.Header, body []byte) (installationID int64, ok bool) {
	if h.Get("X-GitHub-Event") != "installation" {
		return 0, false
	}
	var p struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if json.Unmarshal(body, &p) != nil || p.Action != "deleted" {
		return 0, false
	}
	return p.Installation.ID, true
}
