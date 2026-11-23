// Package store persists Dockfly's state with Bun: in SQLite (the default),
// PostgreSQL or MySQL/MariaDB.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
	_ "modernc.org/sqlite" // pure Go SQLite driver: no CGO
)

var ErrNotFound = errors.New("not found")

// migrationsFS holds the versioned SQL migrations, one directory per engine.
// Never edit a migration that has shipped: add a new file with a later
// timestamp, in all three directories.
//
//go:embed migrations/*.sql migrations_postgres/*.sql migrations_mysql/*.sql
var migrationsFS embed.FS

// Target types of a proxy host.
const (
	TargetDocker   = "docker"   // Docker container (by name)
	TargetHost     = "host"     // application on this same server
	TargetExternal = "external" // external IP or domain
)

type User struct {
	bun.BaseModel `bun:"table:users"`

	ID           int64     `bun:",pk,autoincrement" json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"` // empty until the invitation is accepted
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	// SessionVersion goes in every session: bumping it invalidates the older ones.
	SessionVersion int64 `json:"-"`

	// Two-factor authentication. The secret is stored encrypted; while it is
	// being set up it exists but TOTPEnabled is still false.
	TOTPSecretEnc []byte   `bun:"totp_secret_enc" json:"-"`
	TOTPEnabled   bool     `bun:"totp_enabled" json:"totp_enabled"`
	TOTPLastStep  int64    `bun:"totp_last_step" json:"-"`
	RecoveryCodes []string `bun:"recovery_codes" json:"-"`
}

