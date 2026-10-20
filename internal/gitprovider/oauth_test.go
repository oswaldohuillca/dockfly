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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

// verifyJWT valida la firma RS256 con la clave pública, como hace GitHub.
func verifyJWT(t *testing.T, token string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT con %d partes", len(parts))
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("firma del JWT inválida: %v", err)
	}
	var header, claims map[string]any
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(h, &header)
	json.Unmarshal(c, &claims)
	if header["alg"] != "RS256" {
		t.Errorf("alg = %v", header["alg"])
	}
	return claims
}

func TestAppJWT(t *testing.T) {
	key, pemData := testKey(t)
	parsed, err := ParsePrivateKey(pemData)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{ID: 4242, Key: parsed}
	now := time.Unix(1_800_000_000, 0)
	tok, err := app.JWT(now)
	if err != nil {
		t.Fatal(err)
	}
	claims := verifyJWT(t, tok, &key.PublicKey)
	if claims["iss"] != "4242" {
		t.Errorf("iss = %v", claims["iss"])
	}
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	if iat >= now.Unix() || exp-iat > 600 {
		t.Errorf("iat/exp fuera de lo que acepta GitHub: iat=%d exp=%d now=%d", iat, exp, now.Unix())
	}

	// PKCS#8 también debe aceptarse.
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if _, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		t.Errorf("PKCS#8: %v", err)
	}
	if _, err := ParsePrivateKey([]byte("basura")); err == nil {
		t.Error("una clave inválida debe dar error")
	}
}

