package cli

// The `kb docs` family: the AI-facing read/write surface of the v0.5
// room memory. Reads go over HTTP (/kb/docs*), writes go over WS
// (kb_write/kb_append/kb_restore) with the same seat hygiene and
// three-state output as the task CLI — reusing its helpers directly.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"

	"github.com/coder/websocket/wsjson"
)

// runKbDocs prints the manual index (kb docs). Scoped runs list the
// 公共文档库＋本房项目文档库 (the isolation read face).
func runKbDocs(args []string) int {
	fs := flag.NewFlagSet("kb docs", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	_ = pos
	port, ok := tf.resolvePort("kb docs")
	if !ok {
		return 1
	}
	var metas []kb.DocMeta
	if !taskGet(port, withQuery("/kb/docs", "project", pickScope(*project)), &metas, "kb docs") {
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(metas)
		return 0
	}
	if len(metas) == 0 {
		fmt.Println("（没有手册）")
		return 0
	}
	fmt.Printf("共 %d 篇（按更新时间倒序）\n", len(metas))
	for _, m := range metas {
		fmt.Printf("%s\trev %-3d %s\t%s\t%d 字节\n",
			m.Key, m.Rev, m.Title, shortWho(m.UpdatedBy, m.UpdatedTS), m.Size)
	}
	return 0
}

// runKbDoc prints one manual (kb doc KEY), current or --rev N. A
// scoped run reading another office's shelf answers 404 on the
// server — same discipline as an unknown key.
func runKbDoc(args []string) int {
	fs := flag.NewFlagSet("kb doc", flag.ContinueOnError)
	tf := &taskFlags{}
	rev := fs.Int("rev", 0, "read this revision instead of the current one")
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "kb doc: key required, e.g. kb doc roles/hr")
		return 2
	}
	port, ok := tf.resolvePort("kb doc")
	if !ok {
		return 1
	}
	revStr := ""
	if *rev > 0 {
		revStr = strconv.Itoa(*rev)
	}
	path := withQuery("/kb/docs/"+pos[0], "rev", revStr, "project", pickScope(*project))
	var doc kb.Doc
	if !taskGet(port, path, &doc, "kb doc") {
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc)
		return 0
	}
	fmt.Printf("%s  %s\n", doc.Key, doc.Title)
	fmt.Printf("owner %s · 更新 %s · rev %d\n", doc.Owner, shortWho(doc.UpdatedBy, doc.UpdatedTS), doc.Rev)
	if doc.Body != "" {
		fmt.Println(doc.Body)
	}
	return 0
}

// kbHistoryRow mirrors the /kb/docs/{key}/history projection.
type kbHistoryRow struct {
	Rev   int    `json:"rev"`
	By    string `json:"by"`
	TS    int64  `json:"ts"`
	Title string `json:"title"`
	Size  int    `json:"size"`
}

// runKbHistory prints a manual's version chain (kb history KEY).
func runKbHistory(args []string) int {
	fs := flag.NewFlagSet("kb history", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "kb history: key required, e.g. kb history roles/hr")
		return 2
	}
	port, ok := tf.resolvePort("kb history")
	if !ok {
		return 1
	}
	var chain []kbHistoryRow
	if !taskGet(port, withQuery("/kb/docs/"+pos[0]+"/history", "project", pickScope(*project)), &chain, "kb history") {
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(chain)
		return 0
	}
	if len(chain) == 0 {
		fmt.Println("（没有历史版本——只有创建后未再修改的手册才这样）")
		return 0
	}
	for _, h := range chain {
		fmt.Printf("rev %-3d %s · %s · %d 字节\n", h.Rev, shortWho(h.By, h.TS), h.Title, h.Size)
	}
	return 0
}

