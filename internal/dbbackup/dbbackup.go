// Package dbbackup backs up the database apps (PostgreSQL, MySQL/MariaDB and
// Redis) and restores them.
//
// The backup is taken with the engine's own tool inside the app's container
// (pg_dumpall, mysqldump, redis-cli), compressed and uploaded to the
// configured storage (S3 or disk).
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

// Supported engines.
const (
	Postgres = "postgres"
	MySQL    = "mysql"
	Redis    = "redis"
)

// Engine works out the engine from an app's image ("" if it is not a known
// database).
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

// dumpCommand dumps the whole database to standard output. It uses the
// environment variables the official image created the user with.
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

// restoreCommand reads a dump from standard input (nil if the engine cannot
// be restored while running).
func restoreCommand(engine string) []string {
	switch engine {
	case Postgres:
		// ON_ERROR_STOP=0: pg_dumpall --clean always tries to drop and create
		// the role it connects with, and that fails harmlessly.
		return []string{"sh", "-c", `exec psql -q -v ON_ERROR_STOP=0 -U "${POSTGRES_USER:-postgres}" -d postgres`}
	case MySQL:
		return []string{"sh", "-c", `C=mysql; command -v mariadb >/dev/null && C=mariadb
export MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"
exec "$C" -uroot`}
	}
	return nil
}

// CanRestore says whether Dockfly knows how to restore that engine (Redis
// needs the server stopped to swap its file: it is restored by hand).
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
	// DockerFor, if not nil, gives the Docker of each app (the ones on remote
	// servers go through their agent's tunnel).
	DockerFor func(app *store.App) *docker.Client
	// Storage gives the storage configured right now (it can change).
	Storage func(ctx context.Context) (objstore.Store, error)
	TmpDir  string
	Log     *slog.Logger
	// OnFinish, if not nil, is called when each backup finishes (notifications).
	OnFinish func(ctx context.Context, app *store.App, b *store.DBBackup)

	mu      sync.Mutex
	running map[int64]bool // apps with a backup or a restore under way
}

// ErrBusy means the app already has a backup or a restore under way.
var ErrBusy = errors.New("this database already has a backup or a restore under way")

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

// container returns an app's running container.
func (m *Manager) container(ctx context.Context, app *store.App) (string, error) {
	if app.Source == store.AppSourceCompose {
		return "", errors.New("automatic backups are for apps from an image (postgres, mysql, redis…)")
	}
	if app.CurrentDeploymentID == nil || app.Stopped {
		return "", errors.New("the database is not running")
	}
	d, err := m.Store.GetAppDeployment(ctx, *app.CurrentDeploymentID)
	if err != nil {
		return "", err
	}
	return d.Container, nil
}

// Start creates the backup and takes it in the background. It returns the
// record (in the running state).
func (m *Manager) Start(ctx context.Context, app *store.App, trigger string) (*store.DBBackup, error) {
	engine := Engine(app.Image)
	if engine == "" {
		return nil, fmt.Errorf("%s is not a known database (postgres, mysql, mariadb, redis)", app.Image)
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
	// The running backup works on its own copy of the record: the one returned
	// is read (and serialized) by the caller at the same time.
	work := *b
	go func() {
		defer m.unlock(app.ID)
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Hour)
		defer cancel()
		m.run(bg, app, &work)
	}()
	return b, nil
}

func (m *Manager) run(ctx context.Context, app *store.App, b *store.DBBackup) {
	err := m.backup(ctx, app, b)
	if err != nil {
		b.Status, b.Error = store.DBBackupFailed, err.Error()
		m.Log.Warn("database backup failed", "app", app.Name, "error", err)
	} else {
		b.Status = store.DBBackupSuccess
	}
	if err := m.Store.FinishDBBackup(ctx, b); err != nil {
		m.Log.Error("saving the backup result", "app", app.Name, "error", err)
	}
	if b.Status == store.DBBackupSuccess {
		m.prune(ctx, app)
	}
	if m.OnFinish != nil {
		m.OnFinish(ctx, app, b)
	}
}

