package cli

import (
	"github.com/WWestC/Niuma_Studio/util"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/tasks"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// runTask implements the task ledger CLI: list/show read over HTTP,
// create/update/confirm/decline write over WS (same read/write split
// as say/members). --name is the operator identity for writes.
func runTask(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma task <list|show|brief|create|update|confirm|decline|arbitrate> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runTaskList(rest)
	case "show":
		return runTaskShow(rest)
	case "brief":
		return runTaskBrief(rest)
	case "create":
		return runTaskCreate(rest)
	case "update":
		return runTaskUpdate(rest)
	case "confirm":
		return runTaskConfirmDecline(chat.MsgTaskConfirm, rest)
	case "decline":
		return runTaskConfirmDecline(chat.MsgTaskDecline, rest)
	case "arbitrate":
		return runTaskArbitrate(rest)
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "task: unknown subcommand %q (list|show|brief|create|update|confirm|decline|arbitrate)\n", sub)
		return 2
	}
}

// taskFlags is the shared flag set: port for reads, name+port for
// writes, json for both.
type taskFlags struct {
	port int
	name string
	json bool
}

// parseTaskFlags parses flags around positionals (agents write both
// orders) and returns the remaining positionals.
func parseTaskFlags(fs *flag.FlagSet, tf *taskFlags, args []string) ([]string, error) {
	fs.IntVar(&tf.port, "port", 0, "server port (default: auto-discover)")
	fs.StringVar(&tf.name, "name", "", "operator display name (writes)")
	fs.BoolVar(&tf.json, "json", false, "machine-readable output")
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (tf *taskFlags) resolvePort(cmd string) (int, bool) {
	if tf.port != 0 {
		return tf.port, true
	}
	p, err := server.DiscoverPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 0, false
	}
	return p, true
}

// taskGet fetches and decodes a /kb JSON endpoint.
func taskGet(port int, path string, v any, cmd string) bool {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintln(os.Stderr, cmd+": not found:", strings.TrimPrefix(path, "/kb/tasks"))
		return false
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return false
	}
	return true
}

// runTaskList prints the ledger, optionally filtered. The default
// project filter drinks from the CLI's scope (the workspace binding):
// a member session lists ITS project's tasks; the host — or "-" —
// keeps the studio-wide ledger.
func runTaskList(args []string) int {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	tf := &taskFlags{}
	assignee := fs.String("assignee", "", "filter by assignee")
	status := fs.String("status", "", "filter by status (todo|doing|done|cancelled)")
	project := fs.String("project", "", "filter by project key (default: the workspace's bound project; \"-\" = studio-wide, also sees unattached tasks)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	_ = pos
	*project = pickScope(*project)
	port, ok := tf.resolvePort("task list")
	if !ok {
		return 1
	}
	q := ""
	if *assignee != "" || *status != "" || *project != "" {
		q = "?"
		var parts []string
		if *assignee != "" {
			parts = append(parts, "assignee="+*assignee)
		}
		if *status != "" {
			parts = append(parts, "status="+*status)
		}
		if *project != "" {
			parts = append(parts, "project="+*project)
		}
		q += strings.Join(parts, "&")
	}
	var list []tasks.Task
	if !taskGet(port, "/kb/tasks"+q, &list, "task list") {
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(list)
		return 0
	}
	if len(list) == 0 {
		fmt.Println("（没有任务）")
		return 0
	}
	fmt.Printf("共 %d 条（按更新时间倒序）\n", len(list))
	for _, t := range list {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", t.ID, taskStateLabel(t),
			t.Title, taskOwnerLabel(t), t.Progress)
	}
	return 0
}

// runTaskShow prints one task's full detail. Scoped runs get 404 for
// another project's task (the server's not-found discipline).
func runTaskShow(args []string) int {
	fs := flag.NewFlagSet("task show", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "task show: id required, e.g. task show t_01")
		return 2
	}
	id := pos[0]
	port, ok := tf.resolvePort("task show")
	if !ok {
		return 1
	}
	var t tasks.Task
	if !taskGet(port, withQuery("/kb/tasks/"+id, "project", pickScope(*project)), &t, "task show") {
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(t)
		return 0
	}
	printTaskDetail(t)
	return 0
}

