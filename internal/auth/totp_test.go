package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// Vectores del apéndice B de la RFC 6238 (SHA-1, 8 dígitos → aquí los 6 últimos).
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	} {
		step, ok := VerifyTOTP(secret, want, time.Unix(unix, 0), 0)
		if !ok || step != unix/30 {
			t.Errorf("t=%d: el código %s debe aceptarse (step=%d ok=%v)", unix, want, step, ok)
		}
	}
}

func TestTOTPWindowAndReplay(t *testing.T) {
	secret := NewTOTPSecret()
	key, _ := b32.DecodeString(secret)
	now := time.Unix(1_800_000_000, 0)
	cur := now.Unix() / 30
	if _, ok := VerifyTOTP(secret, totpCode(key, cur-1), now, 0); !ok {
		t.Error("el código anterior debe aceptarse (reloj desfasado)")
	}
	if _, ok := VerifyTOTP(secret, totpCode(key, cur-2), now, 0); ok {
		t.Error("un código de hace un minuto no debe aceptarse")
	}
	step, ok := VerifyTOTP(secret, totpCode(key, cur), now, 0)
	if !ok {
		t.Fatal("el código actual debe aceptarse")
	}
	if _, ok := VerifyTOTP(secret, totpCode(key, cur), now, step); ok {
		t.Error("un código ya usado no debe aceptarse otra vez")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Error("un código de 5 dígitos no es válido")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := NewRecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Fatalf("código inválido o repetido: %q", c)
		}
		seen[c] = true
	}
	if HashRecoveryCode(codes[0]) != HashRecoveryCode(" "+strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))+" ") {
		t.Error("el código debe aceptarse sin guion y en mayúsculas")
	}
}

func TestQRSVG(t *testing.T) {
	svg, err := QRSVG(TOTPURI(NewTOTPSecret(), "ana@ejemplo.com", "Dockfly"))
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "v1h-") {
		t.Fatalf("QR: %v %.80s", err, svg)
	}
}

func TestTickets(t *testing.T) {
	s := &Signer{secret: []byte(strings.Repeat("k", 32))}
	tk := s.IssueTicket("mfa", 42, time.Minute)
	if id, err := s.VerifyTicket("mfa", tk); err != nil || id != 42 {
		t.Fatalf("ticket válido: %d %v", id, err)
	}
	if _, _, err := s.Verify(tk); err == nil {
		t.Error("un ticket no debe valer como sesión")
	}
	session, _ := s.Issue(42, 0)
	if _, err := s.VerifyTicket("mfa", session); err == nil {
		t.Error("una sesión no debe valer como ticket")
	}
	if _, err := s.VerifyTicket("otro", tk); err == nil {
		t.Error("un ticket solo vale para su propósito")
	}
	if _, err := s.VerifyTicket("mfa", s.IssueTicket("mfa", 42, -time.Second)); err == nil {
		t.Error("un ticket caducado no debe aceptarse")
	}
	if _, err := s.VerifyTicket("mfa", strings.Replace(tk, ".42.", ".43.", 1)); err == nil {
		t.Error("un ticket alterado no debe aceptarse")
	}
}
