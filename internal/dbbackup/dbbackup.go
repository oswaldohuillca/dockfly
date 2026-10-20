// Package dbbackup hace copias de seguridad de las apps de bases de datos
// (PostgreSQL, MySQL/MariaDB y Redis) y las restaura.
//
// La copia se hace con la propia herramienta del motor dentro del
// contenedor de la app (pg_dumpall, mysqldump, redis-cli), se comprime y se
// sube al almacenamiento configurado (S3 o disco).
package dbbackup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/objstore"
	"github.com/dockfly/dockfly/internal/store"
)

// Motores soportados.
const (
	Postgres = "postgres"
	MySQL    = "mysql"
	Redis    = "redis"
)

// Engine deduce el motor de la imagen de una app ("" si no es una base de
// datos conocida).
func Engine(image string) string {
	name := strings.ToLower(image)
	if i := strings.LastIndex(name, "@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		name = name[:i]
	}
	name = name[strings.LastIndex(name, "/")+1:]
	switch {
	case name == "postgres" || name == "postgresql" || name == "postgis" || name == "timescaledb" || strings.HasPrefix(name, "pgvector"):
		return Postgres
	case name == "mysql" || name == "mariadb" || name == "percona-server":
		return MySQL
	case name == "redis" || name == "valkey" || name == "redis-stack-server":
		return Redis
	}
	return ""
}

// dumpCommand vuelca la base de datos entera a la salida estándar. Usa las
// variables de entorno con las que la imagen oficial creó el usuario.
func dumpCommand(engine string) []string {
	switch engine {
	case Postgres:
		return []string{"sh", "-c", `exec pg_dumpall --clean --if-exists -U "${POSTGRES_USER:-postgres}"`}
	case MySQL:
		return []string{"sh", "-c", `D=mysqldump; command -v mariadb-dump >/dev/null && D=mariadb-dump
export MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"
exec "$D" -uroot --all-databases --single-transaction --routines --events --triggers`}
	case Redis:
		return []string{"sh", "-c", `F=/tmp/dockfly-$$.rdb
redis-cli ${REDIS_PASSWORD:+-a "$REDIS_PASSWORD" --no-auth-warning} --rdb "$F" >&2 && cat "$F"; S=$?; rm -f "$F"; exit $S`}
	}
	return nil
}

// restoreCommand lee un volcado de la entrada estándar (nil si el motor no
// se puede restaurar en caliente).
func restoreCommand(engine string) []string {
	switch engine {
	case Postgres:
		return []string{"sh", "-c", `exec psql -q -v ON_ERROR_STOP=0 -U "${POSTGRES_USER:-postgres}" -d postgres`}
	case MySQL:
		return []string{"sh", "-c", `C=mysql; command -v mariadb >/dev/null && C=mariadb
export MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"
exec "$C" -uroot`}
	}
	return nil
}

// CanRestore indica si Dockfly sabe restaurar ese motor (Redis necesita
// parar el servidor para cambiar su archivo: se restaura a mano).
func CanRestore(engine string) bool { return restoreCommand(engine) != nil }

func extension(engine string) string {
	if engine == Redis {
		return "rdb.gz"
	}
	return "sql.gz"
}

type Manager struct {
	Store  *store.Store
	Docker *docker.Client
	// DockerFor, si no es nil, da el Docker de cada app (las de servidores
	// remotos van por el túnel de su agente).
	DockerFor func(app *store.App) *docker.Client
	// Storage da el almacenamiento configurado ahora mismo (puede cambiar).
	Storage func(ctx context.Context) (objstore.Store, error)
	TmpDir  string
	Log     *slog.Logger
	// OnFinish, si no es nil, se llama al terminar cada copia (notificaciones).
	OnFinish func(ctx context.Context, app *store.App, b *store.DBBackup)

	mu      sync.Mutex
	running map[int64]bool // apps con una copia o restauración en marcha
}

// ErrBusy indica que la app ya tiene una copia o restauración en marcha.
var ErrBusy = errors.New("ya hay una copia o una restauración en marcha para esta base de datos")

func (m *Manager) lock(appID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == nil {
		m.running = map[int64]bool{}
	}
	if m.running[appID] {
		return false
	}
	m.running[appID] = true
	return true
}

