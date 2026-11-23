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

// Setup code: while there are no users, creating the administrator or
// restoring a backup requires a code only someone with access to the server
// knows (it is in the log and in /data/setup-token). That way nobody can get
// ahead of the owner on a freshly installed panel exposed to the internet.

const setupTokenFile = "setup-token"

// PrepareSetupToken generates (or reuses) the code if the installation has
// no users yet. It returns the code ("" when it is already set up).
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
	raw := strings.ToLower(base32.StdEncoding.EncodeToString(b)) // 24 characters
	token := raw[:6] + "-" + raw[6:12] + "-" + raw[12:18] + "-" + raw[18:]
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	s.SetupToken = token
	return token, nil
}

// checkSetupToken checks the code for the initial signup. With no code
// prepared (tests, or Dockfly started with users already) none is required.
func (s *Server) checkSetupToken(token string) bool {
	if s.SetupToken == "" {
		return true
	}
	norm := func(t string) string { return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(t), " ", "")) }
	return subtle.ConstantTimeCompare([]byte(norm(token)), []byte(norm(s.SetupToken))) == 1
}

// clearSetupToken deletes the code once there is an administrator.
func (s *Server) clearSetupToken() {
	s.SetupToken = ""
	_ = os.Remove(filepath.Join(s.DataDir, setupTokenFile))
}
