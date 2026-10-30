package api

import (
	"strings"
	"sync"
	"time"
)

// Límites de intentos por cuenta, además del límite por IP: así una botnet o
// un atacante con muchas IPv6 no puede probar contraseñas o códigos sin freno
// contra una misma cuenta.
const (
	loginMaxFailures = 15
	mfaMaxFailures   = 10
	failLockout      = 15 * time.Minute
	// failMaxTracked acota la memoria: las claves (emails) las elige el cliente.
	failMaxTracked = 10000
)

// failures cuenta los fallos por clave (email o usuario) en una ventana de
// failLockout.
type failures[K comparable] struct {
	mu    sync.Mutex
	byKey map[K]failCount
}

type failCount struct {
	n     int
	since time.Time
}

func (f *failures[K]) blocked(key K, max int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byKey[key]
	if ok && time.Since(c.since) > failLockout {
		delete(f.byKey, key)
		return false
	}
	return ok && c.n >= max
}

func (f *failures[K]) fail(key K) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byKey == nil {
		f.byKey = map[K]failCount{}
	}
	c, ok := f.byKey[key]
	if !ok && len(f.byKey) >= failMaxTracked {
		// Lleno: se olvida el más antiguo. Dejar de contar permitiría
		// desactivar el bloqueo llenando el mapa con claves inventadas.
		var oldest K
		first := true
		for k, v := range f.byKey {
			if first || v.since.Before(f.byKey[oldest].since) {
				oldest, first = k, false
			}
		}
		delete(f.byKey, oldest)
	}
	if c.n == 0 {
		c.since = time.Now()
	}
	c.n++
	f.byKey[key] = c
}

func (f *failures[K]) reset(key K) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byKey, key)
}

// loginKey normaliza el email con el que se cuentan los fallos de login.
func loginKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
