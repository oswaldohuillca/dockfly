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

// verifyJWT validates the RS256 signature with the public key, as GitHub does.
func verifyJWT(t *testing.T, token string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT with %d parts", len(parts))
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("invalid JWT signature: %v", err)
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
		t.Errorf("iat/exp outside what GitHub accepts: iat=%d exp=%d now=%d", iat, exp, now.Unix())
	}

	// PKCS#8 must be accepted too.
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	if _, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		t.Errorf("PKCS#8: %v", err)
	}
	if _, err := ParsePrivateKey([]byte("garbage")); err == nil {
		t.Error("an invalid key must return an error")
	}
}

func TestManifestAndAppAPI(t *testing.T) {
	key, pemData := testKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/app-manifests/CODE1/conversions":
			if r.Header.Get("Authorization") != "" {
				t.Error("the manifest conversion must not carry authentication")
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
				io.WriteString(w, `{"id":9,"account":{"login":"my-org","type":"Organization"},"html_url":"https://github.com/organizations/my-org/settings/installations/9"}`)
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
			io.WriteString(w, `{"total_count":1,"repositories":[{"full_name":"my-org/web","default_branch":"main","private":true}]}`)
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
	if err != nil || inst.Account != "my-org" || inst.AccountType != "Organization" {
		t.Fatalf("Installation: %+v %v", inst, err)
	}
	tok, exp, err := app.InstallationToken(ctx, 9)
	if err != nil || tok != "ghs_inst" || exp.Year() != 2030 {
		t.Fatalf("InstallationToken: %q %v %v", tok, exp, err)
	}
	if err := app.UpdateWebhook(ctx, "https://pub/api/hooks/github-app", "new"); err != nil {
		t.Fatal(err)
	}

	// With an installation token, the repo list is the installation's.
	p, _ := NewWithOptions(GitHub, srv.URL, tok, Options{Installation: true})
	repos, err := p.ListRepos(ctx, "")
	if err != nil || len(repos) != 1 || repos[0].FullName != "my-org/web" {
		t.Errorf("repos of the installation: %+v %v", repos, err)
	}
}

func TestManifest(t *testing.T) {
	m := NewManifest("Dockfly", "http://localhost:81", "")
	if m.HookAttributes != nil {
		t.Error("with no public URL, the app must not declare a webhook")
	}
	if m.RedirectURL != "http://localhost:81/api/git/github/manifest" || m.DefaultPermissions["contents"] != "read" {
		t.Errorf("manifest: %+v", m)
	}
	if m := NewManifest("Dockfly", "http://x", "https://pub/api/hooks/github-app"); m.HookAttributes == nil || !m.HookAttributes.Active {
		t.Error("with a public URL, the webhook must be active")
	}
	if u := ManifestFormURL("https://github.com", "my org", "s t"); u != "https://github.com/organizations/my%20org/settings/apps/new?state=s+t" {
		t.Errorf("ManifestFormURL: %s", u)
	}
	web, api, err := GitHubWebURLs("https://ghe.mycompany.com/")
	if err != nil || web != "https://ghe.mycompany.com" || api != "https://ghe.mycompany.com/api/v3" {
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
		t.Errorf("authorization URL: %s", u)
	}

	tok, err := GitLabExchangeCode(ctx, srv.URL, "cid", "sec", "code1", "http://localhost:81/api/git/gitlab/callback")
	if err != nil || tok.AccessToken != "at-authorization_code" || tok.RefreshToken != "rt-2" {
		t.Fatalf("code exchange: %+v %v", tok, err)
	}
	if d := time.Until(tok.ExpiresAt); d < time.Hour || d > 3*time.Hour {
		t.Errorf("computed expiry: %v", d)
	}
	if calls[0].Get("redirect_uri") == "" || calls[0].Get("code") != "code1" {
		t.Errorf("the exchange must send code and redirect_uri: %v", calls[0])
	}

	// The OAuth token goes as Bearer, not as PRIVATE-TOKEN.
	p, _ := NewWithOptions(GitLab, GitLabAPIURL(srv.URL), tok.AccessToken, Options{OAuth: true})
	if u, err := p.CurrentUser(ctx); err != nil || u != "oswa" {
		t.Errorf("user with an OAuth token: %q %v", u, err)
	}

	if _, err := GitLabRefresh(ctx, srv.URL, "cid", "sec", "rt-1", "http://x"); err != nil {
		t.Fatal(err)
	}
	if calls[1].Get("grant_type") != "refresh_token" || calls[1].Get("refresh_token") != "rt-1" || calls[1].Get("redirect_uri") != "http://x" {
		t.Errorf("refresh: %v", calls[1])
	}
	if _, err := GitLabExchangeCode(ctx, srv.URL, "cid", "wrong", "c", "r"); err == nil ||
		!strings.Contains(err.Error(), "Client authentication failed") {
		t.Errorf("wrong app credentials: %v", err)
	}
	if w, _ := GitLabWebURL("https://gitlab.mycompany.com/api/v4/"); w != "https://gitlab.mycompany.com" {
		t.Errorf("GitLabWebURL: %s", w)
	}
}

func TestParseGitHubAppEvents(t *testing.T) {
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	body := []byte(`{"ref":"refs/heads/main","head_commit":{"id":"a1","message":"m"},
		"repository":{"full_name":"my-org/web"},"installation":{"id":9}}`)
	p, ok, err := ParseGitHubPush(h, body)
	if err != nil || !ok || p.Repo != "my-org/web" || p.InstallationID != 9 {
		t.Errorf("push from the app: %+v %v %v", p, ok, err)
	}
	h.Set("X-GitHub-Event", "installation")
	if id, ok := ParseGitHubInstallationDeleted(h, []byte(`{"action":"deleted","installation":{"id":9}}`)); !ok || id != 9 {
		t.Errorf("uninstall: %d %v", id, ok)
	}
	if _, ok := ParseGitHubInstallationDeleted(h, []byte(`{"action":"created","installation":{"id":9}}`)); ok {
		t.Error("a new installation is not an uninstall")
	}
}
