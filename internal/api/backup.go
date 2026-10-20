package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dockfly/dockfly/internal/backup"
)

// Una copia incluye los sitios publicados: puede pesar bastante más que una
// subida normal.
const maxBackupBytes = 10 << 30

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
	if n > 0 {
		if _, ok := s.sessionUser(r); !ok {
			writeError(w, http.StatusUnauthorized, "no autenticado")
			return
		}
	}
	if !isMultipartUpload(r) {
		writeError(w, http.StatusBadRequest, "se esperaba la copia en multipart/form-data")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "se esperaba multipart/form-data")
		return
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, http.StatusBadRequest, "no se recibió ninguna copia")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if part.FormName() != "backup" {
			part.Close()
			continue
		}
		m, err := backup.Stage(part, s.DataDir)
		part.Close()
		if errors.Is(err, backup.ErrInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
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