type ProxyHost struct {
	bun.BaseModel `bun:"table:proxy_hosts"`

	ID            int64    `bun:",pk,autoincrement" json:"id"`
	Domains       []string `json:"domains"` // Bun stores slices as JSON
	TargetType    string   `json:"target_type"`
	ForwardScheme string   `json:"forward_scheme"`
	ForwardHost   string   `json:"forward_host"`
	ForwardPort   int      `json:"forward_port"`
	SSL           bool     `bun:"ssl" json:"ssl"`
	ForceHTTPS    bool     `bun:"force_https" json:"force_https"`
	HSTS          bool     `bun:"hsts" json:"hsts"`
	SkipTLSVerify bool     `bun:"skip_tls_verify" json:"skip_tls_verify"`
	PreserveHost  bool     `json:"preserve_host"`
	Enabled       bool     `json:"enabled"`
	AccessListID  *int64   `json:"access_list_id"`
	// Locations send part of the domain (/api…) to a different target.
	Locations []ProxyLocation `json:"locations"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Access of a location: inherited from its proxy host or site, open to all, or an access list.
const (
	LocationAccessInherit = "inherit"
	LocationAccessPublic  = "public"
	LocationAccessList    = "list"
)

// ProxyLocation is a custom location of a proxy host or of a site.
type ProxyLocation struct {
	Path          string `json:"path"` // "/api"
	TargetType    string `json:"target_type"`
	ForwardScheme string `json:"forward_scheme"`
	ForwardHost   string `json:"forward_host"`
	ForwardPort   int    `json:"forward_port"`
	// StripPrefix removes Path before forwarding (/api/users → /users).
	StripPrefix  bool   `json:"strip_prefix"`
	Access       string `json:"access"`
	AccessListID *int64 `json:"access_list_id"`
}

// The SQLite driver returns timestamps with a fixed "+0000" zone instead of
// UTC. We normalize them after every read so the JSON stays uniform.
var (
	_ bun.AfterScanRowHook = (*User)(nil)
	_ bun.AfterScanRowHook = (*ProxyHost)(nil)
)

func (u *User) AfterScanRow(context.Context) error {
	u.CreatedAt = u.CreatedAt.UTC()
	return nil
}

func (h *ProxyHost) AfterScanRow(context.Context) error {
	h.CreatedAt, h.UpdatedAt = h.CreatedAt.UTC(), h.UpdatedAt.UTC()
	if h.Locations == nil {
		h.Locations = []ProxyLocation{}
	}
	return nil
}

type Store struct {
	db *bun.DB
}

// Open opens the database and applies the pending migrations. dsn is the path
// of a SQLite file or a postgres:// or mysql:// URL.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, dir, err := connect(dsn)
	if err != nil {
		return nil, err
	}
	if err := migrateUp(ctx, db, dir); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// connect returns the connection and the migrations directory of its engine.
func connect(dsn string) (*bun.DB, string, error) {
	scheme, _, isURL := strings.Cut(dsn, "://")
	switch scheme {
	case "postgres", "postgresql":
		// With the session's clock in UTC, timestamps read back as they were stored.
		conn := pgdriver.NewConnector(pgdriver.WithDSN(dsn), pgdriver.WithConnParams(map[string]any{"timezone": "UTC"}))
		return bun.NewDB(sql.OpenDB(conn), pgdialect.New()), "migrations_postgres", nil
	case "mysql", "mariadb":
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, "", fmt.Errorf("invalid MySQL URL: %w", err)
		}
		cfg := mysql.NewConfig()
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
		cfg.Net, cfg.Addr = "tcp", u.Host
		if u.Port() == "" {
			cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
		}
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
		cfg.TLSConfig = u.Query().Get("tls")
		cfg.ParseTime, cfg.Loc = true, time.UTC
		// Without this, an UPDATE that changes nothing reports 0 rows and
		// mustAffect would read it as "not found".
		cfg.ClientFoundRows = true
		conn, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, "", err
		}
		// DATETIME stores no zone: everything is written and read in UTC.
		return bun.NewDB(sql.OpenDB(conn), mysqldialect.New(mysqldialect.WithTimeLocation("UTC"))), "migrations_mysql", nil
	}
	if isURL {
		return nil, "", fmt.Errorf("database %q is not supported: use postgres:// or mysql://", scheme)
	}
	sqldb, err := sql.Open("sqlite", "file:"+dsn+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, "", err
	}
	// SQLite allows a single writer; this avoids SQLITE_BUSY errors.
	sqldb.SetMaxOpenConns(1)
	return bun.NewDB(sqldb, sqlitedialect.New()), "migrations", nil
}

// sqlite reports whether the data lives in the local file and not on a server.
func (s *Store) sqlite() bool { return s.db.Dialect().Name() == dialect.SQLite }

// External reports whether the database is a separate server (PostgreSQL or
// MySQL): Dockfly does not take its backups.
func (s *Store) External() bool { return !s.sqlite() }

// upsert makes an INSERT update cols if the row already exists. conflict is
// the key that collides (MySQL infers it on its own).
func (s *Store) upsert(q *bun.InsertQuery, conflict string, cols ...string) *bun.InsertQuery {
	set := "? = EXCLUDED.?"
	if s.db.Dialect().Name() == dialect.MySQL {
		q, set = q.On("DUPLICATE KEY UPDATE"), "? = VALUES(?)"
	} else {
		q = q.On("CONFLICT (" + conflict + ") DO UPDATE")
	}
	for _, c := range cols {
		q = q.Set(set, bun.Ident(c), bun.Ident(c))
	}
	return q
}

func (s *Store) Close() error { return s.db.Close() }

func migrateUp(ctx context.Context, db *bun.DB, dir string) error {
	files, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return err
	}
	migrations := migrate.NewMigrations()
	if err := migrations.Discover(files); err != nil {
		return fmt.Errorf("reading migrations: %w", err)
	}
	m := migrate.NewMigrator(db, migrations,
		migrate.WithTableName("schema_migrations"),
		migrate.WithLocksTableName("schema_migration_locks"),
		// A migration that fails is not marked as applied, so it is retried on
		// the next startup.
		migrate.WithMarkAppliedOnSuccess(true),
	)
	if err := m.Init(ctx); err != nil {
		return err
	}
	if err := m.Lock(ctx); err != nil {
		return err
	}
	defer m.Unlock(ctx) //nolint:errcheck
	if db.Dialect().Name() == dialect.SQLite {
		if err := adoptLegacy(ctx, db, m, migrations); err != nil {
			return fmt.Errorf("adopting the older database: %w", err)
		}
	}
	if _, err := m.Migrate(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// adoptLegacy recognizes the databases created before we used Bun, which kept
// the schema version in PRAGMA user_version. Their tables already exist, so
// the initial migration is marked as applied instead of being run (which
// would fail with "table users already exists").
func adoptLegacy(ctx context.Context, db *bun.DB, m *migrate.Migrator, migrations *migrate.Migrations) error {
	var userVersion int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if userVersion == 0 {
		return nil
	}
	applied, err := m.AppliedMigrations(ctx)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		return nil // already adopted on an earlier startup
	}

	// The earliest versions did not have the preserve_host column.
	var hasPreserveHost bool
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) > 0 FROM pragma_table_info('proxy_hosts') WHERE name = 'preserve_host'`,
	).Scan(&hasPreserveHost); err != nil {
		return err
	}
	if !hasPreserveHost {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE proxy_hosts ADD COLUMN preserve_host BOOLEAN NOT NULL DEFAULT TRUE`); err != nil {
			return err
		}
	}

	initial := migrations.Sorted()[0]
	return m.MarkApplied(ctx, &initial)
}

// IsDuplicate reports whether err is the rejection of a repeated value in a
// unique column (each engine words it differently).
func IsDuplicate(err error) bool {
	var pg pgdriver.Error
	var my *mysql.MySQLError
	switch {
	case err == nil:
		return false
	case errors.As(err, &pg):
		return pg.Field('C') == "23505"
	case errors.As(err, &my):
		return my.Number == 1062
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// now returns the current time with the precision SQLite stores through Bun
// (microseconds), so that what we return on create matches exactly what is
// read back afterwards.
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// notFound translates sql.ErrNoRows into this package's own error.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// mustAffect returns ErrNotFound if the operation touched no rows.
func mustAffect(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Users ---

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*User)(nil)).Count(ctx)
	return int(n), err
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	u.CreatedAt = now()
	_, err := s.db.NewInsert().Model(u).Exec(ctx)
	return err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	u := new(User)
	if err := s.db.NewSelect().Model(u).Where("email = ?", email).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	u := &User{ID: id}
	if err := s.db.NewSelect().Model(u).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return u, nil
}

// Roles, from most to least permissions.
const (
	RoleAdmin    = "admin"    // everything, including users and settings
	RoleDeployer = "deployer" // sites, apps, builds and deployments
	RoleViewer   = "viewer"   // read only
)

// RoleRank orders the roles: a role includes the permissions of the lower-ranked ones.
func RoleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleDeployer:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	users := []*User{}
	err := s.db.NewSelect().Model(&users).Order("id").Scan(ctx)
	return users, err
}

// UpdateUserProfile changes the name and the role.
func (s *Store) UpdateUserProfile(ctx context.Context, u *User) error {
	return mustAffect(s.db.NewUpdate().Model(u).Column("name", "role").WherePK().Exec(ctx))
}

// SetPassword changes the password and bumps the session version (which logs
// out the other sessions). It returns the new version.
func (s *Store) SetPassword(ctx context.Context, id int64, hash string) (int64, error) {
	var version int64
	// In a transaction rather than with RETURNING, which MySQL lacks.
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := mustAffect(tx.NewUpdate().Model((*User)(nil)).
			Set("password_hash = ?", hash).Set("session_version = session_version + 1").
			Where("id = ?", id).Exec(ctx)); err != nil {
			return err
		}
		return tx.NewSelect().Model((*User)(nil)).Column("session_version").Where("id = ?", id).Scan(ctx, &version)
	})
	return version, err
}

// BumpSessionVersion bumps the user's session version (which logs out all of
// their sessions). Logout uses it.
func (s *Store) BumpSessionVersion(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewUpdate().Model((*User)(nil)).
		Set("session_version = session_version + 1").Where("id = ?", id).Exec(ctx))
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx))
}

// CountAdmins counts the administrators whose account is already activated.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*User)(nil)).Where("role = ? AND password_hash <> ''", RoleAdmin).Count(ctx)
	return int(n), err
}

// SetUserTOTP saves the two-factor authentication settings.
func (s *Store) SetUserTOTP(ctx context.Context, u *User) error {
	if u.RecoveryCodes == nil {
		u.RecoveryCodes = []string{}
	}
	return mustAffect(s.db.NewUpdate().Model(u).
		Column("totp_secret_enc", "totp_enabled", "totp_last_step", "recovery_codes").WherePK().Exec(ctx))
}

// AdvanceTOTPStep records the step of an accepted code. It returns false if
// that step (or a later one) was already used: two concurrent requests with
// the same code cannot both get in.
func (s *Store) AdvanceTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	res, err := s.db.NewUpdate().Model((*User)(nil)).Set("totp_last_step = ?", step).
		Where("id = ? AND totp_last_step < ?", userID, step).Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UseRecoveryCode spends a recovery code (looked up by its hash).
func (s *Store) UseRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	used := false
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		u := &User{ID: userID}
		if err := tx.NewSelect().Model(u).WherePK().Scan(ctx); err != nil {
			return err
		}
		i := slices.Index(u.RecoveryCodes, hash)
		if i < 0 {
			return nil
		}
		u.RecoveryCodes = slices.Delete(u.RecoveryCodes, i, i+1)
		used = true
		_, err := tx.NewUpdate().Model(u).Column("recovery_codes").WherePK().Exec(ctx)
		return err
	})
	return used, err
}

// --- Proxy hosts ---

func (s *Store) ListProxyHosts(ctx context.Context) ([]*ProxyHost, error) {
	hosts := []*ProxyHost{}
	err := s.db.NewSelect().Model(&hosts).Order("id").Scan(ctx)
	return hosts, err
}

func (s *Store) GetProxyHost(ctx context.Context, id int64) (*ProxyHost, error) {
	h := &ProxyHost{ID: id}
	if err := s.db.NewSelect().Model(h).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return h, nil
}

func (s *Store) CreateProxyHost(ctx context.Context, h *ProxyHost) error {
	h.CreatedAt = now()
	h.UpdatedAt = h.CreatedAt
	_, err := s.db.NewInsert().Model(h).Exec(ctx)
	return err
}

// UpdateProxyHost rewrites every editable column. Unlike GORM, Bun includes
// the zero values (false, 0, ""), so disabling a host or turning off its SSL
// is stored correctly.
func (s *Store) UpdateProxyHost(ctx context.Context, h *ProxyHost) error {
	h.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(h).WherePK().ExcludeColumn("created_at").Exec(ctx))
}

func (s *Store) DeleteProxyHost(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*ProxyHost)(nil)).Where("id = ?", id).Exec(ctx))
}

// Snapshot writes a consistent copy of the database to path, including the
// changes still sitting in the WAL (copying dockfly.db by hand would lose
// them). With PostgreSQL or MySQL it returns errors.ErrUnsupported: such a
// database is backed up with its server's own tools.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	if !s.sqlite() {
		return errors.ErrUnsupported
	}
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}
