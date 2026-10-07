// Package identity owns the studio's account system: the role ladder
// (admin / member / guest), the credential discipline (per-account
// random salt + PBKDF2-HMAC-SHA256, crypto/rand with no predictable
// fallback), and the session layer login mints. It is a base-row
// package in the layering grid — leaves plus nothing above — so the
// persistence seam is the Store interface below; sqlstore implements
// it (multi-user mode requires sqlite; there is deliberately NO file-
// backed implementation, an account table in a file cabinet would be
// the JSON shelf's namespace problem all over again).
//
// The trust model this package serves (see SECURITY.md): the
// historical studio ran on "the loopback IS the owner" — a name string
// was an identity and a connection was a credential. Accounts replace
// that for every face the identity service is wired into: management
// authority comes from a session (login → token → role), not from the
// name a frame claims. Single-user deployments keep the zero-friction
// shape precisely because no account store is wired there.
package identity

import (
	"errors"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// Role is the three-step authority ladder. The ordering is the
// permission matrix's arithmetic: a session may act on a face when its
// role is at least the face's floor (Role.AtLeast). Guest is the zero
// value — a missing role degrades to the least privilege, never to
// admin by accident.
type Role int

const (
	RoleGuest Role = iota
	RoleMember
	RoleAdmin
)

// String is the wire/storage spelling (lowercase ASCII; the accounts
// table stores these bytes verbatim).
func (r Role) String() string {
	switch r {
	case RoleAdmin:
		return "admin"
	case RoleMember:
		return "member"
	default:
		return "guest"
	}
}

// ParseRole reads the storage spelling back (ok=false on anything
// else — an unknown role string is a data problem, not a silent
// guest).
func ParseRole(s string) (Role, bool) {
	switch strings.TrimSpace(s) {
	case "admin":
		return RoleAdmin, true
	case "member":
		return RoleMember, true
	case "guest":
		return RoleGuest, true
	}
	return RoleGuest, false
}

// AtLeast is the matrix check: does r clear min's floor.
func (r Role) AtLeast(min Role) bool { return r >= min }

// Account is one studio-level human principal. AI members are not
// accounts — they are studio resources driven by the host process;
// their connection authority comes from the studio's own service
// credential, never from a password.
type Account struct {
	Username    string
	Role        Role
	DisplayName string
	// Salt+Hash are the credential (credential.go's discipline); an
	// Account returned by Store.List carries them zeroed — digests
	// never leave the process.
	Salt []byte
	Hash []byte
	// CreatedTS is unix seconds, stamped by Store.Create callers.
	CreatedTS int64
}

// ValidUsername judges the RAW string: non-empty, 1–32 runes, no
// control characters, no whitespace anywhere (leading/trailing space
// is invalid here — trimming is SanitizeUsername's job, the gate
// itself never silently repairs). Owner display names may be any
// prose, and the migration promotes the owner under their own name,
// so CJK is legal — what is refused is the shape that would smuggle
// spaces or control bytes into table keys.
func ValidUsername(name string) bool {
	if name == "" || len([]rune(name)) > 32 {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return false
		}
	}
	return true
}

// SanitizeUsername trims and refuses (ok=false) anything
// ValidUsername rejects — construction sites call this once and keep
// the trimmed form.
func SanitizeUsername(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, ValidUsername(name)
}

// --- the refusal vocabulary (parity discipline: every consumer pins
// these constructors' bytes, wording changes land HERE) ----------------

func ErrBadCredentials() error {
	return errors.New(i18n.S("用户名或密码不正确"))
}

func ErrLoginRequired() error {
	return errors.New(i18n.S("此操作需要先登录"))
}

func ErrSessionExpired() error {
	return errors.New(i18n.S("登录会话已过期，请重新登录"))
}

func ErrAccountExists(username string) error {
	return errors.New(i18n.Sf("账户 %s 已存在", username))
}

func ErrAccountNotFound(username string) error {
	return errors.New(i18n.Sf("账户 %s 不存在", username))
}

func ErrLastAdmin() error {
	return errors.New(i18n.S("工作室至少要保留一名管理员——先任命另一名管理员再动这一位"))
}

func ErrNoAccountStore() error {
	return errors.New(i18n.S("本工作室未启用多用户账户体系"))
}

// Store is the persistence seam. The multi-user studio wires
// sqlstore's implementation; single-user mode wires nothing (a nil
// service, the historical zero-friction shape). Methods are
// studio-global: accounts are one studio's roster, namespaces do not
// apply.
type Store interface {
	// Create inserts a new account; a duplicate username answers
	// ErrAccountExists.
	Create(a Account) error
	// Get fetches one account with its credential (Login needs the
	// bytes); ok=false when absent.
	Get(username string) (Account, bool)
	// List answers the roster sorted by username, credentials zeroed.
	List() []Account
	// SetRole moves one account on the ladder (ErrAccountNotFound).
	SetRole(username string, role Role) error
	// SetPassword replaces one account's credential bytes.
	SetPassword(username string, salt, hash []byte) error
	// Delete removes one account and its project grants.
	Delete(username string) error
	// Projects answers the project keys the account may see beyond the
	// lobby (admin ignores grants — the whole studio is theirs).
	Projects(username string) []string
	// GrantProject / RevokeProject maintain that set (idempotent).
	GrantProject(username, projectKey string) error
	RevokeProject(username, projectKey string) error
}

