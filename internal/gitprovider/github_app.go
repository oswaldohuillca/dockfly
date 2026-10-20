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

// GitHub App creada con el flujo de manifiesto: Dockfly describe la app
// (permisos, webhook, URLs de retorno), GitHub la crea en la cuenta del
// usuario con un clic y devuelve sus credenciales. Así cada instalación de
// Dockfly tiene su propia app sin que nadie tenga que rellenar formularios.
//
// https://docs.github.com/apps/sharing-github-apps/registering-a-github-app-from-a-manifest

// GitHubWebURLs devuelve la web y la API de GitHub (o de GitHub Enterprise,
// si enterprise no está vacío).
func GitHubWebURLs(enterprise string) (web, api string, err error) {
	enterprise = strings.TrimRight(strings.TrimSpace(enterprise), "/")
	if enterprise == "" {
		return "https://github.com", "https://api.github.com", nil
	}
	u, err := url.Parse(enterprise)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", fmt.Errorf("URL de GitHub Enterprise inválida: %q", enterprise)
	}
	u.Path = ""
	return u.String(), u.String() + "/api/v3", nil
}

// Manifest describe la GitHub App que se va a crear.
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

// NewManifest prepara la app de Dockfly. panelURL es la dirección con la que
// el navegador abre el panel (los retornos los hace el navegador, así que
// sirve hasta localhost); hookURL es la URL pública para los webhooks, o
// vacía si Dockfly no es accesible desde internet.
func NewManifest(name, panelURL, hookURL string) Manifest {
	m := Manifest{
		Name:          name,
		URL:           panelURL,
		RedirectURL:   panelURL + "/api/git/github/manifest",
		SetupURL:      panelURL + "/api/git/github/setup",
		SetupOnUpdate: true,
		Public:        false,
		// Solo lectura del código: Dockfly nunca escribe en tus repos. Solo
		// marca los commits con el resultado del build.
		DefaultPermissions: map[string]string{"contents": "read", "metadata": "read", "statuses": "write"},
		DefaultEvents:      []string{"push"},
	}
	if hookURL != "" {
		m.HookAttributes = &ManifestHook{URL: hookURL, Active: true}
	}
	return m
}

// ManifestFormURL es donde el navegador envía el manifiesto (por POST).
// Con org, la app pertenece a esa organización en vez de al usuario.
func ManifestFormURL(webURL, org, state string) string {
	path := "/settings/apps/new"
	if org != "" {
		path = "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return webURL + path + "?state=" + url.QueryEscape(state)
}

// InstallURL abre la pantalla de GitHub para elegir cuenta u organización y
// repositorios.
func InstallURL(webURL, slug, state string) string {
	u := webURL + "/apps/" + url.PathEscape(slug) + "/installations/new"
	if state != "" {
		u += "?state=" + url.QueryEscape(state)
	}
	return u
}

// AppCredentials es lo que devuelve GitHub al crear la app.
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

// ConvertManifest canjea el código temporal que GitHub manda al volver por
// las credenciales de la app recién creada. Solo funciona una vez y durante
// una hora.
func ConvertManifest(ctx context.Context, apiURL, code string) (*AppCredentials, error) {
	c := &client{base: apiURL, provider: GitHub, http: apiHTTP}
	req, err := c.newRequest(ctx, http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Del("Authorization") // este endpoint no lleva autenticación
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
		return nil, errors.New("GitHub no devolvió las credenciales de la app")
	}
	return &out, nil
}

// App permite actuar como la GitHub App (autenticada con un JWT firmado con
// su clave privada).
type App struct {
	APIURL string
	ID     int64
	Key    *rsa.PrivateKey
}

func ParsePrivateKey(pemData []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("clave privada de la GitHub App inválida")
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
		return nil, errors.New("la clave privada de la GitHub App no es RSA")
	}
	return rk, nil
}

// JWT genera el token con el que la app se identifica ante GitHub. Dura 9
// minutos (el máximo son 10) y se fecha un minuto atrás para tolerar
// pequeñas diferencias de reloj.
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

// Installation es una cuenta u organización donde se instaló la app.
type Installation struct {
	ID          int64
	Account     string
	AccountType string // "User" | "Organization"
	HTMLURL     string // página para cambiar qué repos ve la app
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

// InstallationToken genera un token de una hora con acceso solo a los repos
// de esa instalación.
func (a *App) InstallationToken(ctx context.Context, id int64) (string, time.Time, error) {
	var raw struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err := a.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(id, 10)+"/access_tokens", nil, &raw)
	return raw.Token, raw.ExpiresAt, err
}

// UpdateWebhook apunta el webhook de la app a hookURL, con un secreto nuevo.
func (a *App) UpdateWebhook(ctx context.Context, hookURL, secret string) error {
	body := map[string]any{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"}
	return a.do(ctx, http.MethodPatch, "/app/hook/config", body, nil)
}