func (m *Manager) unlock(appID int64) {
	m.mu.Lock()
	delete(m.running, appID)
	m.mu.Unlock()
}

func (m *Manager) docker(app *store.App) *docker.Client {
	if m.DockerFor != nil {
		return m.DockerFor(app)
	}
	return m.Docker
}

// container devuelve el contenedor en marcha de una app.
func (m *Manager) container(ctx context.Context, app *store.App) (string, error) {
	if app.Source == store.AppSourceCompose {
		return "", errors.New("las copias automáticas son para apps desde una imagen (postgres, mysql, redis…)")
	}
	if app.CurrentDeploymentID == nil || app.Stopped {
		return "", errors.New("la base de datos no está en marcha")
	}
	d, err := m.Store.GetAppDeployment(ctx, *app.CurrentDeploymentID)
	if err != nil {
		return "", err
	}
	return d.Container, nil
}

// Start crea la copia y la hace en segundo plano. Devuelve el registro (en
// estado running).
func (m *Manager) Start(ctx context.Context, app *store.App, trigger string) (*store.DBBackup, error) {
	engine := Engine(app.Image)
	if engine == "" {
		return nil, fmt.Errorf("%s no es una base de datos conocida (postgres, mysql, mariadb, redis)", app.Image)
	}
	if _, err := m.container(ctx, app); err != nil {
		return nil, err
	}
	if !m.lock(app.ID) {
		return nil, ErrBusy
	}
	b := &store.DBBackup{AppID: app.ID, Status: store.DBBackupRunning, Trigger: trigger, Engine: engine}
	if err := m.Store.CreateDBBackup(ctx, b); err != nil {
		m.unlock(app.ID)
		return nil, err
	}
	go func() {
		defer m.unlock(app.ID)
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Hour)
		defer cancel()
		m.run(bg, app, b)
	}()
	return b, nil
}

func (m *Manager) run(ctx context.Context, app *store.App, b *store.DBBackup) {
	err := m.backup(ctx, app, b)
	if err != nil {
		b.Status, b.Error = store.DBBackupFailed, err.Error()
		m.Log.Warn("copia de la base de datos fallida", "app", app.Name, "error", err)
	} else {
		b.Status = store.DBBackupSuccess
	}
	if err := m.Store.FinishDBBackup(ctx, b); err != nil {
		m.Log.Error("guardando el resultado de la copia", "app", app.Name, "error", err)
	}
	if b.Status == store.DBBackupSuccess {
		m.prune(ctx, app)
	}
	if m.OnFinish != nil {
		m.OnFinish(ctx, app, b)
	}
}

