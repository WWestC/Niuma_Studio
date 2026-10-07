package identity

// throttle.go — the login face's brute-force brake. The credential
// discipline (PBKDF2 210k) makes one guess slow; this makes MANY
// guesses stop: per (source, username) key, failures inside the
// window lock the key out once they reach the cap. A locked key's
// further attempts are refused WITHOUT extending the lock (the window
// ages from the last RECORDED failure — an attacker hammering away
// cannot keep the legitimate user locked forever); a successful login
// clears the key outright.
//
// The lock is per-process memory (the studio is a single process);
// behind a TLS reverse proxy every caller shares the proxy's peer
// address as `source` — X-Forwarded-For is NOT trusted (spoofable,
// and trusting it would let an attacker pick a fresh key per attempt).

import (
	"errors"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// ThrottleConfig tunes the brake. Zero values take NewService's
// defaults; tests shrink the window to make aging observable.
type ThrottleConfig struct {
	// Window: failures older than this no longer count (and a lock
	// lifts this long after the last recorded failure).
	Window time.Duration
	// Cap: failures inside the window that lock the key.
	Cap int
}

// defaultThrottle is the production posture: 5 misses inside 15
// minutes locks that (source, username) pair for the remainder of the
// window.
func defaultThrottle() ThrottleConfig {
	return ThrottleConfig{Window: 15 * time.Minute, Cap: 5}
}

// ErrLoginThrottled is the lock's refusal (says nothing about whether
// the account exists — the roster stays unenumerable).
func ErrLoginThrottled() error {
	return errors.New(i18n.S("尝试次数过多，请稍后再试"))
}

type throttleEntry struct {
	fails int
	last  time.Time
}

type loginThrottle struct {
	mu  sync.Mutex
	cfg ThrottleConfig
	by  map[string]throttleEntry
	// now is the clock seam (tests advance it deterministically — the
	// PBKDF2 burn makes wall-clock windows flaky under -race).
	now func() time.Time
}

func newLoginThrottle(cfg ThrottleConfig) *loginThrottle {
	if cfg.Window <= 0 || cfg.Cap <= 0 {
		cfg = defaultThrottle()
	}
	return &loginThrottle{cfg: cfg, by: map[string]throttleEntry{}, now: time.Now}
}

// locked reports whether the key is locked out at now, and why (the
// remaining lock time is the window aging from the last failure).
func (t *loginThrottle) locked(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.by[key]
	if !ok {
		return false
	}
	if now.Sub(e.last) >= t.cfg.Window {
		delete(t.by, key) // aged out: self-healing prune
		return false
	}
	return e.fails >= t.cfg.Cap
}

// recordFailure counts one miss (callers already verified the key is
// not locked — a locked attempt must not extend the lock).
func (t *loginThrottle) recordFailure(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Opportunistic prune: the map's growth is bounded by distinct
	// (source, username) pairs tried; stale pairs drop on touch.
	for k, e := range t.by {
		if now.Sub(e.last) >= t.cfg.Window {
			delete(t.by, k)
		}
	}
	e := t.by[key]
	if now.Sub(e.last) >= t.cfg.Window {
		e = throttleEntry{} // aged out: a fresh window starts
	}
	e.fails++
	e.last = now
	t.by[key] = e
}

// recordSuccess clears the key (a legitimate login resets the brake).
func (t *loginThrottle) recordSuccess(key string) {
	t.mu.Lock()
	delete(t.by, key)
	t.mu.Unlock()
}
