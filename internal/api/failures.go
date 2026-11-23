package api

import (
	"strings"
	"sync"
	"time"
)

// Per-account attempt limits, on top of the per-IP limit: this way a botnet or
// an attacker with plenty of IPv6 addresses cannot try passwords or codes
// unchecked against a single account.
const (
	loginMaxFailures = 15
	mfaMaxFailures   = 10
	failLockout      = 15 * time.Minute
	// failMaxTracked bounds memory use: the keys (emails) are client-chosen.
	failMaxTracked = 10000
)

// failures counts the failures per key (email or user) within a failLockout
// window.
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
		// Full: the oldest entry is forgotten. Stopping the count instead
		// would let anyone disable the lockout by filling the map with
		// made-up keys.
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

// loginKey normalizes the email that login failures are counted against.
func loginKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
