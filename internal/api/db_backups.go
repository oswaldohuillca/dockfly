package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/dbbackup"
	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/objstore"
	"github.com/dockfly/dockfly/internal/store"
)

// --- Almacenamiento de las copias (S3 o disco) ---

const settingBackupStorage = "backup_storage"

var (
	errBackupEndpointInvalid = newError("backup_storage_endpoint_invalid", "The endpoint must be a URL, e.g. https://s3.eu-west-1.amazonaws.com")
	errBackupMissingKeys     = newError("backup_storage_missing_credentials", "The bucket or the access keys are missing")
	errBackupWriteFailed     = newError("backup_storage_write_failed", "Couldn't write to the bucket: {detail}")
	errBackupStorageType     = newError("backup_storage_type_unknown", "Unknown storage type: use local or s3")
	errDBBackupsNoDocker     = newError("db_backups_docker_unavailable", "Docker isn't available")
	errDBBackupUnsupported   = newError("db_backup_unsupported", "Automatic backups are for database apps from an image (postgres, mysql, mariadb, redis)")
	errDBBackupPolicyInvalid = newError("db_backup_policy_invalid", "The interval must be between 1 hour and 30 days, and you can keep 1 to 365 backups")
	errDBBackupBusy          = newError("db_backup_busy", "A backup or restore is already running for this database")
	errDBBackupInvalidID     = newError("db_backup_invalid_id", "Invalid backup ID")
	errDBBackupNotFound      = newError("db_backup_not_found", "Backup not found")
	errDBBackupRestoreFailed = newError("db_backup_restore_failed", "Couldn't restore: {detail}")
	errDBBackupStillRunning  = newError("db_backup_running", "The backup is still running")
)

// backupStorageConfig se guarda cifrado en los ajustes.
type backupStorageConfig struct {
	Type      string `json:"type"` // "local" | "s3"
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func (s *Server) backupStorageConfig(ctx context.Context) (backupStorageConfig, error) {
	cfg := backupStorageConfig{Type: "local"}
	raw, err := s.Store.GetSetting(ctx, settingBackupStorage)
	if err != nil || raw == "" {
		return cfg, err
	}
	sealed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return cfg, err
	}
	plain, err := s.Secrets.Open(sealed)
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(plain, &cfg)
}

func (c backupStorageConfig) store(localDir string) objstore.Store {
	if c.Type == "s3" {
		return &objstore.S3{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, AccessKey: c.AccessKey, SecretKey: c.SecretKey}
	}
	return &objstore.Local{Dir: localDir}
}

func (s *Server) localBackupDir() string { return path.Join(s.DataDir, "db-backups") }

// BackupStorage es el almacenamiento configurado ahora mismo.
func (s *Server) BackupStorage(ctx context.Context) (objstore.Store, error) {
	cfg, err := s.backupStorageConfig(ctx)
	if err != nil {
		return nil, err
	}
	return cfg.store(s.localBackupDir()), nil
}

func (s *Server) handleGetBackupStorage(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.backupStorageConfig(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if cfg.SecretKey != "" {
		cfg.SecretKey = secretMask
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg, "location": cfg.store(s.localBackupDir()).Describe()})
}

