package zcode

// purge.go — the factory reset's ZCode half: the fold the exit path
// performs (ArchiveTaskIndexes) only ARCHIVES rows — the desktop's
// list keeps showing them under history, and the born sessions keep
// living in the CLI's own stores. A reset means GONE, so this file
// deletes, in three moves:
//
//   1. tasks-index.sqlite — drop every studio-born row. Scope is the
//      union of three conservative arms (a user's OWN rows —「重新编译」
//      「commit」 — must survive): the dhEmployee birth marker, the
//      session ids the room still knows (staffing bindings + the
//      assistant pin + whatever the caller harvested), and the room's
//      title contract 【项目】名-岗-Lv.N / 【…】小助手-… (taskIndexTitle
//      always stamps the rank slot; the assistant title is fixed).
//      The select happens in the same pass as the delete, and the
//      purged task_ids ARE session ids — the deep clean's hit list.
//   2. cli/db/db.sqlite — delete those sessions. Every dependent
//      table (message/part/session_input/… ) cascades on session
//      delete, so one delete with pragma foreign_keys=on takes the
//      whole transcript. The bridge child is already closed by the
//      time this runs (teardown precedes the purge in the reset
//      choreography); a running desktop's own connection yields to
//      the same WAL + busy_timeout seam the register rides.
//   3. per-session files — rollout/model-io-<id>.jsonl, exec/<id>/,
//      image-cache/<id>/ under ~/.zcode/cli.
//
// All best-effort with reported residue, same contract as the index
// writes: a failure means "still visible", never a failed reset.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PurgeStudioTasks deletes every studio-born row from the desktop
// task index and returns the session ids the rows carried (the deep
// clean's hit list — task_id IS the room-driven session id). harvested
// pre-seeds the scope with ids the caller collected before wiping its
// own stores (staffing bindings, the assistant pin); the title/mark
// arms catch rewritten and departed-member rows the room forgot.
func PurgeStudioTasks(harvested []string) ([]string, error) {
	dbPath, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return nil, fmt.Errorf("zcode: task index path: %w", err)
	}
	return purgeTasksAt(dbPath, harvested)
}

// purgeTasksAt is PurgeStudioTasks over an explicit db (tests).
func purgeTasksAt(dbPath string, harvested []string) ([]string, error) {
	db, err := openStoreDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("zcode: task index purge: %w", err)
	}
	defer db.Close()
	known := make([]string, 0, len(harvested))
	for _, id := range harvested {
		if id != "" {
			known = append(known, id)
		}
	}
	scope := "json_extract(meta_json, '$.dhEmployee') = 1" +
		" or title glob '【*】*-Lv.[0-9]*'" +
		" or title glob '【*】小助手-*'"
	args := []any{}
	if len(known) > 0 {
		scope += " or task_id in (" + inMarks(len(known)) + ")"
		args = append(args, toAny(known)...)
	}
	rows, err := db.Query("select task_id from tasks where "+scope, args...)
	if err != nil {
		return nil, fmt.Errorf("zcode: task index purge: %w", err)
	}
	seen := map[string]bool{}
	var ids []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("zcode: task index purge: %w", err)
		}
		if sid != "" && !seen[sid] {
			seen[sid] = true
			ids = append(ids, sid)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	for _, part := range chunkIDs(ids, 400) {
		if _, err := db.Exec("delete from tasks where task_id in ("+inMarks(len(part))+")",
			toAny(part)...); err != nil {
			return nil, fmt.Errorf("zcode: task index purge: %w", err)
		}
	}
	return ids, nil
}

// toAny widens a string slice for variadic sql args.
func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// PurgeTaskRows deletes EXACTLY the named session ids' rows from the
// desktop task index — the per-project dismiss's narrow arm: the
// factory reset's marker/title scope arms would take EVERY project's
// members, so a project-scoped clean must hit by id only (ids the
// caller harvested from that project's staffing rows). No ids come
// back — the caller already knows them. Best-effort: an error means
// "still visible", never a failed dismiss.
func PurgeTaskRows(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	dbPath, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return fmt.Errorf("zcode: task index path: %w", err)
	}
	return purgeTaskRowsAt(dbPath, ids)
}

// purgeTaskRowsAt is PurgeTaskRows over an explicit db (tests).
func purgeTaskRowsAt(dbPath string, ids []string) error {
	db, err := openStoreDB(dbPath)
	if err != nil {
		return fmt.Errorf("zcode: task row purge: %w", err)
	}
	defer db.Close()
	for _, part := range chunkIDs(ids, 400) {
		if _, err := db.Exec("delete from tasks where task_id in ("+inMarks(len(part))+")",
			toAny(part)...); err != nil {
			return fmt.Errorf("zcode: task row purge: %w", err)
		}
	}
	return nil
}

// PurgeStudioSessions deletes the born sessions' transcripts from the
// CLI's own database (~/.zcode/cli/db/db.sqlite): one delete per
// session id with foreign_keys=on — message/part/session_input/
// session_target/turn_usage/todo/model_usage all cascade, so the
// session's whole record goes in one sweep. Best-effort per id.
func PurgeStudioSessions(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	dbPath, err := HomePath(filepath.Join("cli", "db", "db.sqlite"))
	if err != nil {
		return fmt.Errorf("zcode: cli db path: %w", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil // no CLI store on this machine: nothing to purge
	}
	return purgeSessionsAt(dbPath, ids)
}

// purgeSessionsAt is PurgeStudioSessions over an explicit db (tests).
func purgeSessionsAt(dbPath string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	db, err := openStoreDB(dbPath)
	if err != nil {
		return fmt.Errorf("zcode: session purge: %w", err)
	}
	defer db.Close()
	// foreign_keys 是 per-connection：级联删除必须与 pragma 同连接
	if err := withForeignKeys(db); err != nil {
		return fmt.Errorf("zcode: session purge: %w", err)
	}
	for _, part := range chunkIDs(ids, 400) {
		if _, err := db.Exec("delete from session where id in ("+inMarks(len(part))+")",
			toAny(part)...); err != nil {
			return fmt.Errorf("zcode: session purge: %w", err)
		}
	}
	return nil
}

// PurgeSessionFiles removes the per-session residue the CLI keeps
// under ~/.zcode/cli: the model-io rollout transcript, the exec
// sandbox dir and the image-cache dir — each keyed by the session id,
// each absent on most machines. Best-effort per entry.
func PurgeSessionFiles(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	base, err := HomePath("cli")
	if err != nil {
		return fmt.Errorf("zcode: cli home: %w", err)
	}
	return purgeSessionFilesAt(base, ids)
}

// purgeSessionFilesAt is PurgeSessionFiles over an explicit base (tests).
func purgeSessionFilesAt(base string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var errs []string
	for _, sid := range ids {
		if sid == "" {
			continue
		}
		targets := []string{
			filepath.Join(base, "rollout", "model-io-"+sid+".jsonl"),
			filepath.Join(base, "exec", sid),
			filepath.Join(base, "image-cache", sid),
		}
		for _, p := range targets {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			if err := os.RemoveAll(p); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", p, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
