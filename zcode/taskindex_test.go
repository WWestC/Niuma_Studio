package zcode

// taskindex_test.go — exercises the sidebar seams against a real
// sqlite file through the native store paths production rides
// (modernc/sqlite, in-process — the retired python seam carried a
// bare-Windows 9009 with it).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// execSQL runs one statement batch against db — the fixtures' legs,
// retired along with the python seam they rode.
func execSQL(t *testing.T, db string, statements ...string) {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	for _, st := range statements {
		if _, err := conn.Exec(st); err != nil {
			t.Fatalf("fixture exec failed: %v\nsql: %s", err, st)
		}
	}
}

// queryRow returns the first row shaped like python's fetchone repr —
// ints bare, text single-quoted, NULL as None, no row as "None" — so
// the assertions below keep the seam era's exact strings.
func queryRow(t *testing.T, db, q string, args ...any) string {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer conn.Close()
	rows, err := conn.Query(q, args...)
	if err != nil {
		t.Fatalf("probe query failed: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "None"
	}
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatal(err)
	}
	parts := make([]string, len(cols))
	for i, v := range vals {
		switch x := v.(type) {
		case nil:
			parts[i] = "None"
		case int64:
			parts[i] = fmt.Sprintf("%d", x)
		case float64:
			parts[i] = fmt.Sprintf("%v", x)
		case []byte:
			parts[i] = "'" + string(x) + "'"
		case string:
			parts[i] = "'" + x + "'"
		default:
			parts[i] = fmt.Sprintf("%v", x)
		}
	}
	body := "(" + strings.Join(parts, ", ")
	if len(parts) == 1 {
		body += "," // python 单元素元组的 repr 带尾逗号
	}
	return body + ")"
}

// makeTasksDB builds a temp tasks table carrying the columns the
// stamp/archive paths touch (a faithful subset — the real table's
// NOT NULL defaults don't change what these paths read or write).
func makeTasksDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "tasks-index.sqlite")
	execSQL(t, db, `create table tasks (
  workspace_key text not null, task_id text not null,
  unread_at integer, last_unread_at integer not null default 0,
  updated_at integer not null, pinned integer not null default 0,
  archived integer not null default 0,
  task_status text, meta_json text not null default '{}',
  searchable_text text,
  primary key (workspace_key, task_id))`)
	return db
}

// makeClapDB builds the CLI conversation store's message/part subset
// the searchable mirror reads (same shapes, same ordering keys).
func makeClapDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db.sqlite")
	execSQL(t, db,
		`create table message (
  id text primary key, session_id text not null,
  time_created integer not null, time_updated integer not null,
  data text not null, sequence integer)`,
		`create table part (
  id text primary key, message_id text not null,
  session_id text not null,
  time_created integer not null, time_updated integer not null,
  data text not null, sequence integer)`)
	return db
}

type seededMessage struct {
	id   string
	data string // full message JSON
	ents []string
}

