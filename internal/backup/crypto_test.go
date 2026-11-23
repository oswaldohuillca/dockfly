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
	// Sizes around the chunk: 0, exactly one chunk, and several chunks + rest.
	for _, n := range []int{0, 1, encChunk - 1, encChunk, encChunk + 1, 3*encChunk + 123} {
		data := make([]byte, n)
		rand.Read(data)
		enc := encrypt(t, data, "secret-key")

		if ok, _, _ := IsEncrypted(bytes.NewReader(enc)); !ok {
			t.Errorf("n=%d: IsEncrypted = false", n)
		}
		r, err := DecryptReader(bytes.NewReader(enc), "secret-key")
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("n=%d: decrypted output differs (%d bytes)", n, len(got))
		}
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	enc := encrypt(t, []byte("hello world"), "good")
	r, err := DecryptReader(bytes.NewReader(enc), "bad")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a wrong password must fail on read: %v", err)
	}
}

func TestDecryptTruncatedFails(t *testing.T) {
	enc := encrypt(t, make([]byte, 3*encChunk), "key")
	r, err := DecryptReader(bytes.NewReader(enc[:len(enc)-10]), "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err == nil {
		t.Error("a truncated backup must not decrypt without an error")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	enc := encrypt(t, bytes.Repeat([]byte("x"), 100), "key")
	enc[len(enc)-1] ^= 0xff // flip the last byte of the ciphertext
	r, err := DecryptReader(bytes.NewReader(enc), "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a tampered backup must fail: %v", err)
	}
}

func TestIsEncryptedPlain(t *testing.T) {
	// A plain .tar.gz starts with 0x1f 0x8b: not encrypted, and re-readable.
	plain := []byte{0x1f, 0x8b, 0x08, 0x00, 1, 2, 3, 4, 5, 6}
	ok, r, err := IsEncrypted(bytes.NewReader(plain))
	if err != nil || ok {
		t.Fatalf("not encrypted: ok=%v err=%v", ok, err)
	}
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, plain) {
		t.Errorf("IsEncrypted consumed bytes: %v", got)
	}
}
