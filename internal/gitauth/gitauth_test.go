package gitauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

func setup(t *testing.T) (*store.Store, *secrets.Box) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.New(bytes.Repeat([]byte{1}, 32))
	return st, box
}

func TestInstallationTokenIsCached(t *testing.T) {
	ctx := context.Background()
	st, box := setup(t)
	var tokenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/9/access_tokens":
			tokenCalls.Add(1)
			fmt.Fprintf(w, `{"token":"ghs_%d","expires_at":%q}`, tokenCalls.Load(), time.Now().Add(time.Hour).Format(time.RFC3339))
		case "/installation/repositories":
			io.WriteString(w, `{"repositories":[{"full_name":"org/web"}]}`)
		}
	}))
	defer srv.Close()

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemData := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	app := &store.GitHubApp{WebURL: "https://github.com", APIURL: srv.URL, AppID: 1, Slug: "d", Name: "d", Owner: "o",
		HTMLURL: "h", ClientID: "c", ClientSecretEnc: box.Seal([]byte("s")), PrivateKeyEnc: box.Seal(pemData)}
	if err := st.CreateGitHubApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	inst := int64(9)
	conn := &store.GitConnection{Provider: "github", AuthType: store.AuthGitHubApp, BaseURL: srv.URL, Username: "org",
		GitHubAppID: &app.ID, InstallationID: &inst}
	if err := st.UpsertInstallation(ctx, conn); err != nil {
		t.Fatal(err)
	}
	// Instalar de nuevo la misma cuenta no duplica la conexión.
	again := *conn
	again.ID = 0
	st.UpsertInstallation(ctx, &again)
	if list, _ := st.ListGitConnections(ctx); len(list) != 1 || again.ID != conn.ID {
		t.Fatalf("UpsertInstallation duplicó la conexión: %d", len(list))
	}

	r := New(st, box)
	for range 2 {
		p, err := r.Provider(ctx, conn)
		if err != nil {
			t.Fatal(err)
		}
		if repos, err := p.ListRepos(ctx, ""); err != nil || len(repos) != 1 {
			t.Fatalf("repos: %v %v", repos, err)
		}
	}
	if n := tokenCalls.Load(); n != 1 {
		t.Errorf("el token de instalación debe reutilizarse: se pidió %d veces", n)
	}
}

func TestGitLabTokenRefreshesOnce(t *testing.T) {
	ctx := context.Background()
	st, box := setup(t)
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path == "/oauth/token" {
			// GitLab invalida el refresh token al usarlo: un segundo uso falla.
			if r.PostForm.Get("refresh_token") != "rt-1" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			refreshes.Add(1)
			time.Sleep(50 * time.Millisecond)
			fmt.Fprintf(w, `{"access_token":"at-2","refresh_token":"rt-2","expires_in":7200,"created_at":%d}`, time.Now().Unix())
		}
	}))
	defer srv.Close()

	app := &store.GitLabApp{WebURL: srv.URL, ClientID: "cid", ClientSecretEnc: box.Seal([]byte("sec"))}
	if err := st.SaveGitLabApp(ctx, app); err != nil || app.ID == 0 {
		t.Fatalf("SaveGitLabApp: %v (id %d)", err, app.ID)
	}
	expired := time.Now().Add(-time.Minute)
	conn := &store.GitConnection{Provider: "gitlab", AuthType: store.AuthGitLabOAuth, BaseURL: gitprovider.GitLabAPIURL(srv.URL),
		Username: "oswa", TokenEnc: box.Seal([]byte("at-1")), RefreshTokenEnc: box.Seal([]byte("rt-1")),
		TokenExpiresAt: &expired, GitLabAppID: &app.ID, RedirectURI: "http://x/cb"}
	st.CreateGitConnection(ctx, conn)

	r := New(st, box)
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			if _, err := r.gitlabToken(ctx, conn); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("un build concurrente falló al renovar: %v", err)
	}
	if n := refreshes.Load(); n != 1 {
		t.Errorf("el token debe renovarse una sola vez, se renovó %d", n)
	}
	saved, _ := st.GetGitConnection(ctx, conn.ID)
	if rt, _ := box.Open(saved.RefreshTokenEnc); string(rt) != "rt-2" || saved.TokenExpiresAt.Before(time.Now()) {
		t.Errorf("el refresh token nuevo debe guardarse: %q %v", rt, saved.TokenExpiresAt)
	}
}