func TestManifestAndAppAPI(t *testing.T) {
	key, pemData := testKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/app-manifests/CODE1/conversions":
			if r.Header.Get("Authorization") != "" {
				t.Error("la conversión del manifiesto no debe llevar autenticación")
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id": 77, "slug": "dockfly-oswa", "name": "Dockfly (oswa)", "html_url": "https://github.com/apps/dockfly-oswa",
				"client_id": "Iv1.abc", "client_secret": "csecret", "webhook_secret": "wsecret", "pem": string(pemData),
				"owner": map[string]string{"login": "oswa"},
			})
		case strings.HasPrefix(r.URL.Path, "/app/"):
			tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			verifyJWT(t, tok, &key.PublicKey)
			switch r.URL.Path {
			case "/app/installations/9":
				io.WriteString(w, `{"id":9,"account":{"login":"mi-org","type":"Organization"},"html_url":"https://github.com/organizations/mi-org/settings/installations/9"}`)
			case "/app/installations/9/access_tokens":
				io.WriteString(w, `{"token":"ghs_inst","expires_at":"2030-01-01T00:00:00Z"}`)
			case "/app/hook/config":
				body, _ := io.ReadAll(r.Body)
				if r.Method != "PATCH" || !strings.Contains(string(body), `"url":"https://pub/api/hooks/github-app"`) {
					t.Errorf("UpdateWebhook: %s %s", r.Method, body)
				}
				io.WriteString(w, `{}`)
			}
		case r.URL.Path == "/installation/repositories":
			if r.Header.Get("Authorization") != "Bearer ghs_inst" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, `{"total_count":1,"repositories":[{"full_name":"mi-org/web","default_branch":"main","private":true}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	creds, err := ConvertManifest(ctx, srv.URL, "CODE1")
	if err != nil || creds.ID != 77 || creds.Slug != "dockfly-oswa" || creds.Owner.Login != "oswa" {
		t.Fatalf("ConvertManifest: %+v %v", creds, err)
	}
	k, _ := ParsePrivateKey([]byte(creds.PEM))
	app := &App{APIURL: srv.URL, ID: creds.ID, Key: k}

	inst, err := app.Installation(ctx, 9)
	if err != nil || inst.Account != "mi-org" || inst.AccountType != "Organization" {
		t.Fatalf("Installation: %+v %v", inst, err)
	}
	tok, exp, err := app.InstallationToken(ctx, 9)
	if err != nil || tok != "ghs_inst" || exp.Year() != 2030 {
		t.Fatalf("InstallationToken: %q %v %v", tok, exp, err)
	}
	if err := app.UpdateWebhook(ctx, "https://pub/api/hooks/github-app", "nuevo"); err != nil {
		t.Fatal(err)
	}

	// Con un token de instalación, la lista de repos es la de la instalación.
	p, _ := NewWithOptions(GitHub, srv.URL, tok, Options{Installation: true})
	repos, err := p.ListRepos(ctx, "")
	if err != nil || len(repos) != 1 || repos[0].FullName != "mi-org/web" {
		t.Errorf("repos de la instalación: %+v %v", repos, err)
	}
}

func TestManifest(t *testing.T) {
	m := NewManifest("Dockfly", "http://localhost:81", "")
	if m.HookAttributes != nil {
		t.Error("sin URL pública, la app no debe declarar webhook")
	}
	if m.RedirectURL != "http://localhost:81/api/git/github/manifest" || m.DefaultPermissions["contents"] != "read" {
		t.Errorf("manifiesto: %+v", m)
	}
	if m := NewManifest("Dockfly", "http://x", "https://pub/api/hooks/github-app"); m.HookAttributes == nil || !m.HookAttributes.Active {
		t.Error("con URL pública, el webhook debe ir activo")
	}
	if u := ManifestFormURL("https://github.com", "mi org", "s t"); u != "https://github.com/organizations/mi%20org/settings/apps/new?state=s+t" {
		t.Errorf("ManifestFormURL: %s", u)
	}
	web, api, err := GitHubWebURLs("https://ghe.empresa.com/")
	if err != nil || web != "https://ghe.empresa.com" || api != "https://ghe.empresa.com/api/v3" {
		t.Errorf("GitHubWebURLs: %s %s %v", web, api, err)
	}
}

func TestGitLabOAuth(t *testing.T) {
	var calls []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			r.ParseForm()
			calls = append(calls, r.PostForm)
			if r.PostForm.Get("client_secret") != "sec" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":"invalid_client","error_description":"Client authentication failed"}`)
				return
			}
			fmt.Fprintf(w, `{"access_token":"at-%s","refresh_token":"rt-2","expires_in":7200,"created_at":%d}`,
				r.PostForm.Get("grant_type"), time.Now().Unix())
		case "/api/v4/user":
			if r.Header.Get("Authorization") != "Bearer at-authorization_code" || r.Header.Get("PRIVATE-TOKEN") != "" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, `{"username":"oswa"}`)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	u := GitLabAuthorizeURL(srv.URL, "cid", "http://localhost:81/api/git/gitlab/callback", "st")
	parsed, _ := url.Parse(u)
	if parsed.Query().Get("scope") != "api" || parsed.Query().Get("state") != "st" || parsed.Path != "/oauth/authorize" {
		t.Errorf("URL de autorización: %s", u)
	}

	tok, err := GitLabExchangeCode(ctx, srv.URL, "cid", "sec", "code1", "http://localhost:81/api/git/gitlab/callback")
	if err != nil || tok.AccessToken != "at-authorization_code" || tok.RefreshToken != "rt-2" {
		t.Fatalf("canje del código: %+v %v", tok, err)
	}
	if d := time.Until(tok.ExpiresAt); d < time.Hour || d > 3*time.Hour {
		t.Errorf("caducidad calculada: %v", d)
	}
	if calls[0].Get("redirect_uri") == "" || calls[0].Get("code") != "code1" {
		t.Errorf("el canje debe enviar code y redirect_uri: %v", calls[0])
	}

	// El token OAuth va como Bearer, no como PRIVATE-TOKEN.
	p, _ := NewWithOptions(GitLab, GitLabAPIURL(srv.URL), tok.AccessToken, Options{OAuth: true})
	if u, err := p.CurrentUser(ctx); err != nil || u != "oswa" {
		t.Errorf("usuario con token OAuth: %q %v", u, err)
	}

	if _, err := GitLabRefresh(ctx, srv.URL, "cid", "sec", "rt-1", "http://x"); err != nil {
		t.Fatal(err)
	}
	if calls[1].Get("grant_type") != "refresh_token" || calls[1].Get("refresh_token") != "rt-1" || calls[1].Get("redirect_uri") != "http://x" {
		t.Errorf("refresco: %v", calls[1])
	}
	if _, err := GitLabExchangeCode(ctx, srv.URL, "cid", "mal", "c", "r"); err == nil ||
		!strings.Contains(err.Error(), "Client authentication failed") {
		t.Errorf("credenciales de la app incorrectas: %v", err)
	}
	if w, _ := GitLabWebURL("https://gitlab.empresa.com/api/v4/"); w != "https://gitlab.empresa.com" {
		t.Errorf("GitLabWebURL: %s", w)
	}
}

func TestParseGitHubAppEvents(t *testing.T) {
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	body := []byte(`{"ref":"refs/heads/main","head_commit":{"id":"a1","message":"m"},
		"repository":{"full_name":"mi-org/web"},"installation":{"id":9}}`)
	p, ok, err := ParseGitHubPush(h, body)
	if err != nil || !ok || p.Repo != "mi-org/web" || p.InstallationID != 9 {
		t.Errorf("push de la app: %+v %v %v", p, ok, err)
	}
	h.Set("X-GitHub-Event", "installation")
	if id, ok := ParseGitHubInstallationDeleted(h, []byte(`{"action":"deleted","installation":{"id":9}}`)); !ok || id != 9 {
		t.Errorf("desinstalación: %d %v", id, ok)
	}
	if _, ok := ParseGitHubInstallationDeleted(h, []byte(`{"action":"created","installation":{"id":9}}`)); ok {
		t.Error("una instalación nueva no es una desinstalación")
	}
}
