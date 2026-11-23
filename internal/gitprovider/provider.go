// Package gitprovider talks to GitHub and GitLab: listing repositories,
// downloading the code of a commit and managing the push webhooks.
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
	"unicode/utf8"

	"github.com/dockfly/dockfly/internal/egress"
)

const (
	GitHub = "github"
	GitLab = "gitlab"
)

type Repo struct {
	FullName      string `json:"full_name"` // "user/repo" or "group/subgroup/project"
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	WebURL        string `json:"web_url"`
}

type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
}

// Push is a push event received through a webhook.
type Push struct {
	Branch string
	Commit Commit
	// Deleted means the branch was deleted (Commit is empty).
	Deleted bool
	// Only in GitHub App webhooks, which are shared by all the repos.
	Repo           string
	InstallationID int64
}

type Provider interface {
	// CurrentUser validates the token and returns the user name.
	CurrentUser(ctx context.Context) (string, error)
	ListRepos(ctx context.Context, query string) ([]Repo, error)
	ListBranches(ctx context.Context, repo string) ([]string, error)
	LatestCommit(ctx context.Context, repo, branch string) (Commit, error)
	// Tarball downloads the code of the commit as a .tar.gz. The contents sit
	// inside a single root directory, which the caller is expected to strip.
	Tarball(ctx context.Context, repo, sha string) (io.ReadCloser, error)
	CreateWebhook(ctx context.Context, repo, hookURL, secret string) (id string, err error)
	DeleteWebhook(ctx context.Context, repo, id string) error
	// SetStatus marks the commit with the state of the build (✓/✗ on the
	// commit and on its pull requests).
	SetStatus(ctx context.Context, repo, sha string, st Status) error
}

// States of a commit.
const (
	StatePending = "pending"
	StateSuccess = "success"
	StateFailure = "failure"
)

// Status is the state of a build as it is shown on the commit.
type Status struct {
	State       string
	Name        string // identifies the check ("dockfly/my-site")
	Description string
	URL         string // where the status link points to
}

// truncate cuts s down to n bytes without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// DefaultBaseURL is the URL of each provider's public API.
func DefaultBaseURL(provider string) string {
	if provider == GitLab {
		return "https://gitlab.com/api/v4"
	}
	return "https://api.github.com"
}

// NormalizeBaseURL accepts either the URL of the instance or that of its
// API. For a self-hosted GitLab, "https://gitlab.mycompany.com" is enough;
// for GitHub Enterprise, "https://github.mycompany.com".
func NormalizeBaseURL(provider, raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return DefaultBaseURL(provider), nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("invalid URL: %q", raw)
	}
	switch {
	case provider == GitLab && !strings.HasSuffix(u.Path, "/api/v4"):
		u.Path += "/api/v4"
	case provider == GitHub && u.Host != "api.github.com" && !strings.HasSuffix(u.Path, "/api/v3"):
		u.Path += "/api/v3" // GitHub Enterprise Server
	}
	return u.String(), nil
}

// Options states where the token comes from.
type Options struct {
	// Installation: installation token of a GitHub App. It only sees the
	// repositories the user picked when installing it.
	Installation bool
	// OAuth: GitLab OAuth token (sent in Authorization: Bearer instead of
	// PRIVATE-TOKEN).
	OAuth bool
}

// New creates a client with a personal access token.
func New(provider, baseURL, token string) (Provider, error) {
	return NewWithOptions(provider, baseURL, token, Options{})
}

func NewWithOptions(provider, baseURL, token string, opts Options) (Provider, error) {
	c := &client{base: strings.TrimRight(baseURL, "/"), token: token, http: apiHTTP, provider: provider, opts: opts}
	switch provider {
	case GitHub:
		return &github{c}, nil
	case GitLab:
		return &gitlab{c}, nil
	}
	return nil, fmt.Errorf("unknown provider: %q", provider)
}

var (
	// Transport that rejects link-local addresses (cloud metadata) to stop
	// SSRF through a malicious base_url, while still allowing loopback and
	// private addresses for self-hosted GitHub/GitLab.
	apiHTTP = &http.Client{Timeout: 30 * time.Second, Transport: egress.SafeTransport()}
	// Code downloads can take a while: the context sets their limit.
	downloadHTTP = &http.Client{Transport: egress.SafeTransport()}
)

// APIError is an error returned by GitHub or GitLab.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "invalid or expired token"
	case http.StatusForbidden:
		return "the token does not have enough permissions: " + e.Message
	case http.StatusNotFound:
		return "not found, or the token has no access"
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
	opts     Options
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
	// The path may contain "%2F" (GitLab project IDs): NewRequest keeps it
	// verbatim in RawPath.
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "Dockfly")
	if c.provider == GitLab && !c.opts.OAuth {
		req.Header.Set("PRIVATE-TOKEN", c.token)
	} else if c.provider == GitLab {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	return req, nil
}

// do runs a request against the API and decodes the JSON response into out.
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

// download asks for a binary file (the tarball with the code).
func (c *client) download(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	// GitHub redirects to codeload.github.com with a temporary token in the
	// URL; Go does not forward the Authorization header to another domain, so
	// the real token never leaves the API.
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

// firstLine returns the first line of a commit message.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
