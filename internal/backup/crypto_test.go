package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func encrypt(t *testing.T, data []byte, pw string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := EncryptWriter(&buf, pw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEncryptRoundTrip(t *testing.T) {
	// Tamaños alrededor del bloque: 0, justo el bloque, y varios bloques + resto.
	for _, n := range []int{0, 1, encChunk - 1, encChunk, encChunk + 1, 3*encChunk + 123} {
		data := make([]byte, n)
		rand.Read(data)
		enc := encrypt(t, data, "clave-secreta")

		if ok, _, _ := IsEncrypted(bytes.NewReader(enc)); !ok {
			t.Errorf("n=%d: IsEncrypted = false", n)
		}
		r, err := DecryptReader(bytes.NewReader(enc), "clave-secreta")
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("n=%d: descifrado distinto (%d bytes)", n, len(got))
		}
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	enc := encrypt(t, []byte("hola mundo"), "buena")
	r, err := DecryptReader(bytes.NewReader(enc), "mala")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("contraseña incorrecta debe fallar al leer: %v", err)
	}
}

func TestDecryptTruncatedFails(t *testing.T) {
	enc := encrypt(t, make([]byte, 3*encChunk), "clave")
	r, err := DecryptReader(bytes.NewReader(enc[:len(enc)-10]), "clave")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err == nil {
		t.Error("una copia truncada no debe descifrar sin error")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	enc := encrypt(t, bytes.Repeat([]byte("x"), 100), "clave")
	enc[len(enc)-1] ^= 0xff // altera el último byte del ciphertext
	r, err := DecryptReader(bytes.NewReader(enc), "clave")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("una copia manipulada debe fallar: %v", err)
	}
}

func TestIsEncryptedPlain(t *testing.T) {
	// Un .tar.gz normal empieza por 0x1f 0x8b: no es cifrado, y se puede releer.
	plain := []byte{0x1f, 0x8b, 0x08, 0x00, 1, 2, 3, 4, 5, 6}
	ok, r, err := IsEncrypted(bytes.NewReader(plain))
	if err != nil || ok {
		t.Fatalf("no cifrado: ok=%v err=%v", ok, err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, plain) {
		t.Errorf("IsEncrypted consumió bytes: %v", got)
	}
}