// runTaskBrief fetches and prints one task's assembled pickup brief —
// the one-page context read (requirement body, parents, deps, minutes,
// branch bindings) before starting or resuming a task. Plain text both
// ways: the server assembles, the CLI just carries it, so members pipe
// the same page humans read (--json wraps the same text in an envelope
// for scripts). Scope discipline matches task show.
func runTaskBrief(args []string) int {
	fs := flag.NewFlagSet("task brief", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "task brief: id required, e.g. task brief t_01")
		return 2
	}
	id := pos[0]
	port, ok := tf.resolvePort("task brief")
	if !ok {
		return 1
	}
	scope := pickScope(*project)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port,
		withQuery("/kb/tasks/"+id+"/brief", "project", scope)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "task brief:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "task brief: not found: %s（号段按项目分家——先 task list 看 id，或用 --project 指定架）\n", id)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "task brief: HTTP %d\n", resp.StatusCode)
		return 1
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "task brief:", err)
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(struct {
			Task    string `json:"task"`
			Project string `json:"project,omitempty"`
			Brief   string `json:"brief"`
		}{Task: id, Project: scope, Brief: string(body)})
		return 0
	}
	_, _ = os.Stdout.Write(body)
	return 0
}

func printTaskDetail(t tasks.Task) {
	fmt.Printf("任务   %s %s\n", t.ID, t.Title)
	fmt.Printf("状态   %s", tasks.StatusLabel(t.Status))
	if p := tasks.PriorityLabel(t.Priority); p != "" {
		fmt.Printf(" · 优先级 %s", p)
	}
	if t.Progress != "" {
		fmt.Printf(" · 进度 %s", t.Progress)
	}
	fmt.Println()
	owner := "（任务池，待认领）"
	if t.Assignee != "" {
		owner = t.Assignee
	}
	fmt.Printf("负责人 %s\n", owner)
	if t.CreatedBy != "" || t.CreatedTS > 0 {
		line := "创建   " + t.CreatedBy
		if t.CreatedTS > 0 {
			line += " · " + time.Unix(t.CreatedTS, 0).Format("01-02 15:04")
		}
		if t.UpdatedTS > 0 {
			line += " · 更新 " + time.Unix(t.UpdatedTS, 0).Format("01-02 15:04")
		}
		fmt.Println(line)
	}
	if t.Desc != "" {
		fmt.Println("描述:")
		for _, ln := range strings.Split(t.Desc, "\n") {
			fmt.Println("  " + ln)
		}
	}
	if p := t.Pending; p != nil {
		fmt.Printf("待确认 %s 申请: %s\n", p.By, patchDesc(p.Patch))
		if len(p.Got) > 0 {
			fmt.Printf("       已确认: %s\n", strings.Join(p.Got, "、"))
		}
		var rest []string
		for _, n := range p.Need {
			if !util.Contains(p.Got, n) {
				rest = append(rest, n)
			}
		}
		fmt.Printf("       剩余等待: %s\n", strings.Join(rest, "、"))
	}
	if len(t.Log) > 0 {
		fmt.Println("日志:")
		for _, e := range t.Log {
			ts := time.Unix(e.TS, 0).Format("01-02 15:04")
			for i, ln := range strings.Split(e.Note, "\n") {
				if i == 0 {
					fmt.Printf("  %s %s: %s\n", ts, e.By, ln)
				} else {
					fmt.Println("      " + ln)
				}
			}
		}
	}
}

