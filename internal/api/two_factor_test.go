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
	login := `{"email":"admin@test.com","password":"secret123"}`

	// Enabling: it takes the password plus confirming a code from the secret.
	if code, _ := c.do("POST", "/api/auth/2fa/setup", `{"password":"wrong"}`); code != 403 {
		t.Errorf("setup with the wrong password: %d", code)
	}
	code, setup := c.do("POST", "/api/auth/2fa/setup", `{"password":"secret123"}`)
	if code != 200 || !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/Dockfly:admin@test.com?") ||
		!strings.HasPrefix(setup["qr_svg"].(string), "<svg") {
		t.Fatalf("setup: %d %v", code, setup["uri"])
	}
	secret := setup["secret"].(string)
	if _, st := c.do("GET", "/api/auth/2fa", ""); st["enabled"] != false {
		t.Error("it must not be enabled until the code is confirmed")
	}
	if code, _ := c.do("POST", "/api/auth/2fa/enable", `{"code":"000000"}`); code != 400 {
		t.Errorf("a wrong code must not enable it: %d", code)
	}
	now := time.Now()
	code, en := c.do("POST", "/api/auth/2fa/enable", fmt.Sprintf(`{"code":%q}`, auth.GenerateTOTP(secret, now)))
	if code != 200 || len(en["recovery_codes"].([]any)) != 10 {
		t.Fatalf("enabling: %d %v", code, en)
	}
	recovery := en["recovery_codes"].([]any)[0].(string)

	// Login: the password alone no longer grants a session.
	anon := newClient(t, ts)
	code, step1 := anon.do("POST", "/api/auth/login", login)
	if code != 200 || step1["two_factor"] != true || step1["ticket"] == "" {
		t.Fatalf("login with 2FA: %d %v", code, step1)
	}
	if code, _ := anon.do("GET", "/api/auth/me", ""); code != 401 {
		t.Fatal("without the second step there must be no session")
	}
	ticket := step1["ticket"].(string)
	if code, _ := anon.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":"123456"}`, ticket)); code != 401 {
		t.Errorf("wrong code: %d", code)
	}
	// The code used to enable it is spent: you have to wait for the next one.
	if code, _ := anon.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, auth.GenerateTOTP(secret, now))); code != 401 {
		t.Errorf("an already used code must not work a second time: %d", code)
	}
	next := auth.GenerateTOTP(secret, now.Add(30*time.Second))
	code, body := anon.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, next))
	if code != 200 {
		t.Fatalf("second step: %d %v", code, body)
	}
	if code, _ := anon.do("GET", "/api/auth/me", ""); code != 200 {
		t.Fatal("after the second step there must be a session")
	}

	// With a recovery code, which only works once.
	rc := newClient(t, ts)
	_, step1 = rc.do("POST", "/api/auth/login", login)
	ticket = step1["ticket"].(string)
	code, body = rc.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, strings.ToUpper(recovery)))
	if code != 200 || body["used_recovery_code"] != true || body["recovery_codes_left"] != float64(9) {
		t.Fatalf("recovery code: %d %v", code, body)
	}
	_, step1 = rc.do("POST", "/api/auth/login", login)
	if code, _ := rc.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, step1["ticket"], recovery)); code != 401 {
		t.Errorf("a recovery code must not work twice: %d", code)
	}

	// An expired or forged ticket is no good.
	s := serverOf(t, ts)
	old := s.Signer.IssueTicket(mfaTicketPurpose, 1, -time.Second)
	// The panel keys off the login_expired code to ask for the password again.
	if code, body := rc.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, old, next)); code != 401 || body["code"] != "login_expired" {
		t.Errorf("expired ticket: %d %v", code, body)
	}

	// Disabling asks for the password and a valid code.
	if code, _ := c.do("POST", "/api/auth/2fa/disable", `{"password":"wrong"}`); code != 403 {
		t.Errorf("disabling with the wrong password: %d", code)
	}
	if code, _ := c.do("POST", "/api/auth/2fa/disable", `{"password":"secret123","code":"000000"}`); code != 401 {
		t.Errorf("disabling with the wrong code: %d", code)
	}
	disableCode := en["recovery_codes"].([]any)[1].(string) // unused; doesn't depend on the clock
	if code, _ := c.do("POST", "/api/auth/2fa/disable", fmt.Sprintf(`{"password":"secret123","code":%q}`, disableCode)); code != 204 {
		t.Fatalf("disabling: %d", code)
	}
	if _, body := newClient(t, ts).do("POST", "/api/auth/login", login); body["two_factor"] == true {
		t.Error("once disabled, the password alone is enough")
	}
}

// After too many wrong codes the user's second step is locked, even when the
// attempts come from different IPs.
func TestTwoFactorLockout(t *testing.T) {
	ts, _ := testServer(t)
	s := serverOf(t, ts)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	_, setup := c.do("POST", "/api/auth/2fa/setup", `{"password":"secret123"}`)
	secret := setup["secret"].(string)
	c.do("POST", "/api/auth/2fa/enable", fmt.Sprintf(`{"code":%q}`, auth.GenerateTOTP(secret, time.Now())))

	for range mfaMaxFailures {
		s.mfa.fail(1)
	}
	ticket := s.Signer.IssueTicket(mfaTicketPurpose, 1, time.Minute)
	good := auth.GenerateTOTP(secret, time.Now().Add(30*time.Second))
	if code, _ := c.do("POST", "/api/auth/login/2fa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, ticket, good)); code != 429 {
		t.Errorf("with the user locked out not even the right code must work: %d", code)
	}
	if err := Reset2FA(context.Background(), s.Store, "ADMIN@test.com"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Store.UserByID(context.Background(), 1); u.TOTPEnabled || len(u.RecoveryCodes) != 0 {
		t.Error("reset-2fa must turn it off")
	}
}
