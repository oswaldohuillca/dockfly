// Package secrets cifra los datos sensibles que se guardan en la base de
// datos (tokens de GitHub/GitLab, variables de entorno de los builds).
//
// Si alguien obtiene una copia de dockfly.db sin el archivo de clave, no
// puede leer los tokens.
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

var ErrDecrypt = errors.New("no se pudo descifrar: clave incorrecta o datos corruptos")

type Box struct {
	aead cipher.AEAD
}

// LoadOrCreate lee la clave de path o genera una nueva. Perder este archivo
// implica perder los tokens guardados (habría que volver a vincular las
// cuentas), pero no el resto de datos.
func LoadOrCreate(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, keySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, fmt.Errorf("guardando clave de cifrado: %w", err)
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("la clave de cifrado %s debe tener %d bytes", path, keySize)
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

// Seal cifra y autentica plaintext. El resultado es nonce || ciphertext.
func (b *Box) Seal(plaintext []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand no falla en sistemas soportados
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