func patchDesc(p tasks.Patch) string {
	var parts []string
	if p.Status != "" {
		parts = append(parts, "状态→"+tasks.StatusLabel(p.Status))
	}
	if p.Progress != "" {
		parts = append(parts, "进度 "+p.Progress)
	}
	if p.Assignee != "" {
		parts = append(parts, "负责人→"+p.Assignee)
	}
	if p.Note != "" {
		parts = append(parts, "备注 "+util.FirstLine(p.Note))
	}
	if p.Project != "" {
		if p.Project == "-" {
			parts = append(parts, "挂靠清除")
		} else {
			parts = append(parts, "挂靠→"+p.Project)
		}
	}
	if p.Version != "" {
		if p.Version == "-" {
			parts = append(parts, "版本清除")
		} else {
			parts = append(parts, "版本→"+p.Version)
		}
	}
	if p.Start != "" {
		parts = append(parts, "start "+p.Start)
	}
	if p.End != "" {
		parts = append(parts, "end "+p.End)
	}
	if p.Deps != "" {
		if p.Deps == "-" {
			parts = append(parts, "依赖清空")
		} else {
			parts = append(parts, "依赖→"+p.Deps)
		}
	}
	if p.Milestone != "" {
		parts = append(parts, "里程碑 "+p.Milestone)
	}
	if p.Pct != "" {
		parts = append(parts, "pct "+p.Pct+"%")
	}
	if p.Priority != "" {
		if p.Priority == "-" {
			parts = append(parts, "优先级清除")
		} else {
			parts = append(parts, "优先级→"+tasks.PriorityLabel(p.Priority))
		}
	}
	return strings.Join(parts, " · ")
}

// runTaskCreate sends task_create over WS.
func runTaskCreate(args []string) int {
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	tf := &taskFlags{}
	title := fs.String("title", "", "task title (required, ≤120 chars)")
	desc := fs.String("desc", "", "task description")
	assignee := fs.String("assignee", "", "assignee name (empty = task pool)")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(os.Stderr, "task create: --title required")
		return 2
	}
	if tf.name == "" {
		return failNoName("task create")
	}
	// v2.10：绑定工作区里建的单直接铸在本项目架（未绑定＝池单）
	frame := chat.Message{Type: chat.MsgTaskCreate,
		Project: pickScope(""),
		Task:    (&tasks.Task{Title: *title, Desc: *desc, Assignee: *assignee}).Wire()}
	return runTaskWrite(tf, "task create", frame)
}

// runTaskUpdate sends task_update over WS.
func runTaskUpdate(args []string) int {
	fs := flag.NewFlagSet("task update", flag.ContinueOnError)
	tf := &taskFlags{}
	status := fs.String("status", "", "todo|doing|done|cancelled")
	progress := fs.String("progress", "", "free-form progress, e.g. 60% (a percentage syncs --pct)")
	note := fs.String("note", "", "note appended to the task log")
	assignee := fs.String("assignee", "", "reassign to this name")
	project := fs.String("project", "", "project key to attach (\"-\" detaches to the lobby)")
	version := fs.String("version", "", "project version name (\"-\" clears)")
	start := fs.String("start", "", "planned start, YYYY-MM-DD HH:MM (\"-\" clears)")
	end := fs.String("end", "", "planned end, YYYY-MM-DD HH:MM (\"-\" clears)")
	deps := fs.String("deps", "", "prerequisite ids, t_01,t_02 (\"-\" empties)")
	milestone := fs.String("milestone", "", "true|false")
	pct := fs.String("pct", "", "numeric progress 0-100 (never touches the text)")
	priority := fs.String("priority", "", "urgent|high|medium|low (\"-\" clears to unset)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "task update: id required, e.g. task update t_01 --status doing")
		return 2
	}
	if *status == "" && *progress == "" && *note == "" && *assignee == "" &&
		*project == "" && *version == "" && *start == "" && *end == "" &&
		*deps == "" && *milestone == "" && *pct == "" && *priority == "" {
		fmt.Fprintln(os.Stderr, "task update: nothing to update (--status/--progress/--note/--assignee/--project/--version/--start/--end/--deps/--milestone/--pct/--priority)")
		return 2
	}
	if tf.name == "" {
		return failNoName("task update")
	}
	// v2.10 号段分家：裸号带工作区绑定的架名解析（绑定表/NIUMA_PROJECT
	// 的缺省；未绑定＝Niuma_Studio 架）。挂靠 --project 是另一个语义（挪任务）。
	patch := tasks.Patch{Status: *status, Progress: *progress, Note: *note, Assignee: *assignee,
		Project: *project, Version: *version, Start: *start, End: *end,
		Deps: *deps, Milestone: *milestone, Pct: *pct, Priority: *priority}
	wp := patch.Wire()
	frame := chat.Message{Type: chat.MsgTaskUpdate, TaskID: pos[0],
		Project: pickScope(""),
		Patch:   &wp}
	return runTaskWrite(tf, "task update", frame)
}

