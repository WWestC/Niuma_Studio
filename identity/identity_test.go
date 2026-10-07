package identity

// identity_test.go — the account system's own pins: PBKDF2 against
// the published SHA-256 vectors, the credential discipline (salt
// uniqueness, verify), the session TTL ladder, and the service's
// refusal vocabulary (bad credentials uniform for unknown user and
// wrong password; the last-admin guard).

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestPBKDF2Vectors pins the hand-rolled PBKDF2-HMAC-SHA256 against
// the widely published vector set (the SHA-256 analog of RFC 6070's
// PBKDF2-HMAC-SHA1 vectors): a regression in the derivation is a
// silent invalidation of every stored hash, and this test is the trip
// wire.
func TestPBKDF2Vectors(t *testing.T) {
	cases := []struct {
		password, salt string
		iter, keyLen   int
		want           string
	}{
		{"password", "salt", 1, 32, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{"password", "salt", 2, 32, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
		{"password", "salt", 4096, 32, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
		{"passwordPASSWORDpassword", "saltSALTsaltSALTsaltSALTsaltSALTsalt", 4096, 40, "348c89dbcbd32b2f32d814b8116e84cf2b17347ebc1800181c4e2a1fb8dd53e1c635518c7dac47e9"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(pbkdf2Key([]byte(c.password), []byte(c.salt), c.iter, c.keyLen))
		if got != c.want {
			t.Errorf("pbkdf2(%q,%q,%d,%d) = %s, want %s", c.password, c.salt, c.iter, c.keyLen, got, c.want)
		}
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	salt, hash, err := NewCredential("correct horse battery staple")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(salt) != SaltLen || len(hash) != HashLen {
		t.Fatalf("credential shape: salt %d hash %d", len(salt), len(hash))
	}
	if !VerifyPassword(salt, hash, "correct horse battery staple") {
		t.Error("right password refused")
	}
	if VerifyPassword(salt, hash, "wrong") {
		t.Error("wrong password accepted")
	}
	if VerifyPassword(nil, nil, "anything") {
		t.Error("empty credential accepted")
	}
	// 两个同密码账户的盐必须不同（盐重用＝离线字典攻击打折）。
	salt2, hash2, _ := NewCredential("correct horse battery staple")
	if hex.EncodeToString(salt) == hex.EncodeToString(salt2) {
		t.Error("salt reuse across mints")
	}
	if hex.EncodeToString(hash) == hex.EncodeToString(hash2) {
		t.Error("hash identical across salts")
	}
}

func TestUsernameValidation(t *testing.T) {
	for _, ok := range []string{"owner", "3users", "牛马房主", "a.b-c_d", strings.Repeat("长", 32)} {
		if !ValidUsername(ok) {
			t.Errorf("ValidUsername(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "  ", "with space", "lead ", " trail", strings.Repeat("长", 33), "a\nb", string(rune(0x01))} {
		if ValidUsername(bad) {
			t.Errorf("ValidUsername(%q) = true, want false", bad)
		}
	}
	if n, ok := SanitizeUsername("  owner  "); !ok || n != "owner" {
		t.Errorf("SanitizeUsername trim: %q %v", n, ok)
	}
}

// memStore is the in-memory Store (tests; production has no file-
// backed account store by design).
type memStore struct {
	accounts map[string]Account
	projects map[string]map[string]bool
}

func newMemStore() *memStore {
	return &memStore{accounts: map[string]Account{}, projects: map[string]map[string]bool{}}
}

func (m *memStore) Create(a Account) error {
	if _, dup := m.accounts[a.Username]; dup {
		return ErrAccountExists(a.Username)
	}
	m.accounts[a.Username] = a
	return nil
}
func (m *memStore) Get(u string) (Account, bool) {
	a, ok := m.accounts[u]
	return a, ok
}
func (m *memStore) List() []Account {
	out := []Account{}
	for _, a := range m.accounts {
		out = append(out, Account{Username: a.Username, Role: a.Role, DisplayName: a.DisplayName, CreatedTS: a.CreatedTS})
	}
	return out
}
func (m *memStore) SetRole(u string, r Role) error {
	a, ok := m.accounts[u]
	if !ok {
		return ErrAccountNotFound(u)
	}
	a.Role = r
	m.accounts[u] = a
	return nil
}
func (m *memStore) SetPassword(u string, salt, hash []byte) error {
	a, ok := m.accounts[u]
	if !ok {
		return ErrAccountNotFound(u)
	}
	a.Salt, a.Hash = salt, hash
	m.accounts[u] = a
	return nil
}
func (m *memStore) Delete(u string) error {
	delete(m.accounts, u)
	delete(m.projects, u)
	return nil
}
func (m *memStore) Projects(u string) []string {
	var out []string
	for k := range m.projects[u] {
		out = append(out, k)
	}
	return out
}
func (m *memStore) GrantProject(u, p string) error {
	if m.projects[u] == nil {
		m.projects[u] = map[string]bool{}
	}
	m.projects[u][p] = true
	return nil
}
func (m *memStore) RevokeProject(u, p string) error {
	delete(m.projects[u], p)
	return nil
}

func mustAccount(t *testing.T, username string, role Role, password string) Account {
	t.Helper()
	salt, hash, err := NewCredential(password)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	return Account{Username: username, Role: role, Salt: salt, Hash: hash, CreatedTS: time.Now().Unix()}
}

func TestLoginRefusalsUniform(t *testing.T) {
	st := newMemStore()
	svc := NewService(st, time.Hour, ThrottleConfig{})
	if err := st.Create(mustAccount(t, "alice", RoleMember, "s3cret")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login("test", "alice", "wrong"); err == nil || err.Error() != ErrBadCredentials().Error() {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := svc.Login("test", "nobody", "whatever"); err == nil || err.Error() != ErrBadCredentials().Error() {
		t.Errorf("unknown user: %v", err)
	}
	// 拒绝文案逐字节相同：登录面不可枚举账户。
	_, e1 := svc.Login("test", "alice", "wrong")
	_, e2 := svc.Login("test", "nobody", "whatever")
	if e1.Error() != e2.Error() {
		t.Error("refusal copy differs between wrong-password and unknown-user")
	}
	sess, err := svc.Login("test", "  alice  ", "s3cret") // 用户名规范化
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.Username != "alice" || sess.Role != RoleMember {
		t.Errorf("session shape: %+v", sess)
	}
	if again, ok := svc.Sessions().Verify(sess.Token); !ok || again.Username != "alice" {
		t.Error("minted token does not verify")
	}
}

func TestSessionTTL(t *testing.T) {
	tab := NewSessions(30 * time.Millisecond)
	s, err := tab.Mint("alice", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tab.Verify(s.Token); !ok {
		t.Fatal("fresh session does not verify")
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok := tab.Verify(s.Token); ok {
		t.Error("expired session verifies")
	}
	// 过期即清扫：表里不再有它。
	tab.mu.Lock()
	n := len(tab.byTok)
	tab.mu.Unlock()
	if n != 0 {
		t.Errorf("expired entry not dropped: %d", n)
	}
}

func TestDisabledServiceDegrades(t *testing.T) {
	var svc *Service
	if svc.Enabled() {
		t.Error("nil service reports enabled")
	}
	if _, err := svc.Login("test", "a", "b"); err == nil || err.Error() != ErrNoAccountStore().Error() {
		t.Errorf("nil service login: %v", err)
	}
	s := NewService(nil, 0, ThrottleConfig{})
	if s.Enabled() {
		t.Error("storeless service reports enabled")
	}
}

func TestLastAdminGuard(t *testing.T) {
	st := newMemStore()
	svc := NewService(st, time.Hour, ThrottleConfig{})
	_ = st.Create(mustAccount(t, "root", RoleAdmin, "rootpw"))
	_ = st.Create(mustAccount(t, "bob", RoleMember, "bobpw"))
	if err := svc.DeleteAccount("root"); err == nil || err.Error() != ErrLastAdmin().Error() {
		t.Errorf("delete last admin: %v", err)
	}
	if err := svc.SetRole("root", RoleMember); err == nil {
		t.Error("demote last admin allowed")
	}
	// 第二名管理员在位后，动作放行。
	_ = st.Create(mustAccount(t, "root2", RoleAdmin, "pw"))
	if err := svc.SetRole("root", RoleMember); err != nil {
		t.Errorf("demote with second admin: %v", err)
	}
	// root 已是 member：删它不再受最后管理员守卫；而剩下的唯一管理员
	// root2 仍受守卫。
	if err := svc.DeleteAccount("root"); err != nil {
		t.Errorf("delete demoted member: %v", err)
	}
	if err := svc.DeleteAccount("root2"); err == nil || err.Error() != ErrLastAdmin().Error() {
		t.Errorf("delete the remaining sole admin: %v", err)
	}
}

func TestRoleLadder(t *testing.T) {
	if !(RoleAdmin.AtLeast(RoleMember) && RoleMember.AtLeast(RoleGuest) && RoleAdmin.AtLeast(RoleAdmin)) {
		t.Error("AtLeast ladder broken")
	}
	if RoleGuest.AtLeast(RoleMember) {
		t.Error("guest clears member floor")
	}
	cases := []struct {
		s    string
		want Role
	}{
		{"admin", RoleAdmin}, {"member", RoleMember}, {"guest", RoleGuest},
	}
	for _, c := range cases {
		if r, ok := ParseRole(c.s); !ok || r != c.want {
			t.Errorf("ParseRole(%q) = %v %v", c.s, r, ok)
		}
	}
	if _, ok := ParseRole("supervisor"); ok {
		t.Error("unknown role parses")
	}
	if RoleAdmin.String() != "admin" || RoleMember.String() != "member" || RoleGuest.String() != "guest" {
		t.Error("role spelling drifted")
	}
}

// TestLoginThrottle — the brake's four pins: cap misses lock the
// (source, username) key; the lock's refusals do not extend it; a
// success from a fresh source passes (per-source isolation); the
// window aging releases the lock.
func TestLoginThrottle(t *testing.T) {
	st := newMemStore()
	short := ThrottleConfig{Window: time.Minute, Cap: 3}
	svc := NewService(st, time.Hour, short)
	// 可测时钟：老化用推进而非等待（PBKDF2 的耗时会让真实短窗口
	// 在校验途中就过期——-race 下的实测偶红来源）。
	base := time.Now()
	svc.throttle.now = func() time.Time { return base }
	_ = st.Create(mustAccount(t, "alice", RoleMember, "s3cret"))

	// 3 misses（cap=3）→ 锁。
	for i := 0; i < 3; i++ {
		if _, err := svc.Login("192.0.2.9:1", "alice", "wrong"); err == nil {
			t.Fatal("错密码应拒绝")
		}
	}
	// 第 4 次：正确密码也被锁（先于校验拒绝）。
	if _, err := svc.Login("192.0.2.9:1", "alice", "s3cret"); err == nil || err.Error() != ErrLoginThrottled().Error() {
		t.Fatalf("满额后应节流拒绝: %v", err)
	}
	// 锁内尝试不续期：推进超过窗口后同钥匙放行。
	base = base.Add(time.Minute + time.Second)
	if _, err := svc.Login("192.0.2.9:1", "alice", "s3cret"); err != nil {
		t.Fatalf("窗口老化后应放行: %v", err)
	}
	// 按来源隔离：另一台机器的钥匙不受这边的失败史影响。
	for i := 0; i < 3; i++ {
		_, _ = svc.Login("198.51.100.7:2", "alice", "wrong")
	}
	if _, err := svc.Login("192.0.2.9:1", "alice", "s3cret"); err != nil {
		t.Fatalf("别处的失败不应锁住本来源: %v", err)
	}
	// 成功清零：错过两次后成功，再两次仍不锁（cap=3）。
	_, _ = svc.Login("203.0.113.5:3", "alice", "wrong")
	_, _ = svc.Login("203.0.113.5:3", "alice", "wrong")
	if _, err := svc.Login("203.0.113.5:3", "alice", "s3cret"); err != nil {
		t.Fatalf("成功应清零: %v", err)
	}
	_, _ = svc.Login("203.0.113.5:3", "alice", "wrong")
	if _, err := svc.Login("203.0.113.5:3", "alice", "s3cret"); err != nil {
		t.Fatalf("清零后 cap 内不应锁: %v", err)
	}
}
