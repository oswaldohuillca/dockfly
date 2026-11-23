// Package auth handles passwords and HMAC-signed sessions.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const SessionTTL = 7 * 24 * time.Hour

var ErrInvalidSession = errors.New("invalid session")

// DummyHash returns a valid bcrypt hash (with the default cost) that does not
// match any real password. It is used to keep the login response time the same
// when the user does not exist. It is computed only once, on first use, so it
// does not slow down startup.
var DummyHash = sync.OnceValue(func() string {
	h, err := bcrypt.GenerateFromPassword([]byte("dockfly-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
})

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Signer issues and validates session tokens of the form
// "userID.version.expiry.signature". The version makes it possible to
// invalidate every session of a user (when the password changes).
type Signer struct {
	secret []byte
}

// LoadOrCreateSigner reads the secret key from path, or creates a new one if
// it does not exist, so that sessions survive container restarts.
func LoadOrCreateSigner(path string) (*Signer, error) {
	secret, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, secret, 0o600); err != nil {
			return nil, fmt.Errorf("saving secret key: %w", err)
		}
		return &Signer{secret: secret}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("secret key in %s is too short", path)
	}
	return &Signer{secret: secret}, nil
}

func (s *Signer) Issue(userID, version int64) (token string, expires time.Time) {
	expires = time.Now().Add(SessionTTL)
	payload := strconv.FormatInt(userID, 10) + "." + strconv.FormatInt(version, 10) + "." + strconv.FormatInt(expires.Unix(), 10)
	return payload + "." + s.sign(payload), expires
}

func (s *Signer) Verify(token string) (userID, version int64, err error) {
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return 0, 0, ErrInvalidSession
	}
	payload, sig := token[:i], token[i+1:]
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return 0, 0, ErrInvalidSession
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return 0, 0, ErrInvalidSession
	}
	nums := make([]int64, 3)
	for j, p := range parts {
		if nums[j], err = strconv.ParseInt(p, 10, 64); err != nil {
			return 0, 0, ErrInvalidSession
		}
	}
	if time.Now().Unix() > nums[2] {
		return 0, 0, ErrInvalidSession
	}
	return nums[0], nums[1], nil
}

// IssueTicket signs a single-purpose token (for example "mfa": the password
// has already been checked and only the second step is left). Its form
// "purpose.id.expiry.signature" can never be mistaken for a session.
func (s *Signer) IssueTicket(purpose string, userID int64, ttl time.Duration) string {
	payload := purpose + "." + strconv.FormatInt(userID, 10) + "." + strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return payload + "." + s.sign(payload)
}

func (s *Signer) VerifyTicket(purpose, token string) (int64, error) {
	i := strings.LastIndexByte(token, '.')
	if i < 0 || !hmac.Equal([]byte(token[i+1:]), []byte(s.sign(token[:i]))) {
		return 0, ErrInvalidSession
	}
	rest, ok := strings.CutPrefix(token[:i], purpose+".")
	if !ok {
		return 0, ErrInvalidSession
	}
	idStr, expStr, ok := strings.Cut(rest, ".")
	if !ok {
		return 0, ErrInvalidSession
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, ErrInvalidSession
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, ErrInvalidSession
	}
	return id, nil
}

func (s *Signer) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
