package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testDSN allows running these tests against another engine: with
// DOCKFLY_TEST_DATABASE_URL (postgres://… or mysql://…, a user that can
// create databases) every file path becomes a database of its own on that
// server.
func testDSN(path string) string {
	base := os.Getenv("DOCKFLY_TEST_DATABASE_URL")
	if base == "" {
		return path
	}
	sum := sha1.Sum([]byte(path))
	name := "t_" + hex.EncodeToString(sum[:8])
	admin, _, err := connect(base)
	if err != nil {
		panic(err)
	}
	defer admin.Close()
	admin.ExecContext(context.Background(), "CREATE DATABASE "+name) //nolint:errcheck // already exists when reopening
	u, _ := url.Parse(base)
	u.Path = "/" + name
	return u.String()
}

func TestConnectRejectsUnknownEngine(t *testing.T) {
	if _, _, err := connect("oracle://u:p@host/db"); err == nil {
		t.Fatal("an unknown engine must not be opened as a SQLite file")
	}
}

func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), testDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProxyHostRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	s := openTest(t, path)

	h := &ProxyHost{Domains: []string{"a.com", "www.a.com"}, TargetType: TargetExternal, ForwardScheme: "https",
		ForwardHost: "1.2.3.4", ForwardPort: 443, SSL: true, HSTS: true, PreserveHost: false, Enabled: true}
	if err := s.CreateProxyHost(ctx, h); err != nil {
		t.Fatal(err)
	}
	if h.ID == 0 {
		t.Fatal("CreateProxyHost must fill in the ID")
	}
	s.Close()

	// Reopening must neither reapply migrations nor lose data.
	s = openTest(t, path)
	got, err := s.GetProxyHost(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Domains) != 2 || got.Domains[1] != "www.a.com" {
		t.Errorf("domains differ after reading back: %v", got.Domains)
	}
	if !got.SSL || !got.HSTS || got.PreserveHost || got.ForwardPort != 443 || got.ForwardScheme != "https" {
		t.Errorf("proxy host differs after reading back: %+v", got)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("timestamps must be read in UTC, they are in %v", got.CreatedAt.Location())
	}
	if !got.CreatedAt.Equal(h.CreatedAt) {
		t.Errorf("created_at differs: stored %v, read %v", h.CreatedAt, got.CreatedAt)
	}
}

// Bun must store the zero values in an UPDATE (GORM skips them in Updates).
func TestUpdatePersistsZeroValues(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))

	h := &ProxyHost{Domains: []string{"a.com"}, TargetType: TargetDocker, ForwardScheme: "http",
		ForwardHost: "web", ForwardPort: 80, SSL: true, ForceHTTPS: true, PreserveHost: true, Enabled: true}
	if err := s.CreateProxyHost(ctx, h); err != nil {
		t.Fatal(err)
	}
	created := h.CreatedAt

	h.SSL, h.ForceHTTPS, h.PreserveHost, h.Enabled = false, false, false, false
	time.Sleep(time.Millisecond)
	if err := s.UpdateProxyHost(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProxyHost(ctx, h.ID)
	if got.SSL || got.ForceHTTPS || got.PreserveHost || got.Enabled {
		t.Errorf("the false values were not stored: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Error("UpdateProxyHost must not modify created_at")
	}
	if !got.UpdatedAt.After(created) {
		t.Error("UpdateProxyHost must update updated_at")
	}

	if err := s.UpdateProxyHost(ctx, &ProxyHost{ID: 999, Domains: []string{}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("editing a missing host must give ErrNotFound, gave %v", err)
	}
	if err := s.DeleteProxyHost(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProxyHost(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice must give ErrNotFound, gave %v", err)
	}
	if _, err := s.GetProxyHost(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a deleted host must give ErrNotFound, gave %v", err)
	}
}

func TestUsers(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))

	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("initial CountUsers: %d %v", n, err)
	}
	u := &User{Email: "a@b.com", Name: "A", PasswordHash: "x"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, &User{Email: "a@b.com", Name: "B", PasswordHash: "y"}); err == nil {
		t.Error("the email must be unique")
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Errorf("CountUsers: expected 1, got %d", n)
	}
	got, err := s.UserByEmail(ctx, "a@b.com")
	if err != nil || got.ID != u.ID || got.PasswordHash != "x" {
		t.Errorf("UserByEmail: %+v %v", got, err)
	}
	if got, err := s.UserByEmail(ctx, "nobody@b.com"); !errors.Is(err, ErrNotFound) || got != nil {
		t.Errorf("missing user: must return nil and ErrNotFound, gave %v %v", got, err)
	}
	if _, err := s.UserByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserByID on a missing user: %v", err)
	}
}
