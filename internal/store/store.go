// Package store persiste el estado de Dockfly con Bun: en SQLite (por
// defecto), PostgreSQL o MySQL/MariaDB.
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
	_ "modernc.org/sqlite" // driver SQLite en Go puro: sin CGO
)

var ErrNotFound = errors.New("no encontrado")

// migrationsFS contiene las migraciones SQL versionadas, una carpeta por
// motor. Nunca edites una migración publicada: añade un archivo nuevo con un
// timestamp posterior, en las tres carpetas.
//
//go:embed migrations/*.sql migrations_postgres/*.sql migrations_mysql/*.sql
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
	PasswordHash string    `json:"-"` // vacío hasta que acepta la invitación
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	// SessionVersion va en cada sesión: al subirla, las anteriores dejan de valer.
	SessionVersion int64 `json:"-"`

	// Verificación en dos pasos. El secreto va cifrado; mientras se configura
	// existe pero TOTPEnabled sigue a false.
	TOTPSecretEnc []byte   `bun:"totp_secret_enc" json:"-"`
	TOTPEnabled   bool     `bun:"totp_enabled" json:"totp_enabled"`
	TOTPLastStep  int64    `bun:"totp_last_step" json:"-"`
	RecoveryCodes []string `bun:"recovery_codes" json:"-"`
}

