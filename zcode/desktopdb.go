package zcode

// desktopdb.go — the desktop-owned sqlite stores (~/.zcode/v2/*, the
// CLI's db.sqlite) read and written IN-PROCESS through the same
// pure-Go engine the studio's own stores ride (modernc.org/sqlite).
// This retires the python3+sqlite3 seam: a bare Windows answers the
// python3 alias with the Store stub (exit 9009) and no user should be
// asked to install an interpreter for a sidebar nicety. Connection
// discipline mirrors the retired scripts exactly — one connection,
// busy_timeout 5000 (the desktop writes the same WAL dbs
// concurrently), foreign_keys per-connection where a delete must
// cascade — and each site keeps its script-era tolerance for absent
// dbs/tables (best-effort faces stay best-effort).

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openStoreDB opens a desktop-owned db the way sqlite3.connect did:
// created when absent, single connection, busy_timeout armed. The
// caller owns Close.
func openStoreDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // the scripts were one connection; pragmas must stick to it
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if _, err := db.Exec("pragma busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// withForeignKeys arms the per-connection cascade pragma (the session
// purge's delete rides it — foreign_keys is per-connection in sqlite,
// so it MUST land on the same single conn the delete runs on).
func withForeignKeys(db *sql.DB) error {
	_, err := db.Exec("pragma foreign_keys=on")
	return err
}

// inMarks builds a "?,?,…" placeholder list of n slots.
func inMarks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// chunkIDs slices ids into bind-chunk groups the retired scripts
// always used (400 — under every sqlite variable limit, parity with
// the seam's own shape).
func chunkIDs(ids []string, size int) [][]string {
	if size <= 0 {
		size = 400
	}
	var out [][]string
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

// RunningTaskByTitleHead answers the newest non-deleted running task
// row whose title prefix-matches the brief's first line (the recruit
// verifier's query, native). Truncation-tolerant: the index stores a
// possibly-truncated title, so whichever side is shorter defines the
// compared prefix. ok=false on a missing db or no match — the caller
// treats that as "not verified", never as an error.
func RunningTaskByTitleHead(taskDB, briefPath string) (taskID, status, createdAt string, ok bool) {
	head := ""
	if b, err := os.ReadFile(briefPath); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			head = strings.TrimSpace(line)
			break
		}
	}
	if head == "" {
		return "", "", "", false
	}
	if _, err := os.Stat(taskDB); err != nil {
		return "", "", "", false
	}
	db, err := openStoreDB(taskDB)
	if err != nil {
		return "", "", "", false
	}
	defer db.Close()
	rows, err := db.Query(
		"select task_id, task_status, created_at, title from tasks" +
			" where deleted=0 and task_status='running'" +
			" order by created_at desc limit 5")
	if err != nil {
		return "", "", "", false
	}
	defer rows.Close()
	for rows.Next() {
		var tid, st, title any
		var created int64
		if rows.Scan(&tid, &st, &created, &title) != nil {
			continue
		}
		t, _ := title.(string)
		rt, rh := []rune(t), []rune(head)
		n := len(rt)
		if len(rh) < n {
			n = len(rh)
		}
		if n == 0 || string(rt[:n]) != string(rh[:n]) {
			continue
		}
		s, _ := tid.(string)
		stat, _ := st.(string)
		return s, stat, fmt.Sprintf("%d", created), true
	}
	return "", "", "", false
}

// CliSessionDirectory answers the session's on-disk directory from the
// CLI's own store (the ackname backfill); "" when unknown.
func CliSessionDirectory(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	dbPath, err := HomePath(filepath.Join("cli", "db", "db.sqlite"))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(dbPath); err != nil {
		return ""
	}
	db, err := openStoreDB(dbPath)
	if err != nil {
		return ""
	}
	defer db.Close()
	var dir any
	if err := db.QueryRow(
		"SELECT directory FROM session WHERE id = ? LIMIT 1", sessionID).Scan(&dir); err != nil {
		return ""
	}
	s, _ := dir.(string)
	return s
}