// runKbWrite sends kb_write (kb write KEY --title T --body B | --file F).
func runKbWrite(args []string) int {
	fs := flag.NewFlagSet("kb write", flag.ContinueOnError)
	tf := &taskFlags{}
	title := fs.String("title", "", "doc title (default: the key)")
	body := fs.String("body", "", "body text (--file alternative)")
	file := fs.String("file", "", "read the body from this file")
	expectRev := fs.Int("expect-rev", -1, "conditional write (v0.6): the rev you read — mismatch refuses with the current rev; 0 = must not exist yet; unset = overwrite")
	project := fs.String("project", "", "documental project tag (r_32 t_124: the server routes by the connection's room, this field is the audit log's breadcrumb)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "kb write: key required, e.g. kb write roles/hr --title 手册 --file m.md")
		return 2
	}
	text, err := bodyFromSpec(*body, *file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb write:", err)
		return 2
	}
	if tf.name == "" {
		return failNoName("kb write")
	}
	frame := chat.Message{Type: chat.MsgKbWrite, Project: pickScope(*project),
		KbDoc: &chat.KbDoc{Key: pos[0], Title: *title, Body: text}}
	if *expectRev >= 0 { // negative = unset = plain overwrite (v0.6 §6.3)
		rev := *expectRev
		frame.ExpectRev = &rev
	}
	return runKbWriteFrame(tf, "kb write", frame)
}

// runKbAppend sends kb_append (kb append KEY TEXT | --file F).
func runKbAppend(args []string) int {
	fs := flag.NewFlagSet("kb append", flag.ContinueOnError)
	tf := &taskFlags{}
	file := fs.String("file", "", "read the appended line from this file")
	project := fs.String("project", "", "documental project tag (r_32 t_124: the server routes by the connection's room, this field is the audit log's breadcrumb)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "kb append: key required, e.g. kb append ops/room-log \"Bot 已上岗\"")
		return 2
	}
	var text string
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kb append:", err)
			return 2
		}
		text = string(b)
	} else if len(pos) > 1 {
		text = strings.Join(pos[1:], " ")
	} else {
		fmt.Fprintln(os.Stderr, "kb append: text (or --file) required")
		return 2
	}
	if tf.name == "" {
		return failNoName("kb append")
	}
	frame := chat.Message{Type: chat.MsgKbAppend, Key: pos[0], Text: text, Project: pickScope(*project)}
	return runKbWriteFrame(tf, "kb append", frame)
}

// runKbRestore sends kb_restore (kb restore KEY --rev N).
func runKbRestore(args []string) int {
	fs := flag.NewFlagSet("kb restore", flag.ContinueOnError)
	tf := &taskFlags{}
	rev := fs.Int("rev", 0, "revision to restore")
	project := fs.String("project", "", "documental project tag (r_32 t_124: the server routes by the connection's room, this field is the audit log's breadcrumb)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 || *rev <= 0 {
		fmt.Fprintln(os.Stderr, "kb restore: key and --rev required, e.g. kb restore roles/hr --rev 3")
		return 2
	}
	if tf.name == "" {
		return failNoName("kb restore")
	}
	frame := chat.Message{Type: chat.MsgKbRestore, Key: pos[0], Rev: *rev, Project: pickScope(*project)}
	return runKbWriteFrame(tf, "kb restore", frame)
}

// runKbArchive sends kb_archive / kb_unarchive (v3 governance): a
// reversible shelf move — hidden from the default list, free of the
// doc cap, readable at its key. Permission rides the server's gate
// (host / owner / rank ≥ owner).
func runKbArchive(args []string, archive bool) int {
	cmd := "kb unarchive"
	op := chat.MsgKbUnarchive
	if archive {
		cmd = "kb archive"
		op = chat.MsgKbArchive
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintf(os.Stderr, "%s: key required, e.g. %s design/r01-scene\n", cmd, cmd)
		return 2
	}
	if tf.name == "" {
		return failNoName(cmd)
	}
	return runKbWriteFrame(tf, cmd, chat.Message{Type: op, Key: pos[0]})
}

