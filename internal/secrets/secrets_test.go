package secrets

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

func TestSealOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed := b.Seal([]byte("ghp_secreto"))
	if bytes.Contains(sealed, []byte("ghp_secreto")) {
		t.Fatal("el texto cifrado contiene el secreto en claro")
	}
	if bytes.Equal(b.Seal([]byte("x")), b.Seal([]byte("x"))) {
		t.Error("cada cifrado debe usar un nonce distinto")
	}

	// La misma clave, recargada del disco, descifra.
	b2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b2.Open(sealed)
	if err != nil || string(got) != "ghp_secreto" {
		t.Fatalf("Open: %q %v", got, err)
	}

	// Manipular un byte invalida el mensaje (GCM autentica).
	sealed[len(sealed)-1] ^= 1
	if _, err := b2.Open(sealed); !errors.Is(err, ErrDecrypt) {
		t.Errorf("un mensaje manipulado debe rechazarse: %v", err)
	}

	other, _ := LoadOrCreate(filepath.Join(t.TempDir(), "otra"))
	if _, err := other.Open(b.Seal([]byte("x"))); !errors.Is(err, ErrDecrypt) {
		t.Error("otra clave no debe poder descifrar")
	}
}
