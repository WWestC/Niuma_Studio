package sqlstore

// identity_test.go — the accounts table over sqlite: CRUD round trips
// (reopen = a fresh handle over the same file, credentials survive),
// the duplicate/miss refusal vocabulary (byte-pinned to identity's
// constructors), project grants, and List's credential-zeroing.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/identity"
)

func openAccounts(t *testing.T) (*AccountStore, func() *AccountStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "studio.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.Identity(), func() *AccountStore {
		again, err := Open(path)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		t.Cleanup(func() { _ = again.Close() })
		return again.Identity()
	}
}

func acc(t *testing.T, username string, role identity.Role, password string) identity.Account {
	t.Helper()
	salt, hash, err := identity.NewCredential(password)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	return identity.Account{Username: username, Role: role, Salt: salt, Hash: hash, CreatedTS: time.Now().Unix()}
}

func TestAccountsRoundTrip(t *testing.T) {
	st, reopen := openAccounts(t)
	if err := st.Create(acc(t, "owner", identity.RoleAdmin, "pw1")); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(acc(t, "alice", identity.RoleMember, "pw2")); err != nil {
		t.Fatal(err)
	}
	// 重名拒绝：文案逐字节钉 identity 构造器。
	if err := st.Create(acc(t, "alice", identity.RoleGuest, "x")); err == nil || err.Error() != identity.ErrAccountExists("alice").Error() {
		t.Errorf("duplicate: %v", err)
	}
	// 重开（新句柄重读盘）：凭据与角色在。
	again := reopen()
	got, ok := again.Get("owner")
	if !ok || got.Username != "owner" || got.Role != identity.RoleAdmin {
		t.Fatalf("reopen get: %+v %v", got, ok)
	}
	if !identity.VerifyPassword(got.Salt, got.Hash, "pw1") {
		t.Error("credential does not survive reopen")
	}
	if _, ok := again.Get("nobody"); ok {
		t.Error("unknown account found")
	}
	// List：排序、凭据清零。
	list := again.List()
	if len(list) != 2 || list[0].Username != "alice" || list[1].Username != "owner" {
		t.Fatalf("list shape: %+v", list)
	}
	for _, a := range list {
		if a.Salt != nil || a.Hash != nil {
			t.Errorf("list leaks credential bytes for %s", a.Username)
		}
	}
}

func TestAccountMutations(t *testing.T) {
	st, reopen := openAccounts(t)
	_ = st.Create(acc(t, "bob", identity.RoleGuest, "pw"))
	if err := st.SetRole("bob", identity.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRole("ghost", identity.RoleMember); err == nil || err.Error() != identity.ErrAccountNotFound("ghost").Error() {
		t.Errorf("SetRole miss: %v", err)
	}
	salt, hash, _ := identity.NewCredential("newpw")
	if err := st.SetPassword("bob", salt, hash); err != nil {
		t.Fatal(err)
	}
	again := reopen()
	got, _ := again.Get("bob")
	if got.Role != identity.RoleMember || !identity.VerifyPassword(got.Salt, got.Hash, "newpw") {
		t.Errorf("mutations lost: role=%v verify=false", got.Role)
	}
	if err := again.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get("bob"); ok {
		t.Error("deleted account still found")
	}
}

func TestAccountProjects(t *testing.T) {
	st, reopen := openAccounts(t)
	_ = st.Create(acc(t, "carol", identity.RoleMember, "pw"))
	_ = st.GrantProject("carol", "book")
	_ = st.GrantProject("carol", "book") // 幂等
	_ = st.GrantProject("carol", "demo")
	if got := st.Projects("carol"); len(got) != 2 || got[0] != "book" || got[1] != "demo" {
		t.Fatalf("projects: %v", got)
	}
	again := reopen()
	if got := again.Projects("carol"); len(got) != 2 {
		t.Fatalf("projects lost on reopen: %v", got)
	}
	_ = again.RevokeProject("carol", "book")
	_ = again.RevokeProject("carol", "book") // 幂等
	if got := again.Projects("carol"); len(got) != 1 || got[0] != "demo" {
		t.Fatalf("after revoke: %v", got)
	}
	// 删账户连带清授权。
	_ = again.Delete("carol")
	if got := again.Projects("carol"); len(got) != 0 {
		t.Fatalf("grants outlive account: %v", got)
	}
}
