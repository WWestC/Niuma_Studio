package sqlstore

// identity.go — the accounts table's Store implementation (identity/
// Store's only production backend: multi-user mode requires sqlite by
// design — an account table in a file cabinet has no honest namespace
// story, so no LocalStore twin exists). Accounts are studio-global
// rows; List answers credentials zeroed (digests never leave the
// process).

import (
	"database/sql"
	"sort"

	"github.com/WWestC/Niuma_Studio/identity"
)

// AccountStore is the identity.Store view over the accounts tables.
type AccountStore struct {
	db *sql.DB
}

// Identity is the account-system view over this database.
func (d *DB) Identity() *AccountStore { return &AccountStore{db: d.db} }

// Compile-time contract pin.
var _ identity.Store = (*AccountStore)(nil)

func (s *AccountStore) Create(a identity.Account) error {
	if _, err := s.db.Exec(`INSERT INTO accounts (username, role, display_name, salt, hash, created_ts)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.Username, a.Role.String(), a.DisplayName, a.Salt, a.Hash, a.CreatedTS); err != nil {
		return identity.ErrAccountExists(a.Username)
	}
	return nil
}

func (s *AccountStore) Get(username string) (identity.Account, bool) {
	var a identity.Account
	var role string
	err := s.db.QueryRow(`SELECT username, role, display_name, salt, hash, created_ts
		FROM accounts WHERE username = ?`, username).
		Scan(&a.Username, &role, &a.DisplayName, &a.Salt, &a.Hash, &a.CreatedTS)
	if err != nil {
		return identity.Account{}, false
	}
	a.Role, _ = identity.ParseRole(role)
	return a, true
}

func (s *AccountStore) List() []identity.Account {
	rows, err := s.db.Query(`SELECT username, role, display_name, created_ts FROM accounts`)
	if err != nil {
		return []identity.Account{}
	}
	defer rows.Close()
	out := []identity.Account{}
	for rows.Next() {
		var a identity.Account
		var role string
		if err := rows.Scan(&a.Username, &role, &a.DisplayName, &a.CreatedTS); err != nil {
			continue
		}
		a.Role, _ = identity.ParseRole(role)
		out = append(out, a) // salt/hash stay nil: digests never leave
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (s *AccountStore) SetRole(username string, role identity.Role) error {
	res, err := s.db.Exec(`UPDATE accounts SET role = ? WHERE username = ?`, role.String(), username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return identity.ErrAccountNotFound(username)
	}
	return nil
}

func (s *AccountStore) SetPassword(username string, salt, hash []byte) error {
	res, err := s.db.Exec(`UPDATE accounts SET salt = ?, hash = ? WHERE username = ?`, salt, hash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return identity.ErrAccountNotFound(username)
	}
	return nil
}

func (s *AccountStore) Delete(username string) error {
	if _, err := s.db.Exec(`DELETE FROM account_projects WHERE username = ?`, username); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM accounts WHERE username = ?`, username)
	return err
}

func (s *AccountStore) Projects(username string) []string {
	rows, err := s.db.Query(`SELECT project_key FROM account_projects WHERE username = ? ORDER BY project_key`, username)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			continue
		}
		out = append(out, k)
	}
	return out
}

func (s *AccountStore) GrantProject(username, projectKey string) error {
	_, err := s.db.Exec(`INSERT INTO account_projects (username, project_key) VALUES (?, ?)
		ON CONFLICT (username, project_key) DO NOTHING`, username, projectKey)
	return err
}

func (s *AccountStore) RevokeProject(username, projectKey string) error {
	_, err := s.db.Exec(`DELETE FROM account_projects WHERE username = ? AND project_key = ?`, username, projectKey)
	return err
}