// seedConversation loads one session's messages and their text parts
// (part ordering follows insertion with rising sequence).
func seedConversation(t *testing.T, db, sid string, msgs []seededMessage) {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("open clap fixture: %v", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	for mi, m := range msgs {
		if _, err := conn.Exec(
			"insert into message (id, session_id, time_created, time_updated, data, sequence)"+
				" values (?,?,?,?,?,?)",
			m.id, sid, mi*100, mi*100, m.data, mi); err != nil {
			t.Fatalf("seed message %s: %v", m.id, err)
		}
		for j, ent := range m.ents {
			data, _ := json.Marshal(map[string]string{"type": "text", "text": ent})
			if _, err := conn.Exec(
				"insert into part (id, message_id, session_id, time_created, time_updated, data, sequence)"+
					" values (?,?,?,?,?,?,?)",
				fmt.Sprintf("%s_p%d", m.id, j), m.id, sid, mi*100+j, mi*100+j, string(data), j); err != nil {
				t.Fatalf("seed part %s_p%d: %v", m.id, j, err)
			}
		}
	}
}

// assertSearchable fails the test unless the row's searchable_text
// equals want exactly.
func assertSearchable(t *testing.T, db, sid, want string) {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	defer conn.Close()
	var got any
	if err := conn.QueryRow(
		"select searchable_text from tasks where task_id=?", sid).Scan(&got); err != nil {
		t.Fatalf("searchable_text 断言失败: %v", err)
	}
	if s, _ := got.(string); s != want {
		t.Fatalf("mirror mismatch:\n got: %q\nwant: %q", s, want)
	}
}

// makeFullTasksDB builds the register path's whole column set — the
// upsert touches every one of them, so its tests ride the full shape.
func makeFullTasksDB(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "tasks-index.sqlite")
	execSQL(t, db, `create table tasks (
  workspace_key text not null, workspace_path text,
  workspace_identity text, task_id text not null,
  title text, task_status text, provider text, mode text, model text,
  created_at integer, updated_at integer, unread_at integer,
  pinned integer not null default 0, archived integer not null default 0,
  deleted integer not null default 0, title_overridden integer,
  meta_json text not null default '{}', searchable_text text,
  cron_automation_id text, last_unread_at integer,
  off_peak_task_id text, migration_source text, forked_from_task_id text,
  primary key (workspace_key, task_id))`)
	return db
}

