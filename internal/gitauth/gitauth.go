// Package gitauth obtiene credenciales válidas para una conexión de Git,
// sea cual sea su tipo: token personal, instalación de la GitHub App u
// OAuth de GitLab.
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

// Margen con el que se renueva un token antes de que caduque: un build
// puede tardar varios minutos en descargar el código.
const refreshMargin = 10 * time.Minute

type Resolver struct {
	Store   *store.Store
	Secrets *secrets.Box

	mu    sync.Mutex
	cache map[int64]cachedToken // tokens de instalación, por conexión
	locks map[int64]*sync.Mutex // un refresco a la vez por conexión
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

// Provider devuelve un cliente con credenciales vigentes para la conexión.
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
			return nil, fmt.Errorf("no se pudo descifrar el token de %s: %w", conn.Username, err)
		}
		return gitprovider.New(conn.Provider, conn.BaseURL, string(token))
	}
}

// App reconstruye la GitHub App a partir de sus credenciales cifradas.
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
		return "", fmt.Errorf("la conexión %s no tiene una instalación de GitHub válida", conn.Username)
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
		return "", fmt.Errorf("la GitHub App de %s ya no existe", conn.Username)
	}
	ghApp, err := r.App(app)
	if err != nil {
		return "", err
	}
	token, expires, err := ghApp.InstallationToken(ctx, *conn.InstallationID)
	if err != nil {
		return "", fmt.Errorf("GitHub no dio acceso a %s (¿se desinstaló la app?): %w", conn.Username, err)
	}
	r.mu.Lock()
	r.cache[conn.ID] = cachedToken{token: token, expires: expires}
	r.mu.Unlock()
	return token, nil
}

// Forget descarta los tokens en caché de una conexión (p. ej. al borrarla).
func (r *Resolver) Forget(connID int64) {
	r.mu.Lock()
	delete(r.cache, connID)
	r.mu.Unlock()
}

func (r *Resolver) gitlabToken(ctx context.Context, conn *store.GitConnection) (string, error) {
	unlock := r.lock(conn.ID)
	defer unlock()

	// Releemos la conexión dentro del candado: otro build pudo renovar el
	// token mientras esperábamos, y el refresh token anterior ya no vale.
	fresh, err := r.Store.GetGitConnection(ctx, conn.ID)
	if err != nil {
		return "", err
	}
	if fresh.TokenExpiresAt == nil || time.Until(*fresh.TokenExpiresAt) > refreshMargin {
		token, err := r.Secrets.Open(fresh.TokenEnc)
		return string(token), err
	}

	if fresh.GitLabAppID == nil {
		return "", fmt.Errorf("la conexión %s no tiene aplicación de GitLab", conn.Username)
	}
	app, err := r.Store.GetGitLabApp(ctx, *fresh.GitLabAppID)
	if err != nil {
		return "", fmt.Errorf("la aplicación de GitLab de %s ya no existe", conn.Username)
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
		return "", fmt.Errorf("hay que volver a conectar GitLab (%s): %w", conn.Username, err)
	}
	fresh.TokenEnc = r.Secrets.Seal([]byte(tok.AccessToken))
	fresh.RefreshTokenEnc = r.Secrets.Seal([]byte(tok.RefreshToken))
	fresh.TokenExpiresAt = &tok.ExpiresAt
	// Guardar sin cancelación: si se pierde el refresh token nuevo, la
	// conexión queda inservible.
	if err := r.Store.UpdateConnectionTokens(context.WithoutCancel(ctx), fresh); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}
