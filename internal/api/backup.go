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

// Una copia incluye los sitios publicados: puede pesar bastante más que una
// subida normal.
const maxBackupBytes = 10 << 30

// Errores de la restauración de copias.
var (
	errBackupMultipart = newError("backup_multipart_required", "The backup must be sent as multipart/form-data")
	errBackupNoFile    = newError("backup_missing", "No backup was received")
	errBackupUpload    = newError("backup_upload_failed", "Couldn't read the upload: {detail}")
	errBackupInvalid   = newError("backup_invalid", "Invalid backup: {detail}")
)

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := "dockfly-backup-" + time.Now().UTC().Format("20060102-1504") + ".tar.gz"
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if err := backup.Create(r.Context(), s.Store, s.DataDir, s.Version, w); err != nil {
		// Las cabeceras ya se enviaron: solo queda cortar la descarga y
		// registrarlo (el navegador verá la descarga fallida).
		s.Log.Error("generando copia de seguridad", "error", err)
		panic(http.ErrAbortHandler)
	}
}

// handleRestoreBackup prepara una copia y reinicia Dockfly para aplicarla.
// En una instalación nueva (sin usuarios) no exige sesión: así se puede
// migrar a otro servidor desde la pantalla de bienvenida.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	// Restaurar sustituye la base de datos y las claves: con usuarios, solo
	// un administrador con su sesión del panel (no un token de API) y
	// volviendo a escribir su contraseña; en una instalación nueva, con el
	// código de instalación.
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
	// Los campos de confirmación van antes que el archivo.
	var password, setupToken string
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
		case "password", "setup_token":
			v, _ := io.ReadAll(io.LimitReader(part, 1024))
			part.Close()
			if part.FormName() == "password" {
				password = string(v)
			} else {
				setupToken = string(v)
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
		m, err := backup.Stage(part, s.DataDir)
		part.Close()
		if errors.Is(err, backup.ErrInvalid) {
			// El detalle es lo que sigue a "copia de seguridad inválida: ".
			detail := strings.TrimPrefix(err.Error(), backup.ErrInvalid.Error()+": ")
			writeError(w, http.StatusBadRequest, errBackupInvalid.With("detail", detail))
			return
		} else if err != nil {
			s.internalError(w, err)
			return
		}
		s.Log.Warn("copia de seguridad preparada: Dockfly se reinicia para aplicarla",
			"creada", m.CreatedAt, "version", m.Dockfly)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"created_at": m.CreatedAt,
			"message":    fmt.Sprintf("Copia del %s preparada. Dockfly se está reiniciando para aplicarla.", m.CreatedAt.Format("02/01/2006 15:04")),
		})
		// Dejamos que la respuesta llegue antes de reiniciar.
		go func() {
			time.Sleep(time.Second)
			s.Restart()
		}()
		return
	}
}
