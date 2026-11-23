// Package secrets encrypts the sensitive data stored in the database
// (GitHub/GitLab tokens, build environment variables).
//
// If someone gets hold of a copy of dockfly.db without the key file, they
// cannot read the tokens.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

const keySize = 32 // AES-256

var ErrDecrypt = errors.New("could not decrypt: wrong key or corrupted data")

type Box struct {
	aead cipher.AEAD
}

// LoadOrCreate reads the key from path or generates a new one. Losing this
// file means losing the stored tokens (the accounts would have to be linked
// again), but not the rest of the data.
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, keySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, fmt.Errorf("saving encryption key: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("the encryption key %s must be %d bytes long", path, keySize)
	}
	return New(key)
}

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts and authenticates plaintext. The result is nonce || ciphertext.
func (b *Box) Seal(plaintext []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil)
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrDecrypt
	}
	out, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
