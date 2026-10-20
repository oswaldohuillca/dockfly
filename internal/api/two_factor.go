package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/store"
)

// Verificación en dos pasos: tras la contraseña, el login devuelve un ticket
// de corta duración que se canjea por la sesión con un código TOTP (o un
// código de recuperación).

const (
	mfaTicketPurpose = "mfa"
	mfaTicketTTL     = 5 * time.Minute
	recoveryCodes    = 10
	// Fallos permitidos por usuario antes de bloquear el segundo paso un
	// rato: el límite por IP no basta si el atacante rota direcciones.
	mfaMaxFailures = 10
	mfaLockout     = 15 * time.Minute
)

// mfaFailures cuenta los códigos incorrectos por usuario.
type mfaFailures struct {
	mu   sync.Mutex
	byID map[int64]mfaCounter
}

type mfaCounter struct {
	n     int
	since time.Time
}

func (f *mfaFailures) blocked(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if ok && time.Since(c.since) > mfaLockout {
		delete(f.byID, id)
		return false
	}
	return ok && c.n >= mfaMaxFailures
}

func (f *mfaFailures) fail(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byID == nil {
		f.byID = map[int64]mfaCounter{}
	}
	c := f.byID[id]
	if c.n == 0 {
		c.since = time.Now()
	}
	c.n++
	f.byID[id] = c
}

func (f *mfaFailures) reset(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byID, id)
}

// checkSecondFactor acepta un código TOTP o uno de recuperación.
func (s *Server) checkSecondFactor(ctx context.Context, u *store.User, code string) (ok bool, usedRecovery bool, err error) {
	if !u.TOTPEnabled || len(u.TOTPSecretEnc) == 0 {
		return false, false, nil
	}
	code = strings.TrimSpace(code)
	if strings.Contains(code, "-") || len(strings.ReplaceAll(code, " ", "")) > 6 {
		ok, err := s.Store.UseRecoveryCode(ctx, u.ID, auth.HashRecoveryCode(code))
		return ok, ok, err
	}
	secret, err := s.Secrets.Open(u.TOTPSecretEnc)
	if err != nil {
		return false, false, err
	}
	step, valid := auth.VerifyTOTP(string(secret), code, time.Now(), u.TOTPLastStep)
	if !valid {
		return false, false, nil
	}
	ok, err = s.Store.AdvanceTOTPStep(ctx, u.ID, step)
	return ok, false, err
}

func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := s.Signer.VerifyTicket(mfaTicketPurpose, in.Ticket)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "el inicio de sesión caducó; vuelve a escribir tu contraseña")
		return
	}
	if s.mfa.blocked(id) {
		writeError(w, http.StatusTooManyRequests, "demasiados códigos incorrectos; espera unos minutos")
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "el inicio de sesión caducó; vuelve a escribir tu contraseña")
		return
	}
	ok, recovery, err := s.checkSecondFactor(r.Context(), u, in.Code)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !ok {
		s.mfa.fail(id)
		writeError(w, http.StatusUnauthorized, "código incorrecto")
		return
	}
	s.mfa.reset(id)
	s.setSession(w, r, u)
	left := 0
	if recovery {
		if fresh, err := s.Store.UserByID(r.Context(), u.ID); err == nil {
			left = len(fresh.RecoveryCodes)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "used_recovery_code": recovery, "recovery_codes_left": left})
}

// requirePassword comprueba la contraseña del usuario de la sesión: las
// operaciones sobre el segundo factor la piden aunque haya sesión.
func (s *Server) requirePassword(w http.ResponseWriter, r *http.Request, password string) (*store.User, bool) {
	u := currentUser(r)
	if !auth.CheckPassword(u.PasswordHash, password) {
		writeError(w, http.StatusForbidden, "contraseña incorrecta")
		return nil, false
	}
	return u, true
}

func (s *Server) handleGet2FA(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": u.TOTPEnabled, "recovery_codes_left": len(u.RecoveryCodes)})
}

// handleSetup2FA genera un secreto nuevo (pendiente hasta confirmarlo con
// un código) y devuelve el QR para escanearlo.
func (s *Server) handleSetup2FA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.requirePassword(w, r, in.Password)
	if !ok {
		return
	}
	if u.TOTPEnabled {
		writeError(w, http.StatusConflict, "la verificación en dos pasos ya está activada")
		return
	}
	secret := auth.NewTOTPSecret()
	u.TOTPSecretEnc = s.Secrets.Seal([]byte(secret))
	if err := s.Store.SetUserTOTP(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	uri := auth.TOTPURI(secret, u.Email, "Dockfly")
	svg, err := auth.QRSVG(uri)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri, "qr_svg": svg})
}

// handleEnable2FA activa el segundo factor si el código del secreto
// pendiente es correcto, y devuelve los códigos de recuperación (solo esta vez).
func (s *Server) handleEnable2FA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := currentUser(r)
	if u.TOTPEnabled {
		writeError(w, http.StatusConflict, "la verificación en dos pasos ya está activada")
		return
	}
	if len(u.TOTPSecretEnc) == 0 {
		writeError(w, http.StatusBadRequest, "primero genera el código QR")
		return
	}
	secret, err := s.Secrets.Open(u.TOTPSecretEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	step, valid := auth.VerifyTOTP(string(secret), in.Code, time.Now(), 0)
	if !valid {
		writeError(w, http.StatusBadRequest, "código incorrecto: revisa que la hora del móvil sea correcta")
		return
	}
	codes := auth.NewRecoveryCodes(recoveryCodes)
	u.TOTPEnabled, u.TOTPLastStep, u.RecoveryCodes = true, step, hashCodes(codes)
	if err := s.Store.SetUserTOTP(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Server) handleDisable2FA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.requirePassword(w, r, in.Password)
	if !ok {
		return
	}
	u.TOTPSecretEnc, u.TOTPEnabled, u.TOTPLastStep, u.RecoveryCodes = nil, false, 0, nil
	if err := s.Store.SetUserTOTP(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.requirePassword(w, r, in.Password)
	if !ok {
		return
	}
	if !u.TOTPEnabled {
		writeError(w, http.StatusBadRequest, "la verificación en dos pasos no está activada")
		return
	}
	codes := auth.NewRecoveryCodes(recoveryCodes)
	u.RecoveryCodes = hashCodes(codes)
	if err := s.Store.SetUserTOTP(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func hashCodes(codes []string) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = auth.HashRecoveryCode(c)
	}
	return out
}

// Reset2FA desactiva la verificación en dos pasos de un usuario. Lo usa el
// subcomando "dockfly reset-2fa" cuando alguien pierde el móvil y los códigos.
func Reset2FA(ctx context.Context, st *store.Store, email string) error {
	u, err := st.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("no hay ningún usuario con ese email")
	} else if err != nil {
		return err
	}
	u.TOTPSecretEnc, u.TOTPEnabled, u.TOTPLastStep, u.RecoveryCodes = nil, false, 0, nil
	return st.SetUserTOTP(ctx, u)
}