// runTaskConfirmDecline sends task_confirm / task_decline over WS.
func runTaskConfirmDecline(msgType string, args []string) int {
	cmd := "task " + map[string]string{chat.MsgTaskConfirm: "confirm", chat.MsgTaskDecline: "decline"}[msgType]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "target project (default: the workspace's bound room)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintf(os.Stderr, "%s: id required, e.g. %s t_01 --name Bot\n", cmd, cmd)
		return 2
	}
	if tf.name == "" {
		return failNoName(cmd)
	}
	frame := chat.Message{Type: msgType, TaskID: pos[0], Project: pickScope(*project)}
	return runTaskWrite(tf, cmd, frame)
}

// runTaskArbitrate sends task_arbitrate — the host's bypass verdict over
// a pending proposal (approve applies it at once, reject discards it; an
// assign proposal's rejection cancels the task). The host is never in
// Need, so confirm/decline only ever deny for them ("你不是该变更的需
// 确认人") — arbitrate is the host's protocol verb.
func runTaskArbitrate(args []string) int {
	cmd := "task arbitrate"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) < 2 || (pos[1] != "approve" && pos[1] != "reject") {
		fmt.Fprintf(os.Stderr, "%s: usage: %s t_01 approve|reject --name 房主\n", cmd, cmd)
		return 2
	}
	if tf.name == "" {
		return failNoName(cmd)
	}
	approve := pos[1] == "approve"
	frame := chat.Message{Type: chat.MsgTaskArbitrate, TaskID: pos[0], Approve: &approve, Project: pickScope("")}
	return runTaskWrite(tf, cmd, frame)
}

// dialTask connects for a one-shot task write and refuses to operate
// under a deduped "-2" name: ranks and proposals bind to the exact
// name, so a silent rename turns "update my own task" into a
// cross-name proposal — or the "you are not the confirmer" denial that
// looks like an engine bug (BUG-3's observed symptom). Before any
// dial: the same read-only seatHeld probe say/report use (t_34) — a
// plain dial against a live same-name seat already spawns the phantom
// twin (join broadcast + grace ghost) before the collision is even
// noticed, which is how a born member's first `task confirm` littered
// the roster with "name-2" ghosts. Probe first, refuse with zero side
// effects; a grace ghost does not count (the dial reclaims it — the
// back-to-back retry path stays). When the room still holds the seat,
// say bye, let the seat reach the presence grace and dial once more;
// if the name is still taken the seat is genuinely occupied (e.g. a
// live watchdog), so fail with an actionable message instead of
// mutating the ledger as someone else.
func dialTask(ctx context.Context, url, name, cmd string) (*websocket.Conn, chat.Member, error) {
	// The dispatcher-held seat refuses seatless one-shots (the
	// anti-top-seat contract) — unless this member holds their seat
	// credential, in which case the dial below supersedes cleanly. The
	// owner is the one held seat the probe must never block: while the
	// GUI seat is live the server routes a same-name dial to the
	// seatless owner write face (serveMirror — no "-2" twin, no
	// top-seat; the frames speak through the real seat), so refusing
	// here would only lock the host out of their own one-shot CLI (kb
	// write/append/restore, task …) until the app closed.
	if loadSeatToken(name) == "" {
		probe, herr := probeSeat(url, name)
		if herr != nil {
			return nil, chat.Member{}, herr
		}
		if probe.held && !probe.owner {
			return nil, chat.Member{}, fmt.Errorf("「%s」已有在座连接（受管成员的座位由调度器持有）——%s 未拨号、无办公室副作用；请直接在成员会话内回复处理，或由房主先移出该座位再执行", name, cmd)
		}
	}
	// A member's seat credential only redeems inside their own project
	// room — a lobby dial with it collides into a ghost "-2". Dial the
	// workspace's bound room ("" for the host/unbound keeps the legacy
	// lobby dial byte-identical).
	room := pickScope("")
	if room == chat.LobbyKey {
		room = ""
	}
	dial := func() (*websocket.Conn, chat.Member, error) {
		return dialTokenRoom(ctx, url, name, "", false, loadSeatToken(name), room)
	}
	conn, me, err := dial()
	if err != nil || me.Name == name {
		return conn, me, err
	}
	taskBye(ctx, conn)
	conn, me, err = dial()
	if err != nil || me.Name == name {
		return conn, me, err
	}
	_ = conn.CloseNow()
	return nil, me, fmt.Errorf("座位被 %s 的既有连接占用（本次被顶为 %s）；任务等级与协商按精确名字绑定，请稍后重试或用 members 检查残留连接", name, me.Name)
}

