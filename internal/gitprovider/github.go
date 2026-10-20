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

func (g *github) ListRepos(ctx context.Context, query string) ([]Repo, error) {
	query = strings.ToLower(query)
	var out []Repo
	// Hasta 300 repos, ordenados por actividad reciente: suficiente para
	// elegir en el panel, y la búsqueda filtra sobre ellos.
	for page := 1; page <= 3; page++ {
		var raw []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
			HTMLURL       string `json:"html_url"`
		}
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

// ParseGitHubWebhook verifica la firma HMAC-SHA256 y extrae el push.
// Devuelve ok=false para eventos que no son un push a una rama (ping, tags,
// ramas borradas…), que deben responderse con 2xx pero ignorarse.
func ParseGitHubWebhook(h http.Header, body []byte, secret string) (push Push, ok bool, err error) {
	sig, found := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
	if !found || secret == "" {
		return Push{}, false, ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return Push{}, false, ErrBadSignature
	}

	if h.Get("X-GitHub-Event") != "push" {
		return Push{}, false, nil
	}
	var p struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		HeadCommit *struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"head_commit"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Push{}, false, err
	}
	branch, isBranch := strings.CutPrefix(p.Ref, "refs/heads/")
	if !isBranch || p.Deleted || p.HeadCommit == nil {
		return Push{}, false, nil
	}
	return Push{Branch: branch, Commit: Commit{SHA: p.HeadCommit.ID, Message: firstLine(p.HeadCommit.Message)}}, true, nil
}
