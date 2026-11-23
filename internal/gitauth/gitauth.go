// Package gitauth obtains valid credentials for a Git connection, whatever
// its type: personal token, GitHub App installation or GitLab OAuth.
package gitauth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

// How long before expiry a token is renewed: a build can take several
// minutes just to download the code.
const refreshMargin = 10 * time.Minute

type Resolver struct {
	Store   *store.Store
	Secrets *secrets.Box

	mu    sync.Mutex
	cache map[int64]cachedToken // installation tokens, per connection
	locks map[int64]*sync.Mutex // one refresh at a time per connection
}

type cachedToken struct {
	token   string
	expires time.Time
}

func New(st *store.Store, box *secrets.Box) *Resolver {
	return &Resolver{Store: st, Secrets: box, cache: map[int64]cachedToken{}, locks: map[int64]*sync.Mutex{}}
}

func (r *Resolver) lock(connID int64) func() {
	r.mu.Lock()
	l, ok := r.locks[connID]
	if !ok {
		l = &sync.Mutex{}
		r.locks[connID] = l
	}
	r.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Provider returns a client with currently valid credentials for the connection.
func (r *Resolver) Provider(ctx context.Context, conn *store.GitConnection) (gitprovider.Provider, error) {
	switch conn.AuthType {
	case store.AuthGitHubApp:
		token, err := r.installationToken(ctx, conn)
		if err != nil {
			return nil, err
		}
		return gitprovider.NewWithOptions(conn.Provider, conn.BaseURL, token, gitprovider.Options{Installation: true})
	case store.AuthGitLabOAuth:
		token, err := r.gitlabToken(ctx, conn)
		if err != nil {
			return nil, err
		}
		return gitprovider.NewWithOptions(conn.Provider, conn.BaseURL, token, gitprovider.Options{OAuth: true})
	default:
		token, err := r.Secrets.Open(conn.TokenEnc)
		if err != nil {
			return nil, fmt.Errorf("could not decrypt the token of %s: %w", conn.Username, err)
		}
		return gitprovider.New(conn.Provider, conn.BaseURL, string(token))
	}
}

// App rebuilds the GitHub App from its encrypted credentials.
func (r *Resolver) App(app *store.GitHubApp) (*gitprovider.App, error) {
	pemData, err := r.Secrets.Open(app.PrivateKeyEnc)
	if err != nil {
		return nil, err
	}
	key, err := gitprovider.ParsePrivateKey(pemData)
	if err != nil {
		return nil, err
	}
	return &gitprovider.App{APIURL: app.APIURL, ID: app.AppID, Key: key}, nil
}

func (r *Resolver) installationToken(ctx context.Context, conn *store.GitConnection) (string, error) {
	if conn.GitHubAppID == nil || conn.InstallationID == nil {
		return "", fmt.Errorf("the connection %s has no valid GitHub installation", conn.Username)
	}
	unlock := r.lock(conn.ID)
	defer unlock()

	r.mu.Lock()
	c, ok := r.cache[conn.ID]
	r.mu.Unlock()
	if ok && time.Until(c.expires) > refreshMargin {
		return c.token, nil
	}

	app, err := r.Store.GetGitHubApp(ctx, *conn.GitHubAppID)
	if err != nil {
		return "", fmt.Errorf("the GitHub App of %s no longer exists", conn.Username)
	}
	ghApp, err := r.App(app)
	if err != nil {
		return "", err
	}
	token, expires, err := ghApp.InstallationToken(ctx, *conn.InstallationID)
	if err != nil {
		return "", fmt.Errorf("GitHub denied access to %s (was the app uninstalled?): %w", conn.Username, err)
	}
	r.mu.Lock()
	r.cache[conn.ID] = cachedToken{token: token, expires: expires}
	r.mu.Unlock()
	return token, nil
}

// Forget drops the cached tokens of a connection (for example when it is deleted).
func (r *Resolver) Forget(connID int64) {
	r.mu.Lock()
	delete(r.cache, connID)
	r.mu.Unlock()
}

func (r *Resolver) gitlabToken(ctx context.Context, conn *store.GitConnection) (string, error) {
	unlock := r.lock(conn.ID)
	defer unlock()

	// Re-read the connection inside the lock: another build may have renewed
	// the token while we waited, and the previous refresh token is dead.
	fresh, err := r.Store.GetGitConnection(ctx, conn.ID)
	if err != nil {
		return "", err
	}
	if fresh.TokenExpiresAt == nil || time.Until(*fresh.TokenExpiresAt) > refreshMargin {
		token, err := r.Secrets.Open(fresh.TokenEnc)
		return string(token), err
	}

	if fresh.GitLabAppID == nil {
		return "", fmt.Errorf("the connection %s has no GitLab application", conn.Username)
	}
	app, err := r.Store.GetGitLabApp(ctx, *fresh.GitLabAppID)
	if err != nil {
		return "", fmt.Errorf("the GitLab application of %s no longer exists", conn.Username)
	}
	secret, err := r.Secrets.Open(app.ClientSecretEnc)
	if err != nil {
		return "", err
	}
	refresh, err := r.Secrets.Open(fresh.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	tok, err := gitprovider.GitLabRefresh(ctx, app.WebURL, app.ClientID, string(secret), string(refresh), fresh.RedirectURI)
	if err != nil {
		return "", fmt.Errorf("GitLab has to be connected again (%s): %w", conn.Username, err)
	}
	fresh.TokenEnc = r.Secrets.Seal([]byte(tok.AccessToken))
	fresh.RefreshTokenEnc = r.Secrets.Seal([]byte(tok.RefreshToken))
	fresh.TokenExpiresAt = &tok.ExpiresAt
	// Save without cancellation: if the new refresh token is lost, the
	// connection becomes unusable.
	if err := r.Store.UpdateConnectionTokens(context.WithoutCancel(ctx), fresh); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}