// runTaskWrite performs one WS write and waits for the room's answer:
// a private "denied" or the broadcast carrying our own task event
// (From == the assigned name). Exits non-zero on denial.
func runTaskWrite(tf *taskFlags, cmd string, frame chat.Message) int {
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
		if m.Type != chat.MsgTask || m.From != me.Name {
			continue // someone else's concurrent task event
		}
		if tf.json {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		}
		if m.Event == "denied" {
			if !tf.json {
				fmt.Fprintf(os.Stderr, "%s: 被拒：%s\n", cmd, m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
		if !tf.json {
			id := ""
			if m.Task != nil {
				id = m.Task.ID // the next step needs it: task show/update <id>
			}
			if m.Task != nil && m.Task.Pending != nil {
				var rest []string
				for _, n := range m.Task.Pending.Need {
					if !util.Contains(m.Task.Pending.Got, n) {
						rest = append(rest, n)
					}
				}
				fmt.Printf("[%s] 已提交，等待 %s 确认（%s）\n", me.Name, strings.Join(rest, "、"), id)
			} else if id != "" {
				fmt.Printf("[%s] 已生效（%s）\n", me.Name, id)
			} else {
				fmt.Printf("[%s] 已生效\n", me.Name)
			}
			fmt.Println(m.Text)
		}
		taskBye(ctx, conn)
		return 0
	}
}

// taskBye says goodbye and drains until the server closes. The server's
// Bye branch hands the seat to the presence grace during teardown, so
// by the time the drain ends the same-name seat is reclaimable and the
// very next command keeps the exact name (rank belongs to it); the
// sleep is margin for old servers without that ordering and for
// scheduling jitter.
func taskBye(ctx context.Context, conn *websocket.Conn) {
	_ = wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgBye})
	dctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		var m chat.Message
		if err := wsjson.Read(dctx, conn, &m); err != nil {
			break // closed: the server is finishing Leave
		}
	}
	time.Sleep(300 * time.Millisecond)
}

func taskStateLabel(t tasks.Task) string {
	if t.Pending != nil {
		return "[待确认]"
	}
	if p := tasks.PriorityLabel(t.Priority); p != "" {
		return "[" + tasks.StatusLabel(t.Status) + "·" + p + "]"
	}
	return "[" + tasks.StatusLabel(t.Status) + "]"
}

func taskOwnerLabel(t tasks.Task) string {
	if t.Assignee == "" {
		return "待认领"
	}
	return t.Assignee
}
