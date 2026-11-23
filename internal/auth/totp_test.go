package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// Vectors from appendix B of RFC 6238 (SHA-1, 8 digits → here the last 6).
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
			t.Errorf("t=%d: code %s must be accepted (step=%d ok=%v)", unix, want, step, ok)
		}
	}
}

func TestTOTPWindowAndReplay(t *testing.T) {
	secret := NewTOTPSecret()
	key, _ := b32.DecodeString(secret)
	now := time.Unix(1_800_000_000, 0)
	cur := now.Unix() / 30
	if _, ok := VerifyTOTP(secret, totpCode(key, cur-1), now, 0); !ok {
		t.Error("the previous code must be accepted (clock out of sync)")
	}
	if _, ok := VerifyTOTP(secret, totpCode(key, cur-2), now, 0); ok {
		t.Error("a code from a minute ago must not be accepted")
	}
	step, ok := VerifyTOTP(secret, totpCode(key, cur), now, 0)
	if !ok {
		t.Fatal("the current code must be accepted")
	}
	if _, ok := VerifyTOTP(secret, totpCode(key, cur), now, step); ok {
		t.Error("an already used code must not be accepted again")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Error("a 5-digit code is not valid")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := NewRecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Fatalf("invalid or repeated code: %q", c)
		}
		seen[c] = true
	}
	if HashRecoveryCode(codes[0]) != HashRecoveryCode(" "+strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))+" ") {
		t.Error("the code must be accepted without the dash and in upper case")
	}
}

func TestQRSVG(t *testing.T) {
	svg, err := QRSVG(TOTPURI(NewTOTPSecret(), "ana@example.com", "Dockfly"))
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "v1h-") {
		t.Fatalf("QR: %v %.80s", err, svg)
	}
}

func TestTickets(t *testing.T) {
	s := &Signer{secret: []byte(strings.Repeat("k", 32))}
	tk := s.IssueTicket("mfa", 42, time.Minute)
	if id, err := s.VerifyTicket("mfa", tk); err != nil || id != 42 {
		t.Fatalf("valid ticket: %d %v", id, err)
	}
	if _, _, err := s.Verify(tk); err == nil {
		t.Error("a ticket must not work as a session")
	}
	session, _ := s.Issue(42, 0)
	if _, err := s.VerifyTicket("mfa", session); err == nil {
		t.Error("a session must not work as a ticket")
	}
	if _, err := s.VerifyTicket("other", tk); err == nil {
		t.Error("a ticket is only valid for its own purpose")
	}
	if _, err := s.VerifyTicket("mfa", s.IssueTicket("mfa", 42, -time.Second)); err == nil {
		t.Error("an expired ticket must not be accepted")
	}
	if _, err := s.VerifyTicket("mfa", strings.Replace(tk, ".42.", ".43.", 1)); err == nil {
		t.Error("a tampered ticket must not be accepted")
	}
}
