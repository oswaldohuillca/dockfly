package api

import (
	"strings"
	"sync"
	"time"
)

// Límite de intentos de login por cuenta, además del límite por IP: así una
// botnet o un atacante con muchas IPv6 no puede probar contraseñas sin freno
// contra una misma cuenta.
const (
	loginMaxFailures = 15
	loginLockout     = 15 * time.Minute
	loginMaxTracked  = 10000
)

// loginFailures cuenta los fallos de login por email.
type loginFailures struct {
	mu   sync.Mutex
	byID map[string]mfaCounter
}

func loginKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (f *loginFailures) blocked(email string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[loginKey(email)]
	if ok && time.Since(c.since) > loginLockout {
		delete(f.byID, loginKey(email))
		return false
	}
	return ok && c.n >= loginMaxFailures
}

func (f *loginFailures) fail(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byID == nil {
		f.byID = map[string]mfaCounter{}
	}
	// Los emails los elige el cliente: se acota el mapa para que no crezca sin
	// fin, purgando primero los contadores caducados.
	if len(f.byID) >= loginMaxTracked {
		for k, c := range f.byID {
			if time.Since(c.since) > loginLockout {
				delete(f.byID, k)
			}
		}
		if len(f.byID) >= loginMaxTracked {
			return
		}
	}
	c := f.byID[loginKey(email)]
	if c.n == 0 {
		c.since = time.Now()
	}
	c.n++
	f.byID[loginKey(email)] = c
}

func (f *loginFailures) reset(email string) {
	f.mu.Lock()
	delete(f.byID, loginKey(email))
	f.mu.Unlock()
}