func (s *Server) handlePutBackupStorage(w http.ResponseWriter, r *http.Request) {
	var in backupStorageConfig
	if !decode(w, r, &in) {
		return
	}
	old, err := s.backupStorageConfig(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	switch in.Type {
	case "local":
		in = backupStorageConfig{Type: "local"}
	case "s3":
		in.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
		in.Bucket, in.Region = strings.TrimSpace(in.Bucket), strings.TrimSpace(in.Region)
		in.Prefix, in.AccessKey = strings.Trim(strings.TrimSpace(in.Prefix), "/"), strings.TrimSpace(in.AccessKey)
		if in.SecretKey == secretMask {
			in.SecretKey = old.SecretKey
		}
		u, err := url.Parse(in.Endpoint)
		switch {
		case err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "":
			writeError(w, http.StatusBadRequest, errBackupEndpointInvalid)
			return
		case in.Bucket == "" || in.AccessKey == "" || in.SecretKey == "":
			writeError(w, http.StatusBadRequest, errBackupMissingKeys)
			return
		}
		// Antes de guardarla, se comprueba que se puede escribir en el bucket.
		if err := in.store("").Check(r.Context()); err != nil {
			writeError(w, http.StatusBadRequest, errBackupWriteFailed.With("detail", err.Error()))
			return
		}
	default:
		writeError(w, http.StatusBadRequest, errBackupStorageType)
		return
	}
	raw, _ := json.Marshal(in)
	if err := s.Store.SetSetting(r.Context(), settingBackupStorage, base64.StdEncoding.EncodeToString(s.Secrets.Seal(raw))); err != nil {
		s.internalError(w, err)
		return
	}
	s.handleGetBackupStorage(w, r)
}

// --- Copias de una app ---

// dbBackupsAvailable responde 503 si no hay Docker (las copias se hacen
// dentro de los contenedores).
func (s *Server) dbBackupsAvailable(w http.ResponseWriter) bool {
	if s.DBBackups == nil {
		writeError(w, http.StatusServiceUnavailable, errDBBackupsNoDocker)
		return false
	}
	return true
}

func (s *Server) loadDBApp(w http.ResponseWriter, r *http.Request) (*store.App, bool) {
	if !s.dbBackupsAvailable(w) {
		return nil, false
	}
	a, ok := s.loadApp(w, r)
	if !ok {
		return nil, false
	}
	if dbbackup.Engine(a.Image) == "" || a.Source == store.AppSourceCompose {
		writeError(w, http.StatusBadRequest, errDBBackupUnsupported)
		return nil, false
	}
	return a, true
}

func (s *Server) handleListDBBackups(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	engine := dbbackup.Engine(a.Image)
	if a.Source == store.AppSourceCompose {
		engine = ""
	}
	policy, err := s.Store.GetDBBackupPolicy(r.Context(), a.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	list, err := s.Store.ListDBBackups(r.Context(), a.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	storage, err := s.BackupStorage(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"engine": engine, "can_restore": dbbackup.CanRestore(engine), "policy": policy, "backups": list, "location": storage.Describe(),
	})
}

func (s *Server) handlePutDBBackupPolicy(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadDBApp(w, r)
	if !ok {
		return
	}
	var in store.DBBackupPolicy
	if !decode(w, r, &in) {
		return
	}
	if in.IntervalHours < 1 || in.IntervalHours > 24*30 || in.Keep < 1 || in.Keep > 365 {
		writeError(w, http.StatusBadRequest, errDBBackupPolicyInvalid)
		return
	}
	in.AppID = a.ID
	if err := s.Store.SaveDBBackupPolicy(r.Context(), &in); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleStartDBBackup(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadDBApp(w, r)
	if !ok {
		return
	}
	b, err := s.DBBackups.Start(r.Context(), a, "manual")
	if errors.Is(err, dbbackup.ErrBusy) {
		writeError(w, http.StatusConflict, errDBBackupBusy)
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, errUnexpected.With("detail", err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}

func (s *Server) loadDBBackup(w http.ResponseWriter, r *http.Request) (*store.App, *store.DBBackup, bool) {
	if !s.dbBackupsAvailable(w) {
		return nil, nil, false
	}
	a, ok := s.loadApp(w, r)
	if !ok {
		return nil, nil, false
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "backupID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, errDBBackupInvalidID)
		return nil, nil, false
	}
	b, err := s.Store.GetDBBackup(r.Context(), a.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errDBBackupNotFound)
		return nil, nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, nil, false
	}
	return a, b, true
}

func (s *Server) handleDownloadDBBackup(w http.ResponseWriter, r *http.Request) {
	_, b, ok := s.loadDBBackup(w, r)
	if !ok {
		return
	}
	rc, err := s.DBBackups.Open(r.Context(), b)
	if err != nil {
		writeError(w, http.StatusConflict, errUnexpected.With("detail", err.Error()))
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, path.Base(b.ObjectKey)))
	if b.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(b.Size, 10))
	}
	_, _ = io.Copy(w, rc)
}

func (s *Server) handleRestoreDBBackup(w http.ResponseWriter, r *http.Request) {
	a, b, ok := s.loadDBBackup(w, r)
	if !ok {
		return
	}
	if err := s.DBBackups.Restore(r.Context(), a, b); errors.Is(err, dbbackup.ErrBusy) {
		writeError(w, http.StatusConflict, errDBBackupBusy)
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, errDBBackupRestoreFailed.With("detail", err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDBBackup(w http.ResponseWriter, r *http.Request) {
	_, b, ok := s.loadDBBackup(w, r)
	if !ok {
		return
	}
	if b.Status == store.DBBackupRunning {
		writeError(w, http.StatusConflict, errDBBackupStillRunning)
		return
	}
	if err := s.DBBackups.Delete(r.Context(), b); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OnDBBackupFinished avisa de las copias fallidas.
func (s *Server) OnDBBackupFinished(ctx context.Context, app *store.App, b *store.DBBackup) {
	if s.Notify == nil || b.Status != store.DBBackupFailed {
		return
	}
	s.Notify.Notify(notify.Event{
		Kind: notify.EventBackupFailed, Severity: notify.Error,
		URL:     s.panelLink(ctx, fmt.Sprintf("/apps/%d", app.ID)),
		Title:   fmt.Sprintf("La copia de %s falló", app.Name),
		Message: b.Error,
	})
}
