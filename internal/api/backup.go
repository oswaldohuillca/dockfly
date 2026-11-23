package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/backup"
	"github.com/dockfly/dockfly/internal/store"
)

// A backup includes the published sites: it can weigh a lot more than a
// normal upload.
const maxBackupBytes = 10 << 30

// Errors from restoring a backup.
var (
	errBackupMultipart  = newError("backup_multipart_required", "The backup must be sent as multipart/form-data")
	errBackupNoFile     = newError("backup_missing", "No backup was received")
	errBackupUpload     = newError("backup_upload_failed", "Couldn't read the upload: {detail}")
	errBackupInvalid    = newError("backup_invalid", "Invalid backup: {detail}")
	errBackupEncrypted  = newError("backup_encrypted", "This backup is encrypted: enter its password")
	errBackupExternalDB = newError("backup_external_db", "Backups can't be restored from the panel when Dockfly uses PostgreSQL or MySQL: restore the database with its own tools and the data folder by hand")
)

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	// Optional password (in a header, not in the URL, which would end up in the
	// logs): if it comes, the backup is encrypted. It holds keys, so encrypting
	// it is a sensible thing to do.
	pw := r.Header.Get("X-Backup-Password")
	name := "dockfly-backup-" + time.Now().UTC().Format("20060102-1504") + ".tar.gz"
	ctype := "application/gzip"
	if pw != "" {
		name += ".enc"
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

	out := io.Writer(w)
	var enc io.WriteCloser
	if pw != "" {
		var err error
		if enc, err = backup.EncryptWriter(w, pw); err != nil {
			s.internalError(w, err)
			return
		}
		out = enc
	}
	if err := backup.Create(r.Context(), s.Store, s.DataDir, s.Version, out); err != nil {
		// The headers went out already: all that's left is cutting the download
		// short and logging it (the browser will see a failed download).
		s.Log.Error("creating backup", "error", err)
		panic(http.ErrAbortHandler)
	}
	if enc != nil {
		if err := enc.Close(); err != nil { // flushes the last encrypted block
			s.Log.Error("closing encrypted backup", "error", err)
			panic(http.ErrAbortHandler)
		}
	}
}

// handleRestoreBackup stages a backup and restarts Dockfly to apply it. On a
// fresh install (no users) it doesn't require a session: that way you can
// migrate to another server straight from the welcome screen.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	// Restoring replaces the database and the keys: once there are users, only
	// an administrator with a panel session (not an API token) and typing their
	// password again; on a fresh install, with the setup code.
	var admin *store.User
	if n > 0 {
		u, ok := s.sessionUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errUnauthenticated)
			return
		}
		if u.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, errRoleForbidden)
			return
		}
		admin = u
	}
	if s.Store.External() {
		writeError(w, http.StatusConflict, errBackupExternalDB)
		return
	}
	if !isMultipartUpload(r) {
		writeError(w, http.StatusBadRequest, errBackupMultipart)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, errBackupMultipart)
		return
	}
	// The confirmation fields come before the file.
	var password, setupToken, encPassword string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, http.StatusBadRequest, errBackupNoFile)
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, errBackupUpload.With("detail", err.Error()))
			return
		}
		switch part.FormName() {
		case "password", "setup_token", "encryption_password":
			v, _ := io.ReadAll(io.LimitReader(part, 1024))
			name := part.FormName()
			part.Close()
			switch name {
			case "password":
				password = string(v)
			case "setup_token":
				setupToken = string(v)
			default:
				encPassword = string(v)
			}
			continue
		case "backup":
		default:
			part.Close()
			continue
		}
		if admin != nil && !auth.CheckPassword(admin.PasswordHash, password) {
			part.Close()
			writeError(w, http.StatusForbidden, errWrongPassword)
			return
		}
		if admin == nil && !s.checkSetupToken(setupToken) {
			part.Close()
			writeError(w, http.StatusForbidden, errSetupToken)
			return
		}
		// An encrypted backup (it starts with its magic) is decrypted on the fly
		// with the encryption password (a different one from the admin's).
		src, rest, err := backup.IsEncrypted(part)
		if err != nil {
			part.Close()
			writeError(w, http.StatusBadRequest, errBackupUpload.With("detail", err.Error()))
			return
		}
		reader := rest
		if src {
			if encPassword == "" {
				part.Close()
				writeError(w, http.StatusBadRequest, errBackupEncrypted)
				return
			}
			dr, err := backup.DecryptReader(rest, encPassword)
			if err != nil {
				part.Close()
				writeError(w, http.StatusBadRequest, errBackupInvalid.With("detail", err.Error()))
				return
			}
			reader = dr
		}
		m, err := backup.Stage(reader, s.DataDir)
		part.Close()
		if errors.Is(err, backup.ErrInvalid) {
			// The detail is whatever follows ErrInvalid's own text and ": ".
			detail := strings.TrimPrefix(err.Error(), backup.ErrInvalid.Error()+": ")
			writeError(w, http.StatusBadRequest, errBackupInvalid.With("detail", detail))
			return
		} else if err != nil {
			s.internalError(w, err)
			return
		}
		s.Log.Warn("backup staged: Dockfly is restarting to apply it",
			"created", m.CreatedAt, "version", m.Dockfly)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"created_at": m.CreatedAt,
			"message":    fmt.Sprintf("Backup from %s staged. Dockfly is restarting to apply it.", m.CreatedAt.Format("02/01/2006 15:04")),
		})
		// We let the response go out before restarting.
		go func() {
			time.Sleep(time.Second)
			s.Restart()
		}()
		return
	}
}
