package zcode

// purge_test.go — the factory reset's ZCode half, exercised against
// temp sqlite files through the native store paths production rides.
// The property under test is SCOPE:
// every studio-born row/session/file goes, and the operator's own
// work —「重新编译」「commit」, a 【】-titled but rank-less session of
// their own — survives untouched.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makePurgeTasksDB builds the tasks table with the columns the purge
// reads (task_id, title, meta_json) plus a recognizable filler.
func makePurgeTasksDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "tasks-index.sqlite")
	execSQL(t, db, `create table tasks (
  workspace_key text not null, task_id text not null,
  title text, task_status text, provider text, mode text, model text,
  created_at integer, updated_at integer, unread_at integer,
  pinned integer not null default 0, archived integer not null default 0,
  deleted integer not null default 0, title_overridden integer,
  meta_json text not null default '{}',
  primary key (workspace_key, task_id))`)
	return db
}

// seedTask inserts one row (meta is a bare JSON fragment; "{}" is the
// operator's own default).
func seedTask(t *testing.T, db, ws, sid, title, meta string) {
	t.Helper()
	execSQL(t, db, "insert into tasks (workspace_key, task_id, title, meta_json, created_at, updated_at)"+
		" values ('"+ws+"', '"+sid+"', '"+title+"', '"+meta+"', 1, 1)")
}

func remainingTaskIDs(t *testing.T, db string) []string {
	t.Helper()
	return queryColumn(t, db, "select task_id from tasks order by task_id")
}

// queryColumn prints one first-column value per row — the multi-row
// probe the fetchone-shaped shared query can't answer.
func queryColumn(t *testing.T, db, q string) []string {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer conn.Close()
	rows, err := conn.Query(q)
	if err != nil {
		t.Fatalf("column query failed: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// TestPurgeTasksScopeIsStudioBornRowsOnly: the three arms (dhEmployee
// marker / known session id / the 【…】…-Lv.N and 小助手 title
// contract) take every studio row — including rewritten and
// departed-member ones — while the operator's own rows survive.
func TestPurgeTasksScopeIsStudioBornRowsOnly(t *testing.T) {
	db := makePurgeTasksDB(t)
	const ws = "/office"
	seedTask(t, db, ws, "sess_member_live", "【Niuma_Studio】小牛-排期编排-Lv.8",
		`{"dhEmployee": true}`)
	seedTask(t, db, ws, "sess_assistant", "【Niuma_Studio】小助手-使用顾问",
		`{"dhEmployee": true}`)
	seedTask(t, db, ws, "sess_member_rewritten", "【项目甲】前端-切图-Lv.3", `{}`)
	seedTask(t, db, ws, "sess_departed_twin", "【Niuma_Studio】小马-人事-Lv.8", `{}`)
	seedTask(t, db, ws, "sess_member_title_overridden", "随便什么标题",
		`{}`) // 已知 id 兜底：标题被房主改掉也能按 id 删
	seedTask(t, db, ws, "sess_user_plain", "重新编译", `{}`)
	seedTask(t, db, ws, "sess_user_brackets", "【紧急】处理事务", `{}`) // 无 Lv/小助手槽：不是房间契约
	seedTask(t, db, ws, "sess_user_commit", "commit", `{"dhEmployee": false}`)

	purged, err := purgeTasksAt(db, []string{"sess_member_title_overridden"})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	// 脚本侧 sorted() 已排序
	want := []string{
		"sess_assistant", "sess_departed_twin", "sess_member_live",
		"sess_member_rewritten", "sess_member_title_overridden",
	}
	if strings.Join(purged, ",") != strings.Join(want, ",") {
		t.Fatalf("purged = %v, want %v", purged, want)
	}
	left := remainingTaskIDs(t, db)
	wantLeft := []string{"sess_user_brackets", "sess_user_commit", "sess_user_plain"}
	if strings.Join(left, ",") != strings.Join(wantLeft, ",") {
		t.Fatalf("残留 = %v, want %v（房主自己的行必须一根不碰）", left, wantLeft)
	}
}

// TestPurgeSessionsCascadesOnlyBorn: one delete per known session id
// takes its entries and messages with it (the FK cascade) and leaves
// the operator's session whole.
func TestPurgeSessionsCascadesOnlyBorn(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.sqlite")
	execSQL(t, db,
		"create table session (id text primary key, title text not null)",
		`create table session_entry (
  id text primary key, session_id text not null references session(id) on delete cascade,
  type text not null)`,
		`create table message (
  id text primary key, session_id text not null references session(id) on delete cascade,
  body text not null)`,
		"insert into session values ('sess_born_a', 't')",
		"insert into session values ('sess_born_b', 't')",
		"insert into session values ('sess_user_own', 't')",
		"insert into session_entry values ('e1_sess_born_a', 'sess_born_a', 'x')",
		"insert into session_entry values ('e1_sess_born_b', 'sess_born_b', 'x')",
		"insert into session_entry values ('e1_sess_user_own', 'sess_user_own', 'x')",
		"insert into message values ('m1_sess_born_a', 'sess_born_a', 'hello')",
		"insert into message values ('m1_sess_born_b', 'sess_born_b', 'hello')",
		"insert into message values ('m1_sess_user_own', 'sess_user_own', 'hello')")
	if err := purgeSessionsAt(db, []string{"sess_born_a", "sess_born_b"}); err != nil {
		t.Fatalf("purge sessions: %v", err)
	}
	if got := queryColumn(t, db, "select id from session"); len(got) != 1 || got[0] != "sess_user_own" {
		t.Fatalf("session 残留 = %v, want [sess_user_own]（只剩房主自己的）", got)
	}
	if got := queryColumn(t, db, "select session_id from message"); len(got) != 1 || got[0] != "sess_user_own" {
		t.Fatalf("message 级联残留 = %v, want [sess_user_own]", got)
	}
	if got := queryColumn(t, db, "select session_id from session_entry"); len(got) != 1 || got[0] != "sess_user_own" {
		t.Fatalf("session_entry 级联残留 = %v, want [sess_user_own]", got)
	}
}

// TestPurgeSessionFilesTakesOnlyBorn: rollout/exec/image-cache entries
// keyed by the born ids go; a stranger's entries beside them stay.
func TestPurgeSessionFilesTakesOnlyBorn(t *testing.T) {
	base := t.TempDir()
	mk := func(rel string) {
		t.Helper()
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mk("rollout/model-io-sess_born.jsonl")
	mk("rollout/model-io-sess_user.jsonl")
	mk("exec/sess_born/sub")
	mk("exec/sess_user/sub")
	mk("image-cache/sess_born/img.png")
	mk("image-cache/sess_user/img.png")

	if err := purgeSessionFilesAt(base, []string{"sess_born"}); err != nil {
		t.Fatalf("purge files: %v", err)
	}
	for _, gone := range []string{
		"rollout/model-io-sess_born.jsonl", "exec/sess_born", "image-cache/sess_born",
	} {
		if _, err := os.Stat(filepath.Join(base, gone)); !os.IsNotExist(err) {
			t.Errorf("%s 应已删除", gone)
		}
	}
	for _, stay := range []string{
		"rollout/model-io-sess_user.jsonl", "exec/sess_user", "image-cache/sess_user",
	} {
		if _, err := os.Stat(filepath.Join(base, stay)); err != nil {
			t.Errorf("%s 不得误删: %v", stay, err)
		}
	}
}
