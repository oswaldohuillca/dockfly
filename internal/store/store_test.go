package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
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
		t.Fatal("CreateProxyHost debe rellenar el ID")
	}
	s.Close()

	// Reabrir no debe volver a aplicar migraciones ni perder datos.
	s = openTest(t, path)
	got, err := s.GetProxyHost(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Domains) != 2 || got.Domains[1] != "www.a.com" {
		t.Errorf("dominios distintos tras releer: %v", got.Domains)
	}
	if !got.SSL || !got.HSTS || got.PreserveHost || got.ForwardPort != 443 || got.ForwardScheme != "https" {
		t.Errorf("proxy host distinto tras releer: %+v", got)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("las fechas leídas deben estar en UTC, están en %v", got.CreatedAt.Location())
	}
	if !got.CreatedAt.Equal(h.CreatedAt) {
		t.Errorf("created_at distinto: guardado %v, leído %v", h.CreatedAt, got.CreatedAt)
	}
}

// Bun debe guardar los valores cero en un UPDATE (GORM los ignora con Updates).
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
		t.Errorf("los false no se guardaron: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Error("UpdateProxyHost no debe modificar created_at")
	}
	if !got.UpdatedAt.After(created) {
		t.Error("UpdateProxyHost debe actualizar updated_at")
	}

	if err := s.UpdateProxyHost(ctx, &ProxyHost{ID: 999, Domains: []string{}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("editar inexistente debe dar ErrNotFound, dio %v", err)
	}
	if err := s.DeleteProxyHost(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProxyHost(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("borrar dos veces debe dar ErrNotFound, dio %v", err)
	}
	if _, err := s.GetProxyHost(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("leer borrado debe dar ErrNotFound, dio %v", err)
	}
}

func TestUsers(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "t.db"))

	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers inicial: %d %v", n, err)
	}
	u := &User{Email: "a@b.com", Name: "A", PasswordHash: "x"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, &User{Email: "a@b.com", Name: "B", PasswordHash: "y"}); err == nil {
		t.Error("el email debe ser único")
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Errorf("CountUsers: esperaba 1, obtuve %d", n)
	}
	got, err := s.UserByEmail(ctx, "a@b.com")
	if err != nil || got.ID != u.ID || got.PasswordHash != "x" {
		t.Errorf("UserByEmail: %+v %v", got, err)
	}
	if got, err := s.UserByEmail(ctx, "nadie@b.com"); !errors.Is(err, ErrNotFound) || got != nil {
		t.Errorf("usuario inexistente: debe devolver nil y ErrNotFound, dio %v %v", got, err)
	}
	if _, err := s.UserByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserByID inexistente: %v", err)
	}
}