// limitedBuffer keeps the beginning of stderr to explain a failure.
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
	// It is dumped to a compressed temporary file: S3 needs to know the size,
	// and this way a failed upload does not keep the database locked meanwhile.
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
		return fmt.Errorf("running the dump: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("the dump exited with code %d: %s", code, strings.TrimSpace(stderr.b.String()))
	}
	if err := gz.Close(); err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if size < 64 {
		return errors.New("the dump is empty")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// With the ID: two backups in the same second do not clobber each other.
	key := fmt.Sprintf("%s/%s-%s-%d.%s", app.Name, app.Name, b.CreatedAt.UTC().Format("20060102-150405"), b.ID, extension(b.Engine))
	if err := storage.Put(ctx, key, tmp, size); err != nil {
		return fmt.Errorf("uploading the backup to %s: %w", storage.Describe(), err)
	}
	b.Location, b.ObjectKey, b.Size = storage.Describe(), key, size
	return nil
}

// prune deletes the backups left over by the policy.
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
			m.Log.Warn("deleting an old backup", "app", app.Name, "backup", b.ID, "error", err)
		}
	}
	// Failures are only kept to be looked at: the last 20 are enough.
	failed := 0
	for _, b := range list {
		if b.Status == store.DBBackupFailed {
			if failed++; failed > 20 {
				_ = m.Store.DeleteDBBackup(ctx, b.ID)
			}
		}
	}
}

// Delete deletes a backup and its file.
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

// Open opens a backup (still compressed) to download it.
func (m *Manager) Open(ctx context.Context, b *store.DBBackup) (io.ReadCloser, error) {
	if b.Status != store.DBBackupSuccess {
		return nil, errors.New("this backup did not finish successfully")
	}
	storage, err := m.Storage(ctx)
	if err != nil {
		return nil, err
	}
	if storage.Describe() != b.Location {
		return nil, fmt.Errorf("the backup is in %s, which is no longer the configured storage", b.Location)
	}
	return storage.Get(ctx, b.ObjectKey)
}

// Restore loads a backup into the app's database (replacing its data). First
// it backs up what is there, in case it has to be rolled back.
func (m *Manager) Restore(ctx context.Context, app *store.App, b *store.DBBackup) error {
	cmd := restoreCommand(b.Engine)
	if cmd == nil {
		return fmt.Errorf("%s backups are restored by hand: download it and follow the engine's guide", b.Engine)
	}
	if !m.lock(app.ID) {
		return ErrBusy
	}
	defer m.unlock(app.ID)
	// A restore cut off halfway leaves the database half written: it is not
	// canceled just because the browser closed the request.
	ctx = context.WithoutCancel(ctx)
	ctr, err := m.container(ctx, app)
	if err != nil {
		return err
	}
	// No prune: it could delete the very backup about to be restored.
	safety := &store.DBBackup{AppID: app.ID, Status: store.DBBackupRunning, Trigger: store.TriggerManual, Engine: b.Engine}
	if err := m.Store.CreateDBBackup(ctx, safety); err != nil {
		return err
	}
	if err := m.backup(ctx, app, safety); err != nil {
		safety.Status, safety.Error = store.DBBackupFailed, err.Error()
		_ = m.Store.FinishDBBackup(ctx, safety)
		return fmt.Errorf("the current data could not be backed up first, so nothing is restored: %w", err)
	}
	safety.Status = store.DBBackupSuccess
	if err := m.Store.FinishDBBackup(ctx, safety); err != nil {
		return err
	}
	rc, err := m.Open(ctx, b)
	if err != nil {
		return err
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("the backup is damaged: %w", err)
	}
	var stderr limitedBuffer
	code, err := m.docker(app).ExecStream(ctx, ctr, cmd, gz, io.Discard, &stderr)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the restore exited with code %d: %s", code, strings.TrimSpace(stderr.b.String()))
	}
	return nil
}

// Run takes the scheduled backups. Every minute it checks which databases
// are due.
func (m *Manager) Run(ctx context.Context) {
	if err := m.Store.FailRunningDBBackups(ctx); err != nil {
		m.Log.Error("checking interrupted backups", "error", err)
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
		// After a failure it retries in an hour, not on the next minute.
		if last != nil && last.Status == store.DBBackupFailed {
			interval = min(interval, time.Hour)
		}
		if last != nil && (last.Status == store.DBBackupRunning || time.Since(last.CreatedAt) < interval) {
			continue
		}
		app, err := m.Store.GetApp(ctx, p.AppID)
		if err != nil || app.Stopped || app.CurrentDeploymentID == nil {
			continue // stopped: it will be backed up once it runs again
		}
		if _, err := m.Start(ctx, app, "schedule"); err != nil && !errors.Is(err, ErrBusy) {
			m.Log.Warn("could not start the scheduled backup", "app", app.Name, "error", err)
		}
	}
}
