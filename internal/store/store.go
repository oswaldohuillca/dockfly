// Package store persiste el estado de Dockfly en SQLite usando Bun.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"
	_ "modernc.org/sqlite" // driver SQLite en Go puro: sin CGO
)

var ErrNotFound = errors.New("no encontrado")

// migrationsFS contiene las migraciones SQL versionadas. Nunca edites una
// migración publicada: añade un archivo nuevo con un timestamp posterior.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Tipos de destino de un proxy host.
const (
	TargetDocker   = "docker"   // contenedor Docker (por nombre)
	TargetHost     = "host"     // aplicación en el mismo servidor
	TargetExternal = "external" // IP o dominio externo
)

type User struct {
	bun.BaseModel `bun:"table:users"`

	ID           int64     `bun:",pk,autoincrement" json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type ProxyHost struct {
	bun.BaseModel `bun:"table:proxy_hosts"`

	ID            int64     `bun:",pk,autoincrement" json:"id"`
	Domains       []string  `json:"domains"` // Bun guarda los slices como JSON
	TargetType    string    `json:"target_type"`
	ForwardScheme string    `json:"forward_scheme"`
	ForwardHost   string    `json:"forward_host"`
	ForwardPort   int       `json:"forward_port"`
	SSL           bool      `bun:"ssl" json:"ssl"`
	ForceHTTPS    bool      `bun:"force_https" json:"force_https"`
	HSTS          bool      `bun:"hsts" json:"hsts"`
	SkipTLSVerify bool      `bun:"skip_tls_verify" json:"skip_tls_verify"`
	PreserveHost  bool      `json:"preserve_host"`
	Enabled       bool      `json:"enabled"`
	AccessListID  *int64    `json:"access_list_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// El driver de SQLite devuelve las fechas con una zona fija "+0000" en vez de
// UTC. Las normalizamos tras cada lectura para que el JSON sea uniforme.
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
	return nil
}

type Store struct {
	db *bun.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	sqldb, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite admite un solo escritor; evita errores SQLITE_BUSY.
	sqldb.SetMaxOpenConns(1)

	db := bun.NewDB(sqldb, sqlitedialect.New())
	if err := migrateUp(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrateUp(ctx context.Context, db *bun.DB) error {
	migrations := migrate.NewMigrations()
	if err := migrations.Discover(migrationsFS); err != nil {
		return fmt.Errorf("leyendo migraciones: %w", err)
	}
	m := migrate.NewMigrator(db, migrations,
		migrate.WithTableName("schema_migrations"),
		migrate.WithLocksTableName("schema_migration_locks"),
		// Si una migración falla no se marca como aplicada, y se reintenta en
		// el siguiente arranque.
		migrate.WithMarkAppliedOnSuccess(true),
	)
	if err := m.Init(ctx); err != nil {
		return err
	}
	if err := m.Lock(ctx); err != nil {
		return err
	}
	defer m.Unlock(ctx) //nolint:errcheck
	if err := adoptLegacy(ctx, db, m, migrations); err != nil {
		return fmt.Errorf("adoptando base de datos anterior: %w", err)
	}
	if _, err := m.Migrate(ctx); err != nil {
		return fmt.Errorf("aplicando migraciones: %w", err)
	}
	return nil
}

// adoptLegacy reconoce las bases de datos creadas antes de usar Bun, que
// llevaban la versión del esquema en PRAGMA user_version. Sus tablas ya
// existen, así que la migración inicial se marca como aplicada en lugar de
// ejecutarla (lo que fallaría con "table users already exists").
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
		return nil // ya adoptada en un arranque anterior
	}

	// Las primeras versiones no tenían la columna preserve_host.
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

// now devuelve la hora actual con la precisión que guarda SQLite vía Bun
// (microsegundos), para que lo que devolvemos al crear coincida exactamente
// con lo que se lee después.
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// notFound traduce sql.ErrNoRows al error propio del paquete.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// mustAffect devuelve ErrNotFound si la operación no tocó ninguna fila.
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

// --- Usuarios ---

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

// UpdateProxyHost reescribe todas las columnas editables. A diferencia de
// GORM, Bun incluye los valores cero (false, 0, ""), así que desactivar un
// host o quitarle el SSL se guarda correctamente.
func (s *Store) UpdateProxyHost(ctx context.Context, h *ProxyHost) error {
	h.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(h).WherePK().ExcludeColumn("created_at").Exec(ctx))
}

func (s *Store) DeleteProxyHost(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*ProxyHost)(nil)).Where("id = ?", id).Exec(ctx))
}

// Snapshot escribe en path una copia consistente de la base de datos,
// incluidos los cambios que aún están en el WAL (copiar dockfly.db a mano
// los perdería).
func (s *Store) Snapshot(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}
