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
	sealed := b.Seal([]byte("ghp_secret"))
	if bytes.Contains(sealed, []byte("ghp_secret")) {
		t.Fatal("the ciphertext contains the secret in the clear")
	}
	if bytes.Equal(b.Seal([]byte("x")), b.Seal([]byte("x"))) {
		t.Error("every encryption must use a different nonce")
	}

	// The same key, reloaded from disk, decrypts.
	b2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b2.Open(sealed)
	if err != nil || string(got) != "ghp_secret" {
		t.Fatalf("Open: %q %v", got, err)
	}

	// Tampering with a single byte invalidates the message (GCM authenticates).
	sealed[len(sealed)-1] ^= 1
	if _, err := b2.Open(sealed); !errors.Is(err, ErrDecrypt) {
		t.Errorf("a tampered message must be rejected: %v", err)
	}

	other, _ := LoadOrCreate(filepath.Join(t.TempDir(), "other"))
	if _, err := other.Open(b.Seal([]byte("x"))); !errors.Is(err, ErrDecrypt) {
		t.Error("a different key must not be able to decrypt")
	}
}