type ProxyHost struct {
	bun.BaseModel `bun:"table:proxy_hosts"`

	ID            int64    `bun:",pk,autoincrement" json:"id"`
	Domains       []string `json:"domains"` // Bun guarda los slices como JSON
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
	// Locations envían una parte del dominio (/api…) a otro destino.
	Locations []ProxyLocation `json:"locations"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Acceso de una ubicación: el de su proxy host o sitio, sin restricciones o una lista.
const (
	LocationAccessInherit = "inherit"
	LocationAccessPublic  = "public"
	LocationAccessList    = "list"
)

// ProxyLocation es una ubicación personalizada de un proxy host o de un sitio.
type ProxyLocation struct {
	Path          string `json:"path"` // "/api"
	TargetType    string `json:"target_type"`
	ForwardScheme string `json:"forward_scheme"`
	ForwardHost   string `json:"forward_host"`
	ForwardPort   int    `json:"forward_port"`
	// StripPrefix quita Path antes de reenviar (/api/users → /users).
	StripPrefix  bool   `json:"strip_prefix"`
	Access       string `json:"access"`
	AccessListID *int64 `json:"access_list_id"`
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
	if h.Locations == nil {
		h.Locations = []ProxyLocation{}
	}
	return nil
}

type Store struct {
	db *bun.DB
}

// Open abre la base de datos y aplica las migraciones pendientes. dsn es la
// ruta de un archivo SQLite o una URL postgres:// o mysql://.
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

// connect devuelve la conexión y la carpeta de migraciones de su motor.
func connect(dsn string) (*bun.DB, string, error) {
	scheme, _, isURL := strings.Cut(dsn, "://")
	switch scheme {
	case "postgres", "postgresql":
		// Con el reloj de la sesión en UTC las fechas se leen como se guardaron.
		conn := pgdriver.NewConnector(pgdriver.WithDSN(dsn), pgdriver.WithConnParams(map[string]any{"timezone": "UTC"}))
		return bun.NewDB(sql.OpenDB(conn), pgdialect.New()), "migrations_postgres", nil
	case "mysql", "mariadb":
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, "", fmt.Errorf("URL de MySQL inválida: %w", err)
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
		// Sin esto un UPDATE que no cambia nada cuenta 0 filas y mustAffect
		// lo tomaría por "no encontrado".
		cfg.ClientFoundRows = true
		conn, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, "", err
		}
		// DATETIME no guarda zona: todo se escribe y se lee en UTC.
		return bun.NewDB(sql.OpenDB(conn), mysqldialect.New(mysqldialect.WithTimeLocation("UTC"))), "migrations_mysql", nil
	}
	if isURL {
		return nil, "", fmt.Errorf("base de datos %q no admitida: usa postgres:// o mysql://", scheme)
	}
	sqldb, err := sql.Open("sqlite", "file:"+dsn+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, "", err
	}
	// SQLite admite un solo escritor; evita errores SQLITE_BUSY.
	sqldb.SetMaxOpenConns(1)
	return bun.NewDB(sqldb, sqlitedialect.New()), "migrations", nil
}

// sqlite indica si los datos están en el archivo local y no en un servidor.
func (s *Store) sqlite() bool { return s.db.Dialect().Name() == dialect.SQLite }

// External indica si la base de datos es un servidor aparte (PostgreSQL o
// MySQL): sus copias de seguridad no las hace Dockfly.
func (s *Store) External() bool { return !s.sqlite() }

// upsert hace que un INSERT actualice cols si la fila ya existe. conflict es
// la clave que choca (MySQL la deduce sola).
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
	if db.Dialect().Name() == dialect.SQLite {
		if err := adoptLegacy(ctx, db, m, migrations); err != nil {
			return fmt.Errorf("adoptando base de datos anterior: %w", err)
		}
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

// IsDuplicate indica si err es el rechazo de un valor repetido en una columna
// única (cada motor lo dice a su manera).
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

// Roles, de más a menos permisos.
const (
	RoleAdmin    = "admin"    // todo, incluidos usuarios y ajustes
	RoleDeployer = "deployer" // sitios, apps, builds y despliegues
	RoleViewer   = "viewer"   // solo lectura
)

// RoleRank ordena los roles: un rol incluye los permisos de los de menor rango.
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

// UpdateUserProfile cambia el nombre y el rol.
func (s *Store) UpdateUserProfile(ctx context.Context, u *User) error {
	return mustAffect(s.db.NewUpdate().Model(u).Column("name", "role").WherePK().Exec(ctx))
}

// SetPassword cambia la contraseña y sube la versión de sesión (cierra las
// demás sesiones). Devuelve la versión nueva.
func (s *Store) SetPassword(ctx context.Context, id int64, hash string) (int64, error) {
	var version int64
	// En una transacción y no con RETURNING, que MySQL no tiene.
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

// BumpSessionVersion sube la versión de sesión del usuario (cierra todas sus
// sesiones). Lo usa el logout.
func (s *Store) BumpSessionVersion(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewUpdate().Model((*User)(nil)).
		Set("session_version = session_version + 1").Where("id = ?", id).Exec(ctx))
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx))
}

// CountAdmins cuenta los administradores con la cuenta activada.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*User)(nil)).Where("role = ? AND password_hash <> ''", RoleAdmin).Count(ctx)
	return int(n), err
}

// SetUserTOTP guarda la configuración de la verificación en dos pasos.
func (s *Store) SetUserTOTP(ctx context.Context, u *User) error {
	if u.RecoveryCodes == nil {
		u.RecoveryCodes = []string{}
	}
	return mustAffect(s.db.NewUpdate().Model(u).
		Column("totp_secret_enc", "totp_enabled", "totp_last_step", "recovery_codes").WherePK().Exec(ctx))
}

// AdvanceTOTPStep registra el paso de un código aceptado. Devuelve false si
// ya se usó ese paso (o uno posterior): dos peticiones simultáneas con el
// mismo código no pueden entrar las dos.
func (s *Store) AdvanceTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	res, err := s.db.NewUpdate().Model((*User)(nil)).Set("totp_last_step = ?", step).
		Where("id = ? AND totp_last_step < ?", userID, step).Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UseRecoveryCode gasta un código de recuperación (por su hash).
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
// los perdería). Con PostgreSQL o MySQL devuelve errors.ErrUnsupported: esa
// base de datos se copia con las herramientas de su servidor.
func (s *Store) Snapshot(ctx context.Context, path string) error {
	if !s.sqlite() {
		return errors.ErrUnsupported
	}
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}
