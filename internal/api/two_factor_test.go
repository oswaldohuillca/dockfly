package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/auth"
)

func TestTwoFactorFlow(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	login := `{"email":"admin@test.com","password":"secreto123"}`

	// Activar: hace falta la contraseña y confirmar un código del secreto.
	if code, _ := c.do("POST", "/api/auth/2fa/setup", `{"password":"mala"}`); code != 403 {
		t.Errorf("setup con contraseña incorrecta: %d", code)
	}
	code, setup := c.do("POST", "/api/auth/2fa/setup", `{"password":"secreto123"}`)
	if code != 200 || !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/Dockfly:admin@test.com?") ||
		!strings.HasPrefix(setup["qr_svg"].(string), "<svg") {
		t.Fatalf("setup: %d %v", code, setup["uri"])
	}
	secret := setup["secret"].(string)
	if _, st := c.do("GET", "/api/auth/2fa", ""); st["enabled"] != false {
		t.Error("hasta confirmar el código no debe estar activada")
	}
	if code, _ := c.do("POST", "/api/auth/2fa/enable", `{"code":"000000"}`); code != 400 {
		t.Errorf("un código incorrecto no debe activarla: %d", code)
	}
	now := time.Now()
	code, en := c.do("POST", "/api/auth/2fa/enable", fmt.Sprintf(`{"code":%q}`, auth.GenerateTOTP(secret, now)))
	if code != 200 || len(en["recovery_codes"].([]any)) != 10 {
		t.Fatalf("activar: %d %v", code, en)
	}
	recovery := en["recovery_codes"].([]any)[0].(string)

	// Login: la contraseña sola ya no da sesión.
	anon := newClient(t, ts)
	code, step1 := anon.do("POST", "/api/auth/login", login)
	if code != 200 || step1["two_factor"] != true || step1["ticket"] == "" {
		t.Fatalf("login con 2FA: %d %v", code, step1)
	}
	if code, _ := anon.do("GET", "/api/auth/me", ""); code != 401 {
		t.Fatal("sin el segundo paso no debe haber sesión")
	}
	ticket := step1["ticket"].(string)
	if code, _ := anon.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":"123456"}`, ticket)); code != 401 {
		t.Errorf("código incorrecto: %d", code)
	}
	// El código usado al activar ya no vale: hay que esperar al siguiente.
	if code, _ := anon.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, auth.GenerateTOTP(secret, now))); code != 401 {
		t.Errorf("un código ya usado no debe valer otra vez: %d", code)
	}
	next := auth.GenerateTOTP(secret, now.Add(30*time.Second))
	code, body := anon.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, next))
	if code != 200 {
		t.Fatalf("segundo paso: %d %v", code, body)
	}
	if code, _ := anon.do("GET", "/api/auth/me", ""); code != 200 {
		t.Fatal("tras el segundo paso debe haber sesión")
	}

	// Con un código de recuperación, que solo vale una vez.
	rc := newClient(t, ts)
	_, step1 = rc.do("POST", "/api/auth/login", login)
	ticket = step1["ticket"].(string)
	code, body = rc.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, strings.ToUpper(recovery)))
	if code != 200 || body["used_recovery_code"] != true || body["recovery_codes_left"] != float64(9) {
		t.Fatalf("código de recuperación: %d %v", code, body)
	}
	_, step1 = rc.do("POST", "/api/auth/login", login)
	if code, _ := rc.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, step1["ticket"], recovery)); code != 401 {
		t.Errorf("un código de recuperación no debe valer dos veces: %d", code)
	}

	// Un ticket caducado o falsificado no sirve.
	s := serverOf(t, ts)
	old := s.Signer.IssueTicket(mfaTicketPurpose, 1, -time.Second)
	if code, _ := rc.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, old, next)); code != 401 {
		t.Errorf("ticket caducado: %d", code)
	}

	// Desactivar pide la contraseña.
	if code, _ := c.do("POST", "/api/auth/2fa/disable", `{"password":"mala"}`); code != 403 {
		t.Errorf("desactivar con contraseña incorrecta: %d", code)
	}
	if code, _ := c.do("POST", "/api/auth/2fa/disable", `{"password":"secreto123"}`); code != 204 {
		t.Fatalf("desactivar: %d", code)
	}
	if _, body := newClient(t, ts).do("POST", "/api/auth/login", login); body["two_factor"] == true {
		t.Error("tras desactivarla, la contraseña basta")
	}
}

// Tras demasiados códigos incorrectos se bloquea el segundo paso del usuario,
// aunque los intentos lleguen desde IPs distintas.
func TestTwoFactorLockout(t *testing.T) {
	ts, _ := testServer(t)
	s := serverOf(t, ts)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	_, setup := c.do("POST", "/api/auth/2fa/setup", `{"password":"secreto123"}`)
	secret := setup["secret"].(string)
	c.do("POST", "/api/auth/2fa/enable", fmt.Sprintf(`{"code":%q}`, auth.GenerateTOTP(secret, time.Now())))

	for range mfaMaxFailures {
		s.mfa.fail(1)
	}
	ticket := s.Signer.IssueTicket(mfaTicketPurpose, 1, time.Minute)
	good := auth.GenerateTOTP(secret, time.Now().Add(30*time.Second))
	if code, _ := c.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, good)); code != 429 {
		t.Errorf("con el usuario bloqueado ni el código correcto debe valer: %d", code)
	}
	if err := Reset2FA(context.Background(), s.Store, "ADMIN@test.com"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Store.UserByID(context.Background(), 1); u.TOTPEnabled || len(u.RecoveryCodes) != 0 {
		t.Error("reset-2fa debe desactivarla")
	}
}