// limitedBuffer guarda el principio de stderr para explicar un fallo.
type limitedBuffer struct{ b strings.Builder }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 4096 - l.b.Len(); room > 0 {
		l.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (m *Manager) backup(ctx context.Context, app *store.App, b *store.DBBackup) error {
	ctr, err := m.container(ctx, app)
	if err != nil {
		return err
	}
	storage, err := m.Storage(ctx)
	if err != nil {
		return err
	}
	// Se vuelca a un temporal comprimido: S3 necesita conocer el tamaño, y
	// así una subida fallida no deja la base de datos bloqueada mientras tanto.
	if err := os.MkdirAll(m.TmpDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.TmpDir, "dbbackup-*.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	gz := gzip.NewWriter(tmp)
	var stderr limitedBuffer
	code, err := m.docker(app).ExecStream(ctx, ctr, dumpCommand(b.Engine), nil, gz, &stderr)
	if err != nil {
		return fmt.Errorf("ejecutando el volcado: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("el volcado terminó con código %d: %s", code, strings.TrimSpace(stderr.b.String()))
	}
	if err := gz.Close(); err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if size < 64 {
		return errors.New("el volcado está vacío")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%s-%s.%s", app.Name, app.Name, b.CreatedAt.UTC().Format("20060102-150405"), extension(b.Engine))
	if err := storage.Put(ctx, key, tmp, size); err != nil {
		return fmt.Errorf("subiendo la copia a %s: %w", storage.Describe(), err)
	}
	b.Location, b.ObjectKey, b.Size = storage.Describe(), key, size
	return nil
}

// prune borra las copias que sobran según la política.
func (m *Manager) prune(ctx context.Context, app *store.App) {
	p, err := m.Store.GetDBBackupPolicy(ctx, app.ID)
	if err != nil || p.Keep < 1 {
		return
	}
	list, err := m.Store.ListDBBackups(ctx, app.ID)
	if err != nil {
		return
	}
	kept := 0
	for _, b := range list {
		if b.Status != store.DBBackupSuccess {
			continue
		}
		if kept++; kept <= p.Keep {
			continue
		}
		if err := m.Delete(ctx, b); err != nil {
			m.Log.Warn("borrando una copia antigua", "app", app.Name, "copia", b.ID, "error", err)
		}
	}
	// Los fallos solo se guardan para verlos: bastan los 20 últimos.
	failed := 0
	for _, b := range list {
		if b.Status == store.DBBackupFailed {
			if failed++; failed > 20 {
				_ = m.Store.DeleteDBBackup(ctx, b.ID)
			}
		}
	}
}

// Delete borra una copia y su archivo.
func (m *Manager) Delete(ctx context.Context, b *store.DBBackup) error {
	if b.ObjectKey != "" {
		storage, err := m.Storage(ctx)
		if err != nil {
			return err
		}
		if storage.Describe() == b.Location {
			if err := storage.Delete(ctx, b.ObjectKey); err != nil && !errors.Is(err, objstore.ErrNotFound) {
				return err
			}
		}
	}
	return m.Store.DeleteDBBackup(ctx, b.ID)
}

// Open abre una copia (comprimida) para descargarla.
func (m *Manager) Open(ctx context.Context, b *store.DBBackup) (io.ReadCloser, error) {
	if b.Status != store.DBBackupSuccess {
		return nil, errors.New("esta copia no terminó bien")
	}
	storage, err := m.Storage(ctx)
	if err != nil {
		return nil, err
	}
	if storage.Describe() != b.Location {
		return nil, fmt.Errorf("la copia está en %s, que ya no es el almacenamiento configurado", b.Location)
	}
	return storage.Get(ctx, b.ObjectKey)
}

// Restore vuelca una copia en la base de datos de la app (sustituye sus datos).
func (m *Manager) Restore(ctx context.Context, app *store.App, b *store.DBBackup) error {
	cmd := restoreCommand(b.Engine)
	if cmd == nil {
		return fmt.Errorf("las copias de %s se restauran a mano: descárgala y sigue la guía del motor", b.Engine)
	}
	if !m.lock(app.ID) {
		return ErrBusy
	}
	defer m.unlock(app.ID)
	ctr, err := m.container(ctx, app)
	if err != nil {
		return err
	}
	rc, err := m.Open(ctx, b)
	if err != nil {
		return err
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("la copia está dañada: %w", err)
	}
	var stderr limitedBuffer
	code, err := m.docker(app).ExecStream(ctx, ctr, cmd, gz, io.Discard, &stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("la restauración terminó con código %d: %s", code, strings.TrimSpace(stderr.b.String()))
	}
	return nil
}

// Run hace las copias programadas. Revisa cada minuto qué bases de datos
// tocan.
func (m *Manager) Run(ctx context.Context) {
	if err := m.Store.FailRunningDBBackups(ctx); err != nil {
		m.Log.Error("revisando copias interrumpidas", "error", err)
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		m.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) tick(ctx context.Context) {
	policies, err := m.Store.EnabledDBBackupPolicies(ctx)
	if err != nil {
		return
	}
	for _, p := range policies {
		last, err := m.Store.LastDBBackup(ctx, p.AppID)
		if err != nil {
			continue
		}
		interval := time.Duration(max(p.IntervalHours, 1)) * time.Hour
		// Tras un fallo se reintenta en una hora, no en el siguiente minuto.
		if last != nil && last.Status == store.DBBackupFailed {
			interval = min(interval, time.Hour)
		}
		if last != nil && (last.Status == store.DBBackupRunning || time.Since(last.CreatedAt) < interval) {
			continue
		}
		app, err := m.Store.GetApp(ctx, p.AppID)
		if err != nil || app.Stopped || app.CurrentDeploymentID == nil {
			continue // parada: se copiará cuando vuelva a estar en marcha
		}
		if _, err := m.Start(ctx, app, "schedule"); err != nil && !errors.Is(err, ErrBusy) {
			m.Log.Warn("no se pudo empezar la copia programada", "app", app.Name, "error", err)
		}
	}
}