// TestRegisterScriptFoldsDisplacedTwinRows pins the 「两个小马」cure:
// registering a member's fresh session folds every still-unarchived
// row carrying the SAME contract title under a different task_id —
// the previous session the binding moved off of — in any workspace
// (a stale-home twin is equally a ghost), while an unrelated title
// and the new row itself stay untouched. A ghost with malformed
// meta_json (the desktop tolerates them; so must the fold) folds
// without failing the upsert.
func TestRegisterScriptFoldsDisplacedTwinRows(t *testing.T) {
	db := makeFullTasksDB(t)
	execSQL(t, db,
		"insert into tasks (workspace_key, task_id, title, task_status, pinned, archived, meta_json) values ('/office', 'sess_old', '【Niuma_Studio】小马-人事-Lv.8', 'running', 1, 0, '{}')",
		"insert into tasks (workspace_key, task_id, title, task_status, pinned, archived, meta_json) values ('/stale', 'sess_old_stale', '【Niuma_Studio】小马-人事-Lv.8', 'running', 1, 0, 'not-json-at-all')",
		"insert into tasks (workspace_key, task_id, title, task_status, pinned, archived, meta_json) values ('/office', 'sess_other', '【Niuma_Studio】小牛-排期编排-Lv.8', 'running', 1, 0, '{}')")
	if err := registerTaskIndexAt(db, TaskIndexEntry{WorkspacePath: "/office", SessionID: "sess_new",
		Title: "【Niuma_Studio】小马-人事-Lv.8", Mode: "yolo", Provider: "glm", Status: "running"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if got := queryRow(t, db, "select pinned, archived from tasks where task_id='sess_new'"); got != "(1, 0)" {
		t.Fatalf("新会话行应置顶未归档，得 %s", got)
	}
	for _, ghost := range []string{"sess_old", "sess_old_stale"} {
		if got := queryRow(t, db, "select pinned, archived from tasks where task_id='"+ghost+"'"); got != "(0, 1)" {
			t.Fatalf("被顶替的旧会话行 %s 应折叠（置顶清、归档落），得 %s", ghost, got)
		}
	}
	if got := queryRow(t, db, "select pinned, archived from tasks where task_id='sess_other'"); got != "(1, 0)" {
		t.Fatalf("无关成员的行不应被折叠，得 %s", got)
	}
}

// TestRegisterScriptRefoldKeepsFreshRow pins the fold's aim: a second
// registration of the SAME session (the assistant's cross-boot
// adoption, a backfill) must never fold its own row — the ghost match
// excludes the task_id being registered.
func TestRegisterScriptRefoldKeepsFreshRow(t *testing.T) {
	db := makeFullTasksDB(t)
	if err := registerTaskIndexAt(db, TaskIndexEntry{WorkspacePath: "/office", SessionID: "sess_keep",
		Title: "【Niuma_Studio】小助手-使用顾问", Mode: "yolo", Provider: "glm", Status: "running"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registerTaskIndexAt(db, TaskIndexEntry{WorkspacePath: "/office", SessionID: "sess_keep",
		Title: "【Niuma_Studio】小助手-使用顾问", Mode: "yolo", Provider: "glm", Status: "running"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := queryRow(t, db, "select pinned, archived, task_status from tasks where task_id='sess_keep'"); got != "(1, 0, 'running')" {
		t.Fatalf("重注册不得折叠自身行，得 %s", got)
	}
}

func TestTaskUnreadScriptStampsRow(t *testing.T) {
	db := makeTasksDB(t)
	execSQL(t, db, "insert into tasks (workspace_key, task_id, unread_at, last_unread_at, updated_at) values ('/ws', 'sess_a', NULL, 0, 111)")

	before := time.Now().UnixMilli()
	if err := markTaskUnreadAt(db, "", "sess_a"); err != nil {
		t.Fatalf("unread: %v", err)
	}

	got := queryRow(t, db, "select unread_at, last_unread_at, updated_at from tasks where task_id='sess_a'")
	// repr shape: (unread_at, last_unread_at, updated_at) — three equal,
	// post-stamp millis (>= before; python's clock is the same host's).
	for _, field := range strings.Split(strings.Trim(got, "()"), ", ") {
		if field == "None" {
			t.Fatalf("stamp left a field NULL: %s", got)
		}
		var v int64
		if _, err := fmt.Sscan(field, &v); err != nil {
			t.Fatalf("non-numeric field %q in %s", field, got)
		}
		if v < before {
			t.Fatalf("stamp wrote a pre-stamp timestamp: %s (before=%d)", got, before)
		}
	}
	if !strings.Contains(got, "(") {
		t.Fatalf("unexpected query shape: %s", got)
	}
}

func TestTaskUnreadScriptUnknownSessionIsNoop(t *testing.T) {
	db := makeTasksDB(t)
	if err := markTaskUnreadAt(db, "", "sess_nobody"); err != nil { // must not error
		t.Fatalf("unknown-session stamp errored: %v", err)
	}
	if got := queryRow(t, db, "select count(*) from tasks"); got != "(0,)" { // python's one-tuple repr keeps the comma
		t.Fatalf("unknown-session stamp should touch nothing, got rows: %s", got)
	}
}

func TestTaskUnreadScriptRestampOverwrites(t *testing.T) {
	db := makeTasksDB(t)
	execSQL(t, db, "insert into tasks (workspace_key, task_id, unread_at, last_unread_at, updated_at) values ('/ws', 'sess_a', 5, 5, 5)")
	if err := markTaskUnreadAt(db, "", "sess_a"); err != nil {
		t.Fatalf("unread: %v", err)
	}
	first := queryRow(t, db, "select unread_at from tasks where task_id='sess_a'")
	time.Sleep(2 * time.Millisecond) // cross at least one ms tick
	if err := markTaskUnreadAt(db, "", "sess_a"); err != nil {
		t.Fatalf("unread: %v", err)
	}
	second := queryRow(t, db, "select unread_at from tasks where task_id='sess_a'")
	var a, b int64
	if _, err := fmt.Sscan(strings.Trim(first, "()"), &a); err != nil {
		t.Fatalf("first stamp unreadable: %s", first)
	}
	if _, err := fmt.Sscan(strings.Trim(second, "()"), &b); err != nil {
		t.Fatalf("second stamp unreadable: %s", second)
	}
	if b < a {
		t.Fatalf("restamp went backwards: %d -> %d", a, b)
	}
}

func TestMarkTaskUnreadGuard(t *testing.T) {
	if err := MarkTaskUnread(""); err == nil {
		t.Fatal("empty session must be refused, not a spawn")
	}
}

// TestUnreadScriptMirrorsConversationText pins the searchable mirror's
// desktop-syncer shaping: user inputs keep ALL text parts, assistant
// turns keep only the LAST text part, model-only synthetic turns and
// non-chat roles stay out, empty parts drop — and the dot lands in the
// same pass. This is the 「侧栏搜索正文」 half of the reply stamp.
func TestUnreadScriptMirrorsConversationText(t *testing.T) {
	db := makeTasksDB(t)
	execSQL(t, db, "insert into tasks (workspace_key, task_id, unread_at, last_unread_at, updated_at, searchable_text) values ('/ws', 'sess_a', NULL, 0, 111, '【Niuma_Studio】小牛-排期编排-Lv.8')")
	clap := makeClapDB(t)
	seedConversation(t, clap, "sess_a", []seededMessage{
		{"m1", `{"role":"user"}`, []string{"【办公室消息】@小牛 请拆解 r_01", "", "  ", "第二条输入"}},
		{"m2", `{"role":"assistant"}`, []string{"草稿段落", "正文回复：已拆解 r_01 为三项任务"}},
		{"m3", `{"role":"user","metadata":{"source":"todo_reminder","visibility":"model-only"}}`, []string{"内部提醒不该被搜到"}},
		{"m4", `{"role":"system"}`, []string{"系统角色不是聊天正文"}},
		{"m5", `{"role":"user","semantics":{"kind":"compact_summary"}}`, []string{"压缩摘要不该被搜到"}},
		{"m6", `{"role":"assistant"}`, []string{"收尾回复"}},
	})

	if err := markTaskUnreadAt(db, clap, "sess_a"); err != nil {
		t.Fatalf("unread: %v", err)
	}

	assertSearchable(t, db, "sess_a",
		"【办公室消息】@小牛 请拆解 r_01\n第二条输入\n正文回复：已拆解 r_01 为三项任务\n收尾回复")
	if got := queryRow(t, db, "select unread_at is not null from tasks where task_id='sess_a'"); got != "(1,)" {
		t.Fatalf("回填 pass 必须同时盖红点，得 %s", got)
	}
}

// TestUnreadScriptCapsMirrorText pins the 200k-char ceiling — the
// desktop syncer's own TASK_SEARCH_TEXT_MAX_CHARS: a six-hundred-turn
// veteran must not bloat tasks-index.sqlite past what native rows
// would carry.
func TestUnreadScriptCapsMirrorText(t *testing.T) {
	db := makeTasksDB(t)
	execSQL(t, db, "insert into tasks (workspace_key, task_id, unread_at, last_unread_at, updated_at) values ('/ws', 'sess_big', NULL, 0, 1)")
	clap := makeClapDB(t)
	seedConversation(t, clap, "sess_big", []seededMessage{
		{"m1", `{"role":"user"}`, []string{strings.Repeat("a", 150000)}},
		{"m2", `{"role":"assistant"}`, []string{strings.Repeat("b", 150000)}},
	})

	if err := markTaskUnreadAt(db, clap, "sess_big"); err != nil {
		t.Fatalf("unread: %v", err)
	}

	if got := queryRow(t, db, "select length(searchable_text) from tasks where task_id='sess_big'"); got != "(200000,)" {
		t.Fatalf("正文镜像应截在 200000 字符，得 %s", got)
	}
}

// TestUnreadScriptMissingClapKeepsDot pins the degradation: a missing
// or unreadable conversation store costs only the search text — the
// dot itself must still land (the stamp's original contract).
func TestUnreadScriptMissingClapKeepsDot(t *testing.T) {
	db := makeTasksDB(t)
	execSQL(t, db, "insert into tasks (workspace_key, task_id, unread_at, last_unread_at, updated_at, searchable_text) values ('/ws', 'sess_a', NULL, 0, 111, '种子标题')")

	if err := markTaskUnreadAt(db, filepath.Join(t.TempDir(), "nope.sqlite"), "sess_a"); err != nil {
		t.Fatalf("unread: %v", err)
	}

	if got := queryRow(t, db, "select unread_at is not null, searchable_text from tasks where task_id='sess_a'"); got != "(1, '种子标题')" {
		t.Fatalf("无对话库应退化为只盖红点、正文保持原值，得 %s", got)
	}
}

// TestRegisterScriptKeepsSearchableText pins the hand-off: the register
// seeds searchable_text with the title at birth, but a later
// re-registration (rank refresh, cross-boot adoption, rebirth) must
// NOT clobber the conversation mirror the reply stamps maintain.
func TestRegisterScriptKeepsSearchableText(t *testing.T) {
	db := makeFullTasksDB(t)
	if err := registerTaskIndexAt(db, TaskIndexEntry{WorkspacePath: "/office", SessionID: "sess_keep",
		Title: "【Niuma_Studio】小助手-使用顾问", Mode: "yolo", Provider: "glm", Status: "running"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := queryRow(t, db, "select searchable_text from tasks where task_id='sess_keep'"); got != "('【Niuma_Studio】小助手-使用顾问',)" {
		t.Fatalf("出生种子应是标题，得 %s", got)
	}
	execSQL(t, db, "update tasks set searchable_text='办公室消息与回复正文' where task_id='sess_keep'")
	if err := registerTaskIndexAt(db, TaskIndexEntry{WorkspacePath: "/office", SessionID: "sess_keep",
		Title: "【Niuma_Studio】小助手-使用顾问", Mode: "yolo", Provider: "glm", Status: "running"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := queryRow(t, db, "select searchable_text from tasks where task_id='sess_keep'"); got != "('办公室消息与回复正文',)" {
		t.Fatalf("重注册不得把正文镜像打回标题，得 %s", got)
	}
}

// TestParseLSBundlePath pins the LaunchServices route's parse: the
// desktop-app lookup must survive ZCode 3.14.4+'s argv rewrite (ps sees
// a bare "ZCode"), so the bundle path comes from lsappinfo instead —
// exact line shape in, bundle path out, anything else empty.
func TestParseLSBundlePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"LSBundlePath"="/Applications/ZCode.app"`, "/Applications/ZCode.app"},
		{`"LSBundlePath"="/Users/x/Apps/Z Code.app"`, "/Users/x/Apps/Z Code.app"},
		{"  \"LSBundlePath\"=\"/a.app\"  ", "/a.app"},
		{"", ""},
		{"NULL", ""},
		{`"LSSomethingElse"="/x"`, ""},
	}
	for _, c := range cases {
		if got := parseLSBundlePath(c.in); got != c.want {
			t.Fatalf("parseLSBundlePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNudgeArgvShapesPlatformChannel pins the dialog-free channel's
// exact spawn argv: Windows/Linux exec the running binary as before;
// macOS never spawns a second instance at all — both spawn shapes
// leave Dock tiles behind on current systems (raw exec = ghost tiles,
// open -n = launch tiles the Dock keeps), so darwin routes to the
// guarded deep link instead (nil here).
func TestNudgeArgvShapesPlatformChannel(t *testing.T) {
	const ws = "/Users/x/office"
	if got := nudgeArgv("", ws); got != nil {
		t.Fatalf("定位不到运行中的应用应回退深链，得 %v", got)
	}
	bin := "/Applications/ZCode.app/Contents/MacOS/ZCode"
	if runtime.GOOS == "darwin" {
		if got := nudgeArgv(bin, ws); got != nil {
			t.Fatalf("macOS 不投第二实例（两代形状都污染 Dock），得 %v", got)
		}
		return
	}
	want := []string{bin, "--open-workspace", ws}
	if got := nudgeArgv(bin, ws); !reflect.DeepEqual(got, want) {
		t.Fatalf("投递 argv = %v, want %v", got, want)
	}
}
