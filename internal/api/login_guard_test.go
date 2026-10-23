package api

import "testing"

// El bloqueo es por cuenta (sin distinguir mayúsculas) y se levanta al acertar.
func TestLoginFailures(t *testing.T) {
	var f loginFailures
	for range loginMaxFailures {
		if f.blocked("Ana@test.com") {
			t.Fatal("bloqueado antes de tiempo")
		}
		f.fail("ana@test.com ")
	}
	if !f.blocked("ANA@test.com") {
		t.Fatal("tras demasiados fallos la cuenta debe bloquearse")
	}
	if f.blocked("otro@test.com") {
		t.Fatal("el bloqueo no debe afectar a otras cuentas")
	}
	f.reset("ana@test.com")
	if f.blocked("ana@test.com") {
		t.Fatal("reset debe levantar el bloqueo")
	}
}
