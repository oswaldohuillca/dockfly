package gitprovider

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type gitlab struct{ *client }

// project codifica la ruta "grupo/proyecto" como ID de proyecto ("grupo%2Fproyecto").
func project(repo string) string { return url.PathEscape(repo) }

func (g *gitlab) CurrentUser(ctx context.Context) (string, error) {
	var u struct {
		Username string `json:"username"`
	}
	err := g.do(ctx, http.MethodGet, "/user", nil, &u)
	return u.Username, err
}

func (g *gitlab) ListRepos(ctx context.Context, query string) ([]Repo, error) {
	var raw []struct {
		PathWithNamespace string `json:"path_with_namespace"`
		DefaultBranch     string `json:"default_branch"`
		Visibility        string `json:"visibility"`
		WebURL            string `json:"web_url"`
	}
	q := url.Values{
		"membership": {"true"},
		"simple":     {"true"},
		"order_by":   {"last_activity_at"},
		"per_page":   {"100"},
	}
	if query != "" {
		q.Set("search", query)
	}
	if err := g.do(ctx, http.MethodGet, "/projects?"+q.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Repo, len(raw))
	for i, r := range raw {
		out[i] = Repo{FullName: r.PathWithNamespace, DefaultBranch: r.DefaultBranch, Private: r.Visibility != "public", WebURL: r.WebURL}
	}
	return out, nil
}

func (g *gitlab) ListBranches(ctx context.Context, repo string) ([]string, error) {
	var raw []struct {
		Name string `json:"name"`
	}
	if err := g.do(ctx, http.MethodGet, "/projects/"+project(repo)+"/repository/branches?per_page=100", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]string, len(raw))
	for i, b := range raw {
		out[i] = b.Name
	}
	return out, nil
}

func (g *gitlab) LatestCommit(ctx context.Context, repo, branch string) (Commit, error) {
	var raw struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	err := g.do(ctx, http.MethodGet, "/projects/"+project(repo)+"/repository/commits/"+url.PathEscape(branch), nil, &raw)
	return Commit{SHA: raw.ID, Message: firstLine(raw.Message)}, err
}

func (g *gitlab) SetStatus(ctx context.Context, repo, sha string, st Status) error {
	state := st.State
	if state == StateFailure {
		state = "failed"
	} else if state == StatePending {
		state = "running"
	}
	body := map[string]string{"state": state, "name": st.Name, "description": truncate(st.Description, 255)}
	if st.URL != "" {
		body["target_url"] = st.URL
	}
	return g.do(ctx, http.MethodPost, "/projects/"+project(repo)+"/statuses/"+url.PathEscape(sha), body, nil)
}

func (g *gitlab) Tarball(ctx context.Context, repo, sha string) (io.ReadCloser, error) {
	return g.download(ctx, "/projects/"+project(repo)+"/repository/archive.tar.gz?sha="+url.QueryEscape(sha))
}

func (g *gitlab) CreateWebhook(ctx context.Context, repo, hookURL, secret string) (string, error) {
	body := map[string]any{
		"url":                     hookURL,
		"token":                   secret,
		"push_events":             true,
		"enable_ssl_verification": true,
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := g.do(ctx, http.MethodPost, "/projects/"+project(repo)+"/hooks", body, &out); err != nil {
		return "", err
	}
	return strconv.FormatInt(out.ID, 10), nil
}

func (g *gitlab) DeleteWebhook(ctx context.Context, repo, id string) error {
	err := g.do(ctx, http.MethodDelete, "/projects/"+project(repo)+"/hooks/"+url.PathEscape(id), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ParseGitLabWebhook verifica el token secreto y extrae el push. Como en
// GitHub, ok=false indica un evento que hay que ignorar.
func ParseGitLabWebhook(h http.Header, body []byte, secret string) (push Push, ok bool, err error) {
	token := h.Get("X-Gitlab-Token")
	if secret == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return Push{}, false, ErrBadSignature
	}
	if h.Get("X-Gitlab-Event") != "Push Hook" {
		return Push{}, false, nil
	}
	var p struct {
		Ref         string  `json:"ref"`
		CheckoutSHA *string `json:"checkout_sha"`
		Commits     []struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Push{}, false, err
	}
	branch, isBranch := strings.CutPrefix(p.Ref, "refs/heads/")
	if !isBranch {
		return Push{}, false, nil
	}
	// checkout_sha es null cuando se borra la rama.
	if p.CheckoutSHA == nil || *p.CheckoutSHA == "" {
		return Push{Branch: branch, Deleted: true}, true, nil
	}
	c := Commit{SHA: *p.CheckoutSHA}
	for _, cm := range p.Commits {
		if cm.ID == c.SHA {
			c.Message = firstLine(cm.Message)
		}
	}
	return Push{Branch: branch, Commit: c}, true, nil
}