// runKbDelete sends kb_delete (v3 governance): host-only — the doc,
// its history tail and its list row all go; the WAL keeps the audit
// event. 归档可逆，删除不是：先想归档。
func runKbDelete(args []string) int {
	fs := flag.NewFlagSet("kb delete", flag.ContinueOnError)
	tf := &taskFlags{}
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "kb delete: key required, e.g. kb delete design/scratch")
		return 2
	}
	if tf.name == "" {
		return failNoName("kb delete")
	}
	return runKbWriteFrame(tf, "kb delete", chat.Message{Type: chat.MsgKbDelete, Key: pos[0]})
}

// runKbExport dumps the whole docs store (active + archived) as a
// markdown tree under DIR (default ./niuma-kb-export): one .md per
// doc at its key path. The v3 read-only human snapshot — exported
// files are never read back; the database under ~/.niuma/kb is the
// single source of truth.
func runKbExport(args []string) int {
	fs := flag.NewFlagSet("kb export", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	dir := "niuma-kb-export"
	if len(pos) > 0 {
		dir = pos[0]
	}
	port, ok := tf.resolvePort("kb export")
	if !ok {
		return 1
	}
	scope := pickScope(*project)
	var metas []kb.DocMeta
	if !taskGet(port, withQuery("/kb/docs", "project", scope), &metas, "kb export") {
		return 1
	}
	var archived []kb.DocMeta
	taskGet(port, withQuery("/kb/docs", "archived", "1", "project", scope), &archived, "kb export") // absence is fine
	metas = append(metas, archived...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "kb export:", err)
		return 1
	}
	n := 0
	for _, m := range metas {
		var doc kb.Doc
		if !taskGet(port, withQuery("/kb/docs/"+m.Key, "project", scope), &doc, "kb export") {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(m.Key)+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "kb export:", err)
			return 1
		}
		if err := os.WriteFile(p, []byte(doc.Body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "kb export:", err)
			return 1
		}
		mark := ""
		if m.Archived {
			mark = "（归档）"
		}
		fmt.Printf("  %s %s\n", p, mark)
		n++
	}
	fmt.Printf("kb export: %d 篇 → %s（只读快照，不回灌；真源是 ~/.niuma/kb 数据库）\n", n, dir)
	return 0
}

// runKbWriteFrame performs one WS write and waits for this member's kb
// event: a private "denied" or the broadcast carrying the new rev —
// the same contract as runTaskWrite.
func runKbWriteFrame(tf *taskFlags, cmd string, frame chat.Message) int {
	port, ok := tf.resolvePort(cmd)
	if !ok {
		return 1
	}
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, me, err := dialTask(ctx, url, tf.name, cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 1
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, frame); err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 1
	}
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "%s: no answer from the room: %v\n", cmd, err)
			return 1
		}
		if m.Type != chat.MsgKb || m.From != me.Name {
			continue
		}
		if tf.json {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		}
		if m.Event == "denied" {
			if !tf.json {
				if m.CurrentRev > 0 {
					// the exact shape the v0.6 spec prescribes: the
					// expectation, the reality, and the way out
					fmt.Fprintf(os.Stderr, "%s: rev 冲突：期望 %s，当前 %d（重读合并后用 --expect-rev %d 重试）\n",
						cmd, expectLabel(frame), m.CurrentRev, m.CurrentRev)
				} else {
					fmt.Fprintf(os.Stderr, "%s: 被拒：%s\n", cmd, m.Text)
				}
			}
			taskBye(ctx, conn)
			return 1
		}
		if !tf.json {
			verb := map[string]string{"written": "已写入", "appended": "已追加", "restored": "已恢复",
				"archived": "已归档", "unarchived": "已出档", "deleted": "已删除"}[m.Event]
			if verb == "" {
				verb = m.Event
			}
			if m.KbDoc != nil {
				fmt.Printf("[%s] %s %s rev=%d\n", me.Name, verb, m.KbDoc.Key, m.KbDoc.Rev)
			} else if m.Key != "" {
				fmt.Printf("[%s] %s %s\n", me.Name, verb, m.Key)
			}
		}
		taskBye(ctx, conn)
		return 0
	}
}