// Service is the runtime facade the server holds: login over the
// store, the session table, and the last-admin guard on management
// moves.
type Service struct {
	store    Store
	sessions *Sessions
	throttle *loginThrottle
}

// NewService wires the facade. ttl is the session lifetime (0 = the
// DefaultSessionTTL); throttle tunes the login brake (zero values take
// the defaults); a nil store answers a nil-correct service every
// method degrades on (ErrNoAccountStore) — embedders that compile
// against the type must not panic for forgetting the store.
func NewService(store Store, ttl time.Duration, throttle ThrottleConfig) *Service {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Service{store: store, sessions: NewSessions(ttl), throttle: newLoginThrottle(throttle)}
}

// Store exposes the management seam (the admin-only HTTP face); nil
// when unwired.
func (s *Service) Store() Store {
	if s == nil {
		return nil
	}
	return s.store
}

// Sessions exposes the session table (the WS/HTTP auth checks).
func (s *Service) Sessions() *Sessions {
	if s == nil {
		return nil
	}
	return s.sessions
}

// Enabled reports whether an account store is wired (the multi-user
// shape). Every zero-friction branch in the server keys off this: a
// disabled service means the historical single-user trust model.
func (s *Service) Enabled() bool { return s != nil && s.store != nil }

// Login verifies credentials and mints a session. source identifies
// the caller's origin for the login brake (the TCP peer; behind a
// reverse proxy every caller shares the proxy's address —
// X-Forwarded-For is deliberately not trusted). The refusal is
// uniform (ErrBadCredentials) for an unknown user and a wrong password
// alike — the account roster is not enumerable through login; a key
// locked by the brake answers ErrLoginThrottled before any
// verification (locked attempts do not extend the lock).
func (s *Service) Login(source, username, password string) (Session, error) {
	if !s.Enabled() {
		return Session{}, ErrNoAccountStore()
	}
	key := source + "\x00" + username
	if s.throttle.locked(key, s.throttle.now()) {
		return Session{}, ErrLoginThrottled()
	}
	username, ok := SanitizeUsername(username)
	if !ok {
		s.throttle.recordFailure(key, s.throttle.now())
		return Session{}, ErrBadCredentials()
	}
	acc, found := s.store.Get(username)
	if !found {
		// Burn a verification anyway so a wrong username and a wrong
		// password cost the same — the timing shape must not leak which
		// leg failed.
		VerifyPassword(dummySalt, dummyHash, password)
		s.throttle.recordFailure(key, s.throttle.now())
		return Session{}, ErrBadCredentials()
	}
	if !VerifyPassword(acc.Salt, acc.Hash, password) {
		s.throttle.recordFailure(key, s.throttle.now())
		return Session{}, ErrBadCredentials()
	}
	s.throttle.recordSuccess(key)
	return s.sessions.Mint(acc.Username, acc.Role)
}

// MintFor issues a session for an already-authenticated account (the
// loopback operator auto-login, the migration's admin bootstrap) —
// password verification is the caller's proof, not this method's.
func (s *Service) MintFor(username string, role Role) (Session, error) {
	if !s.Enabled() {
		return Session{}, ErrNoAccountStore()
	}
	return s.sessions.Mint(username, role)
}

// DeleteAccount drops one account unless it is the last admin
// (ErrLastAdmin) — a studio that locks out every admin is
// unrecoverable without surgery.
func (s *Service) DeleteAccount(username string) error {
	if !s.Enabled() {
		return ErrNoAccountStore()
	}
	if acc, ok := s.store.Get(username); ok && acc.Role == RoleAdmin && s.adminCount() <= 1 {
		return ErrLastAdmin()
	}
	return s.store.Delete(username)
}

// SetRole moves one account on the ladder under the same last-admin
// guard (demoting the final admin away).
func (s *Service) SetRole(username string, role Role) error {
	if !s.Enabled() {
		return ErrNoAccountStore()
	}
	if acc, ok := s.store.Get(username); ok && acc.Role == RoleAdmin && role != RoleAdmin && s.adminCount() <= 1 {
		return ErrLastAdmin()
	}
	return s.store.SetRole(username, role)
}

func (s *Service) adminCount() int {
	n := 0
	for _, a := range s.store.List() {
		if a.Role == RoleAdmin {
			n++
		}
	}
	return n
}
