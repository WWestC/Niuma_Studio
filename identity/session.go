package identity

// session.go — the login-session table: login mints a 128-bit
// crypto/rand token, the TTL ages it out on verify, revocation drops
// it. Sessions are in-memory on purpose: they are the server's own
// runtime state, a restart ends every session (the local operator
// auto-logs back in without touching a key; remote humans log in
// again — a session that survived the process would outlive the
// process's authority to have minted it).

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// DefaultSessionTTL is the login lifetime when Options carry no
// explicit one (NIUMA_SESSION_TTL overrides at boot).
const DefaultSessionTTL = 12 * time.Hour

// Session is one live login: the token (hex, 128-bit random), the
// account it carries, and the expiry.
type Session struct {
	Token    string
	Username string
	Role     Role
	Expires  time.Time
}

// Sessions is the token table (safe for concurrent use; one per
// process, held by the Service).
type Sessions struct {
	mu    sync.Mutex
	byTok map[string]Session
	ttl   time.Duration
}

// NewSessions builds an empty table with the given lifetime (>0;
// DefaultSessionTTL when 0).
func NewSessions(ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Sessions{byTok: make(map[string]Session), ttl: ttl}
}

// Mint issues a fresh session for the account. Token entropy is
// crypto/rand; a mint failure is an error, never a weak fallback.
func (t *Sessions) Mint(username string, role Role) (Session, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return Session{}, err
	}
	s := Session{
		Token:    hex.EncodeToString(buf),
		Username: username,
		Role:     role,
		Expires:  time.Now().Add(t.ttl),
	}
	t.mu.Lock()
	t.byTok[s.Token] = s
	t.mu.Unlock()
	return s, nil
}

// Verify answers the live session for a token (false when unknown or
// past expiry — expired entries are dropped on sight so the table
// self-cleans even without a sweeper).
func (t *Sessions) Verify(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.byTok[token]
	if !ok {
		return Session{}, false
	}
	if time.Now().After(s.Expires) {
		delete(t.byTok, token)
		return Session{}, false
	}
	return s, true
}

// Revoke drops one token (logout; a no-op on miss).
func (t *Sessions) Revoke(token string) {
	t.mu.Lock()
	delete(t.byTok, token)
	t.mu.Unlock()
}

// RevokeAll empties the table (tests, teardown).
func (t *Sessions) RevokeAll() {
	t.mu.Lock()
	t.byTok = make(map[string]Session)
	t.mu.Unlock()
}

// MintToken mints one 128-bit crypto/rand token (hex) — the shared
// shape behind session tokens, the boot service credential and the
// first-boot admin password. Failure is returned, never papered over.
func MintToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
