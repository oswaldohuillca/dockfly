package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/store"
)

// Two-step verification: after the password, login returns a short-lived
// ticket that is exchanged for the session with a TOTP code (or a recovery
// code).

const (
	mfaTicketPurpose = "mfa"
	mfaTicketTTL     = 5 * time.Minute
	recoveryCodes    = 10
)

// Two-step verification errors.
var (
	// errLoginExpired: the second-step ticket expired; the panel asks for the
	// password again.
	errLoginExpired      = newError("login_expired", "Your sign-in has expired; enter your password again")
	errTooManyCodes      = newError("too_many_wrong_codes", "Too many wrong codes; wait a few minutes")
	errWrongCode         = newError("wrong_2fa_code", "Wrong code")
	errWrongPassword     = newError("wrong_password", "Wrong password")
	err2FAAlreadyEnabled = newError("two_factor_already_enabled", "Two-step verification is already enabled")
	err2FANoSetup        = newError("two_factor_setup_required", "Generate the QR code first")
	err2FAWrongSetupCode = newError("two_factor_wrong_setup_code", "Wrong code: check that your phone's clock is correct")
	err2FANotEnabled     = newError("two_factor_not_enabled", "Two-step verification isn't enabled")
)

// checkSecondFactor accepts either a TOTP code or a recovery code.
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
		writeError(w, http.StatusUnauthorized, errLoginExpired)
		return
	}
	if s.mfa.blocked(id, mfaMaxFailures) {
		writeError(w, http.StatusTooManyRequests, errTooManyCodes)
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusUnauthorized, errLoginExpired)
		return
	}
	ok, recovery, err := s.checkSecondFactor(r.Context(), u, in.Code)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !ok {
		s.mfa.fail(id)
		writeError(w, http.StatusUnauthorized, errWrongCode)
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

// requirePassword checks the password of the session's user: operations on
// the second factor ask for it even when there is a session.
func (s *Server) requirePassword(w http.ResponseWriter, r *http.Request, password string) (*store.User, bool) {
	u := currentUser(r)
	if !auth.CheckPassword(u.PasswordHash, password) {
		writeError(w, http.StatusForbidden, errWrongPassword)
		return nil, false
	}
	return u, true
}

func (s *Server) handleGet2FA(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": u.TOTPEnabled, "recovery_codes_left": len(u.RecoveryCodes)})
}

// handleSetup2FA generates a new secret (pending until it is confirmed with
// a code) and returns the QR code to scan.
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
		writeError(w, http.StatusConflict, err2FAAlreadyEnabled)
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

// handleEnable2FA turns the second factor on if the code for the pending
// secret is correct, and returns the recovery codes (this one time only).
func (s *Server) handleEnable2FA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := currentUser(r)
	if u.TOTPEnabled {
		writeError(w, http.StatusConflict, err2FAAlreadyEnabled)
		return
	}
	if len(u.TOTPSecretEnc) == 0 {
		writeError(w, http.StatusBadRequest, err2FANoSetup)
		return
	}
	secret, err := s.Secrets.Open(u.TOTPSecretEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	step, valid := auth.VerifyTOTP(string(secret), in.Code, time.Now(), 0)
	if !valid {
		writeError(w, http.StatusBadRequest, err2FAWrongSetupCode)
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
		Code     string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.requirePassword(w, r, in.Password)
	if !ok {
		return
	}
	// Disabling 2FA also demands a valid code (TOTP or recovery), so that a
	// stolen password is not enough to take the second factor off.
	if s.mfa.blocked(u.ID, mfaMaxFailures) {
		writeError(w, http.StatusTooManyRequests, errTooManyCodes)
		return
	}
	valid, _, err := s.checkSecondFactor(r.Context(), u, in.Code)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !valid {
		s.mfa.fail(u.ID)
		writeError(w, http.StatusUnauthorized, errWrongCode)
		return
	}
	s.mfa.reset(u.ID)
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
		writeError(w, http.StatusBadRequest, err2FANotEnabled)
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

// Reset2FA turns off a user's two-step verification. The "dockfly reset-2fa"
// subcommand uses it when someone loses both their phone and their codes.
func Reset2FA(ctx context.Context, st *store.Store, email string) error {
	u, err := st.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("there is no user with that email")
	} else if err != nil {
		return err
	}
	u.TOTPSecretEnc, u.TOTPEnabled, u.TOTPLastStep, u.RecoveryCodes = nil, false, 0, nil
	return st.SetUserTOTP(ctx, u)
}
