package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Código de instalación: mientras no hay usuarios, crear el administrador o
// restaurar una copia exige un código que solo conoce quien tiene acceso al
// servidor (está en el log y en /data/setup-token). Así nadie se adelanta al
// dueño en un panel recién instalado y expuesto a internet.

const setupTokenFile = "setup-token"

// PrepareSetupToken genera (o reutiliza) el código si la instalación aún no
// tiene usuarios. Devuelve el código ("" si ya está configurada).
func (s *Server) PrepareSetupToken(ctx context.Context) (string, error) {
	n, err := s.Store.CountUsers(ctx)
	if err != nil || n > 0 {
		return "", err
	}
	path := filepath.Join(s.DataDir, setupTokenFile)
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 16 {
		s.SetupToken = strings.TrimSpace(string(b))
		return s.SetupToken, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := strings.ToLower(base32.StdEncoding.EncodeToString(b)) // 24 caracteres
	token := raw[:6] + "-" + raw[6:12] + "-" + raw[12:18] + "-" + raw[18:]
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	s.SetupToken = token
	return token, nil
}

// checkSetupToken comprueba el código del alta inicial. Sin código preparado
// (tests, o Dockfly arrancado con usuarios) no se exige.
func (s *Server) checkSetupToken(token string) bool {
	if s.SetupToken == "" {
		return true
	}
	norm := func(t string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(t), " ", "")) }
	return subtle.ConstantTimeCompare([]byte(norm(token)), []byte(norm(s.SetupToken))) == 1
}

// clearSetupToken borra el código cuando ya hay administrador.
func (s *Server) clearSetupToken() {
	s.SetupToken = ""
	_ = os.Remove(filepath.Join(s.DataDir, setupTokenFile))
}
