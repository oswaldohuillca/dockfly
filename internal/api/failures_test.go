package api

import (
	"strconv"
	"testing"
)

// El bloqueo es por cuenta y se levanta al acertar.
func TestFailures(t *testing.T) {
	var f failures[string]
	for range loginMaxFailures {
		if f.blocked(loginKey("Ana@test.com"), loginMaxFailures) {
			t.Fatal("bloqueado antes de tiempo")
		}
		f.fail(loginKey("ana@test.com "))
	}
	if !f.blocked(loginKey("ANA@test.com"), loginMaxFailures) {
		t.Fatal("tras demasiados fallos la cuenta debe bloquearse")
	}
	if f.blocked("otro@test.com", loginMaxFailures) {
		t.Fatal("el bloqueo no debe afectar a otras cuentas")
	}
	f.reset("ana@test.com")
	if f.blocked("ana@test.com", loginMaxFailures) {
		t.Fatal("reset debe levantar el bloqueo")
	}
}

// Llenar el mapa con emails inventados no desactiva el bloqueo de una cuenta.
func TestFailuresFullMapStillCounts(t *testing.T) {
	var f failures[string]
	for i := range failMaxTracked {
		f.fail(strconv.Itoa(i) + "@spam.test")
	}
	for range loginMaxFailures {
		f.fail("admin@test.com")
	}
	if !f.blocked("admin@test.com", loginMaxFailures) {
		t.Fatal("con el mapa lleno la cuenta real debe seguir bloqueándose")
	}
}
