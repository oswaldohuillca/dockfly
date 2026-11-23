package gitprovider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHub App created through the manifest flow: Dockfly describes the app
// (permissions, webhook, return URLs), GitHub creates it in the user's
// account with a single click and returns its credentials. That way every
// Dockfly installation gets its own app without anyone filling in forms.
//
// https://docs.github.com/apps/sharing-github-apps/registering-a-github-app-from-a-manifest

// GitHubWebURLs returns GitHub's web and API URLs (or those of GitHub
// Enterprise, when enterprise is not empty).
func GitHubWebURLs(enterprise string) (web, api string, err error) {
	enterprise = strings.TrimRight(strings.TrimSpace(enterprise), "/")
	if enterprise == "" {
		return "https://github.com", "https://api.github.com", nil
	}
	u, err := url.Parse(enterprise)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", fmt.Errorf("invalid GitHub Enterprise URL: %q", enterprise)
	}
	u.Path = ""
	return u.String(), u.String() + "/api/v3", nil
}

// Manifest describes the GitHub App that is about to be created.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	HookAttributes     *ManifestHook     `json:"hook_attributes,omitempty"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	SetupOnUpdate      bool              `json:"setup_on_update"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

type ManifestHook struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// NewManifest prepares Dockfly's app. panelURL is the address the browser
// uses to open the panel (the redirects are made by the browser, so even
// localhost works); hookURL is the public URL for the webhooks, or empty if
// Dockfly is not reachable from the internet.
func NewManifest(name, panelURL, hookURL string) Manifest {
	m := Manifest{
		Name:          name,
		URL:           panelURL,
		RedirectURL:   panelURL + "/api/git/github/manifest",
		SetupURL:      panelURL + "/api/git/github/setup",
		SetupOnUpdate: true,
		Public:        false,
		// Read-only on the code: Dockfly never writes to your repos. It only
		// marks the commits with the result of the build.
		DefaultPermissions: map[string]string{"contents": "read", "metadata": "read", "statuses": "write"},
		DefaultEvents:      []string{"push"},
	}
	if hookURL != "" {
		m.HookAttributes = &ManifestHook{URL: hookURL, Active: true}
	}
	return m
}

// ManifestFormURL is where the browser POSTs the manifest. When org is set,
// the app belongs to that organization instead of to the user.
func ManifestFormURL(webURL, org, state string) string {
	path := "/settings/apps/new"
	if org != "" {
		path = "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return webURL + path + "?state=" + url.QueryEscape(state)
}

// InstallURL opens the GitHub screen for picking an account or organization
// and the repositories.
func InstallURL(webURL, slug, state string) string {
	u := webURL + "/apps/" + url.PathEscape(slug) + "/installations/new"
	if state != "" {
		u += "?state=" + url.QueryEscape(state)
	}
	return u
}

// AppCredentials is what GitHub returns when the app is created.
type AppCredentials struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// ConvertManifest trades the temporary code GitHub sends on the way back for
// the credentials of the freshly created app. It only works once, and only
// within an hour.
func ConvertManifest(ctx context.Context, apiURL, code string) (*AppCredentials, error) {
	c := &client{base: apiURL, provider: GitHub, http: apiHTTP}
	req, err := c.newRequest(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Del("Authorization") // this endpoint takes no authentication
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var out AppCredentials
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.ID == 0 || out.PEM == "" {
		return nil, errors.New("GitHub did not return the credentials of the app")
	}
	return &out, nil
}

// App makes it possible to act as the GitHub App (authenticated with a JWT
// signed with its private key).
type App struct {
	APIURL string
	ID     int64
	Key    *rsa.PrivateKey
}

func ParsePrivateKey(pemData []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("invalid GitHub App private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App private key is not RSA")
	}
	return rk, nil
}

// JWT builds the token the app identifies itself to GitHub with. It lasts 9
// minutes (10 is the maximum) and is dated a minute in the past to tolerate
// small clock differences.
func (a *App) JWT(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(a.ID, 10),
	})
	signingInput := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

func (a *App) do(ctx context.Context, method, path string, body, out any) error {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return err
	}
	c := &client{base: a.APIURL, token: jwt, provider: GitHub, http: apiHTTP}
	return c.do(ctx, method, path, body, out)
}

// Installation is an account or organization where the app was installed.
type Installation struct {
	ID          int64
	Account     string
	AccountType string // "User" | "Organization"
	HTMLURL     string // page for changing which repos the app can see
}

func (a *App) Installation(ctx context.Context, id int64) (*Installation, error) {
	var raw struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		HTMLURL string `json:"html_url"`
	}
	if err := a.do(ctx, http.MethodGet, "/app/installations/"+strconv.FormatInt(id, 10), nil, &raw); err != nil {
		return nil, err
	}
	return &Installation{ID: raw.ID, Account: raw.Account.Login, AccountType: raw.Account.Type, HTMLURL: raw.HTMLURL}, nil
}

// InstallationToken creates a one-hour token with access only to the repos
// of that installation.
func (a *App) InstallationToken(ctx context.Context, id int64) (string, time.Time, error) {
	var raw struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err := a.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens", nil, &raw)
	return raw.Token, raw.ExpiresAt, err
}

// UpdateWebhook points the app's webhook at hookURL, with a new secret.
func (a *App) UpdateWebhook(ctx context.Context, hookURL, secret string) error {
	body := map[string]any{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"}
	return a.do(ctx, http.MethodPatch, "/app/hook/config", body, nil)
}
