package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth de GitLab. GitLab no permite crear la aplicación automáticamente
// (como el manifiesto de GitHub), así que el administrador la registra una
// vez; después, cada usuario solo ve la ventana de "Autorizar".

// GitLabScope es el permiso que se pide: "api" es necesario para registrar
// los webhooks de push (read_repository no basta).
const GitLabScope = "api"

// GitLabWebURL normaliza la URL de una instancia de GitLab ("" = gitlab.com).
func GitLabWebURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "https://gitlab.com", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("URL de GitLab inválida: %q", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/api/v4")
	return strings.TrimRight(u.String(), "/"), nil
}

func GitLabAPIURL(webURL string) string { return webURL + "/api/v4" }

// GitLabAuthorizeURL es la pantalla de GitLab donde el usuario autoriza a Dockfly.
func GitLabAuthorizeURL(webURL, clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {GitLabScope},
		"state":         {state},
	}
	return webURL + "/oauth/authorize?" + q.Encode()
}

type OAuthToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// GitLabExchangeCode canjea el código de autorización por un token.
func GitLabExchangeCode(ctx context.Context, webURL, clientID, secret, code, redirectURI string) (*OAuthToken, error) {
	return gitlabToken(ctx, webURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {secret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	})
}

// GitLabRefresh renueva un token caducado. GitLab invalida el refresh token
// usado y devuelve uno nuevo, que hay que guardar.
func GitLabRefresh(ctx context.Context, webURL, clientID, secret, refreshToken, redirectURI string) (*OAuthToken, error) {
	return gitlabToken(ctx, webURL, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"client_secret": {secret},
		"refresh_token": {refreshToken},
		"redirect_uri":  {redirectURI},
	})
}

func gitlabToken(ctx context.Context, webURL string, form url.Values) (*OAuthToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := apiHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		CreatedAt    int64  `json:"created_at"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("respuesta de GitLab inválida (%d)", resp.StatusCode)
	}
	if resp.StatusCode >= 300 || raw.AccessToken == "" {
		msg := raw.Description
		if msg == "" {
			msg = raw.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, errors.New("GitLab rechazó la autorización: " + msg)
	}
	t := &OAuthToken{AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken}
	if raw.ExpiresIn > 0 {
		created := time.Now()
		if raw.CreatedAt > 0 {
			created = time.Unix(raw.CreatedAt, 0)
		}
		t.ExpiresAt = created.Add(time.Duration(raw.ExpiresIn) * time.Second)
	}
	return t, nil
}
