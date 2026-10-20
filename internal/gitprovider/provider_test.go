package gitprovider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ provider, in, want string }{
		{GitHub, "", "https://api.github.com"},
		{GitHub, "https://api.github.com/", "https://api.github.com"},
		{GitHub, "https://github.miempresa.com", "https://github.miempresa.com/api/v3"},
		{GitLab, "", "https://gitlab.com/api/v4"},
		{GitLab, "https://gitlab.miempresa.com", "https://gitlab.miempresa.com/api/v4"},
		{GitLab, "https://gitlab.miempresa.com/api/v4", "https://gitlab.miempresa.com/api/v4"},
	}
	for _, c := range cases {
		got, err := NormalizeBaseURL(c.provider, c.in)
		if err != nil || got != c.want {
			t.Errorf("NormalizeBaseURL(%s, %q) = %q, %v; esperaba %q", c.provider, c.in, got, err, c.want)
		}
	}
	if _, err := NormalizeBaseURL(GitHub, "ftp://x"); err == nil {
		t.Error("una URL que no es http(s) debe rechazarse")
	}
}

func TestGitHubClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/user":
			io.WriteString(w, `{"login":"oswa"}`)
		case "/user/repos":
			io.WriteString(w, `[{"full_name":"oswa/web","default_branch":"main","private":true},{"full_name":"oswa/api","default_branch":"dev"}]`)
		case "/repos/oswa/web/commits/main":
			io.WriteString(w, `{"sha":"abc123","commit":{"message":"Arregla el menú\n\nDetalles largos"}}`)
		case "/repos/oswa/web/tarball/abc123":
			io.WriteString(w, "TARBALL")
		case "/repos/oswa/web/hooks":
			io.WriteString(w, `{"id":42}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"message":"Not Found"}`)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	p, _ := New(GitHub, srv.URL, "tok")
	if u, err := p.CurrentUser(ctx); err != nil || u != "oswa" {
		t.Fatalf("CurrentUser: %q %v", u, err)
	}
	repos, err := p.ListRepos(ctx, "WEB")
	if err != nil || len(repos) != 1 || repos[0].FullName != "oswa/web" || !repos[0].Private {
		t.Errorf("ListRepos con búsqueda: %+v %v", repos, err)
	}
	c, err := p.LatestCommit(ctx, "oswa/web", "main")
	if err != nil || c.SHA != "abc123" || c.Message != "Arregla el menú" {
		t.Errorf("LatestCommit debe quedarse con la primera línea: %+v %v", c, err)
	}
	rc, err := p.Tarball(ctx, "oswa/web", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	rc.Close()
	if string(body) != "TARBALL" {
		t.Errorf("Tarball: %q", body)
	}
	if id, err := p.CreateWebhook(ctx, "oswa/web", "https://x/hook", "s"); err != nil || id != "42" {
		t.Errorf("CreateWebhook: %q %v", id, err)
	}
	if err := p.DeleteWebhook(ctx, "oswa/web", "999"); err != nil {
		t.Errorf("borrar un webhook que ya no existe no es un error: %v", err)
	}

	bad, _ := New(GitHub, srv.URL, "otro")
	if _, err := bad.CurrentUser(ctx); err == nil || err.Error() != "token inválido o caducado" {
		t.Errorf("token inválido: %v", err)
	}
}

func TestGitLabClientKeepsEncodedProjectID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			w.WriteHeader(401)
			return
		}
		gotPath = r.URL.EscapedPath()
		switch gotPath {
		case "/api/v4/projects/grupo%2Fsub%2Fweb/repository/commits/main":
			io.WriteString(w, `{"id":"def456","message":"Primer commit\n"}`)
		case "/api/v4/projects/grupo%2Fsub%2Fweb/repository/archive.tar.gz":
			if r.URL.Query().Get("sha") != "def456" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, "TGZ")
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	p, _ := New(GitLab, srv.URL+"/api/v4", "tok")
	c, err := p.LatestCommit(ctx, "grupo/sub/web", "main")
	if err != nil || c.SHA != "def456" || c.Message != "Primer commit" {
		t.Fatalf("LatestCommit: %+v %v (ruta recibida %s)", c, err, gotPath)
	}
	rc, err := p.Tarball(ctx, "grupo/sub/web", "def456")
	if err != nil {
		t.Fatalf("Tarball: %v (ruta recibida %s)", err, gotPath)
	}
	rc.Close()
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestParseGitHubWebhook(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","after":"abc","deleted":false,"head_commit":{"id":"abc","message":"Nueva portada\n\ncuerpo"}}`)
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-Hub-Signature-256", sign("s3cr3t", body))

	push, ok, err := ParseGitHubWebhook(h, body, "s3cr3t")
	if err != nil || !ok || push.Branch != "main" || push.Commit.SHA != "abc" || push.Commit.Message != "Nueva portada" {
		t.Fatalf("push válido: %+v %v %v", push, ok, err)
	}

	if _, _, err := ParseGitHubWebhook(h, body, "otro-secreto"); !errors.Is(err, ErrBadSignature) {
		t.Error("una firma con otro secreto debe rechazarse")
	}
	tampered := []byte(string(body[:len(body)-1]) + " }")
	if _, _, err := ParseGitHubWebhook(h, tampered, "s3cr3t"); !errors.Is(err, ErrBadSignature) {
		t.Error("un cuerpo alterado debe rechazarse")
	}
	if _, _, err := ParseGitHubWebhook(h, body, ""); !errors.Is(err, ErrBadSignature) {
		t.Error("sin secreto configurado nunca debe aceptarse un webhook")
	}

	ignore := func(name, event string, body []byte) {
		h := http.Header{}
		h.Set("X-GitHub-Event", event)
		h.Set("X-Hub-Signature-256", sign("s", body))
		if _, ok, err := ParseGitHubWebhook(h, body, "s"); ok || err != nil {
			t.Errorf("%s debe ignorarse sin error: ok=%v err=%v", name, ok, err)
		}
	}
	ignore("ping", "ping", []byte(`{"zen":"x"}`))
	ignore("tag", "push", []byte(`{"ref":"refs/tags/v1","head_commit":{"id":"a"}}`))
	ignore("rama borrada", "push", []byte(`{"ref":"refs/heads/vieja","deleted":true,"head_commit":null}`))
}

func TestParseGitLabWebhook(t *testing.T) {
	body := []byte(`{"object_kind":"push","ref":"refs/heads/main","checkout_sha":"b2",
		"commits":[{"id":"b1","message":"viejo"},{"id":"b2","message":"Último cambio\n"}]}`)
	h := http.Header{}
	h.Set("X-Gitlab-Event", "Push Hook")
	h.Set("X-Gitlab-Token", "tk")

	push, ok, err := ParseGitLabWebhook(h, body, "tk")
	if err != nil || !ok || push.Branch != "main" || push.Commit.SHA != "b2" || push.Commit.Message != "Último cambio" {
		t.Fatalf("push válido: %+v %v %v", push, ok, err)
	}
	if _, _, err := ParseGitLabWebhook(h, body, "otro"); !errors.Is(err, ErrBadSignature) {
		t.Error("un token incorrecto debe rechazarse")
	}
	deleted := []byte(`{"ref":"refs/heads/vieja","checkout_sha":null,"commits":[]}`)
	if _, ok, err := ParseGitLabWebhook(h, deleted, "tk"); ok || err != nil {
		t.Errorf("una rama borrada debe ignorarse: ok=%v err=%v", ok, err)
	}
}