// expectLabel renders the frame's expect_rev for the conflict message
// (nil reads as unset, 0 as "must not exist").
func expectLabel(frame chat.Message) string {
	if frame.ExpectRev == nil {
		return "(未设置)"
	}
	if *frame.ExpectRev == 0 {
		return "0（必须不存在）"
	}
	return strconv.Itoa(*frame.ExpectRev)
}

// bodyFromSpec resolves the --body / --file pair (exactly one wins;
// --file keeps newlines intact for real markdown). A body past the
// store's cap REFUSES (v3 discipline, matching the server's
// BodyLimitError) — the old client-side truncation silently lost the
// tail; now the author decides how to split.
func bodyFromSpec(body, file string) (string, error) {
	if file != "" {
		if body != "" {
			return "", fmt.Errorf("--body 与 --file 二选一")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		body = string(b)
	}
	if body == "" {
		return "", fmt.Errorf("--body 或 --file 必填")
	}
	if len(body) > kb.DocMaxBody || len([]rune(body)) > kb.DocMaxBody {
		return "", fmt.Errorf("正文 %d 字超过上限 %d——不再截断，请拆分或分卷后重写", len([]rune(body)), kb.DocMaxBody)
	}
	return body, nil
}

func shortWho(by string, ts int64) string {
	if ts > 0 {
		return fmt.Sprintf("%s %s", by, time.Unix(ts, 0).Format("01-02 15:04"))
	}
	return by
}

// runKbEstablishment prints the establishment reconciliation table
// (v0.8 M3): 岗位 / 编制 / 在岗 / 状态 per row. Scoped runs (the
// workspace binding or --project KEY) read THAT project's own table
// via /p/{key}/establishment; the bare unscoped call keeps the lobby's
// ops/establishment (the host's face). A broken table is an alarm
// (stderr + exit 1), never guessed rows.
func runKbEstablishment(args []string) int {
	fs := flag.NewFlagSet("kb establishment", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key whose table to read (default: the workspace's bound project — the lobby's table when unbound)")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	port, ok := tf.resolvePort("kb establishment")
	if !ok {
		return 1
	}
	path := "/kb/establishment"
	if scope := pickScope(*project); scope != "" && scope != "default" {
		path = fmt.Sprintf("/p/%s/establishment", scope)
	}
	var report struct {
		Rows []struct {
			Key       string   `json:"key"`
			Headcount int      `json:"headcount"`
			AutoFill  bool     `json:"auto_fill"`
			Must      bool     `json:"must"`
			System    bool     `json:"system"`
			Present   []string `json:"present"`
			Missing   int      `json:"missing"`
		} `json:"rows"`
		ParseError string `json:"parse_error"`
	}
	if !taskGet(port, path, &report, "kb establishment") {
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		if report.ParseError != "" {
			return 1
		}
		return 0
	}
	if report.ParseError != "" {
		fmt.Fprintf(os.Stderr, "kb establishment: 编制表解析失败——%s\n", report.ParseError)
		return 1
	}
	fmt.Printf("%-12s %-4s %-16s %s\n", "岗位", "编制", "在岗", "状态")
	for _, r := range report.Rows {
		present := strings.Join(r.Present, "、")
		if present == "" {
			present = "—"
		}
		status := "✓"
		if r.Missing > 0 {
			fill := "否"
			if r.AutoFill {
				fill = "是"
			}
			status = fmt.Sprintf("缺 %d（自动补员=%s）", r.Missing, fill)
		}
		if r.Must {
			status += " ·必须（系统岗，创建时自动招募）"
		}
		fmt.Printf("%-12s %-4d %-16s %s\n", r.Key, r.Headcount, present, status)
	}
	return 0
}
