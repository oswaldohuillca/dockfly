package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// legacySchema is the schema Dockfly created before it used Bun, with the
// version kept in PRAGMA user_version.
const legacySchema = `
CREATE TABLE users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	email         TEXT NOT NULL UNIQUE,
	name          TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	created_at    DATETIME NOT NULL
);
CREATE TABLE proxy_hosts (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	domains         TEXT NOT NULL,
	target_type     TEXT NOT NULL,
	forward_scheme  TEXT NOT NULL,
	forward_host    TEXT NOT NULL,
	forward_port    INTEGER NOT NULL,
	ssl             INTEGER NOT NULL DEFAULT 0,
	force_https     INTEGER NOT NULL DEFAULT 0,
	hsts            INTEGER NOT NULL DEFAULT 0,
	skip_tls_verify INTEGER NOT NULL DEFAULT 0,
	%s
	enabled         INTEGER NOT NULL DEFAULT 1,
	created_at      DATETIME NOT NULL,
	updated_at      DATETIME NOT NULL
);
PRAGMA user_version = 1;
`

// createLegacyDB writes the data the way the old code did: database/sql
// directly, passing time.Time as a parameter.
func createLegacyDB(t *testing.T, withPreserveHost bool) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	col := ""
	if withPreserveHost {
		col = "preserve_host INTEGER NOT NULL DEFAULT 1,"
	}
	if _, err := db.Exec(fmt.Sprintf(legacySchema, col)); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO users (email, name, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		"a@b.com", "A", "hash", created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO proxy_hosts (domains, target_type, forward_scheme, forward_host, forward_port,
		ssl, force_https, hsts, skip_tls_verify, enabled, created_at, updated_at)
		VALUES (?, 'docker', 'http', 'web', 3000, 1, 1, 0, 0, 0, ?, ?)`,
		`["a.com","www.a.com"]`, created, created); err != nil {
		t.Fatal(err)
	}
	return path, created
}

func TestAdoptLegacyDatabase(t *testing.T) {
	if os.Getenv("DOCKFLY_TEST_DATABASE_URL") != "" {
		t.Skip("the pre-Bun databases only exist in SQLite")
	}
	for _, withPreserveHost := range []bool{true, false} {
		name := "without preserve_host"
		if withPreserveHost {
			name = "with preserve_host"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path, created := createLegacyDB(t, withPreserveHost)

			// Open twice: the adoption must happen only once.
			for range 2 {
				s, err := Open(ctx, path)
				if err != nil {
					t.Fatalf("opening the legacy database: %v", err)
				}
				s.Close()
			}

			s := openTest(t, path)
			u, err := s.UserByEmail(ctx, "a@b.com")
			if err != nil {
				t.Fatalf("reading the legacy user: %v", err)
			}
			if !u.CreatedAt.Equal(created) {
				t.Errorf("user timestamp: stored %v, read %v", created, u.CreatedAt)
			}

			hosts, err := s.ListProxyHosts(ctx)
			if err != nil {
				t.Fatalf("reading the legacy proxy hosts: %v", err)
			}
			h := hosts[0]
			if len(h.Domains) != 2 || !h.SSL || !h.ForceHTTPS || h.HSTS || h.Enabled || h.ForwardPort != 3000 {
				t.Errorf("legacy proxy host read incorrectly: %+v", h)
			}
			if !h.PreserveHost {
				t.Error("preserve_host must default to true in legacy databases")
			}

			// And writing keeps working as usual.
			h.Enabled = true
			if err := s.UpdateProxyHost(ctx, h); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateProxyHost(ctx, &ProxyHost{Domains: []string{"b.com"}, TargetType: TargetHost,
				ForwardScheme: "http", ForwardHost: "host.docker.internal", ForwardPort: 8080}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
