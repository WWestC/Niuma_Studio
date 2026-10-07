package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/plan"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// runPlan implements the plan-proposal CLI (v2 P4-b): show reads over
// HTTP, submit/accept/reject write over WS (the task ledger's split).
// submit dials INTO the project's room (hello.project) as the
// orchestrator's own seat; accept/reject dial as the host (the
// seatless mirror channel when the GUI seat holds the name).
func runPlan(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma plan <show|submit|accept|reject> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "show":
		return runPlanShow(rest)
	case "submit":
		return runPlanSubmit(rest)
	case "accept":
		return runPlanAcceptReject(chat.MsgPlanAccept, rest)
	case "reject":
		return runPlanAcceptReject(chat.MsgPlanReject, rest)
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "plan: unknown subcommand %q (show|submit|accept|reject)\n", sub)
		return 2
	}
}

// runPlanShow prints a project's pending plan (GET /p/{key}/plan).
func runPlanShow(args []string) int {
	fs := flag.NewFlagSet("plan show", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "default", "project key (default also sees unattached tasks)")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	port, ok := tf.resolvePort("plan show")
	if !ok {
		return 1
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/%s/plan", port, *project))
	if err != nil {
		fmt.Fprintln(os.Stderr, "plan show:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "plan show: 项目 %s 不存在或提案系统未启用\n", *project)
		return 1
	}
	var body struct {
		Project string     `json:"project"`
		Plan    *plan.Plan `json:"plan"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		fmt.Fprintln(os.Stderr, "plan show:", err)
		return 1
	}
	if body.Plan == nil {
		fmt.Printf("项目 %s 没有待审提案\n", body.Project)
		return 0
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(body.Plan)
		return 0
	}
	printPlanDetail(*body.Plan)
	return 0
}

func printPlanDetail(p plan.Plan) {
	fmt.Printf("提案   %s %s\n", p.ID, p.Title)
	who := p.SubmittedBy
	if who == "" {
		who = "（未知）"
	}
	line := "提交   " + who
	if p.SubmittedTS > 0 {
		line += " · " + time.Unix(p.SubmittedTS, 0).Format("01-02 15:04")
	}
	fmt.Println(line)
	var tags []string
	if p.Req != "" {
		tags = append(tags, "需求 "+p.Req)
	}
	if p.Version != "" {
		tags = append(tags, "版本 "+p.Version)
	}
	if p.ProjectKey != "" {
		tags = append(tags, "项目 "+p.ProjectKey)
	}
	if len(tags) > 0 {
		fmt.Printf("挂靠   %s\n", strings.Join(tags, " · "))
	}
	if p.Need != "" {
		fmt.Println("需求描述:")
		for _, ln := range strings.Split(p.Need, "\n") {
			fmt.Println("  " + ln)
		}
	}
	fmt.Printf("任务   %d 条（deps＝提案内序号）\n", len(p.Tasks))
	for i, t := range p.Tasks {
		marker := " "
		if t.Milestone {
			marker = "◆"
		}
		owner := "（任务池）"
		if t.Assignee != "" {
			owner = t.Assignee
		}
		var sched []string
		if t.StartTS > 0 {
			sched = append(sched, time.Unix(t.StartTS, 0).Format("01-02 15:04"))
		}
		if t.EndTS > 0 {
			sched = append(sched, "~ "+time.Unix(t.EndTS, 0).Format("01-02 15:04"))
		}
		if len(t.Deps) > 0 {
			var ds []string
			for _, d := range t.Deps {
				ds = append(ds, fmt.Sprintf("#%d", d))
			}
			sched = append(sched, "依赖 "+strings.Join(ds, ","))
		}
		fmt.Printf("  %d %s %s → %s", i+1, marker, t.Title, owner)
		if len(sched) > 0 {
			fmt.Printf("（%s）", strings.Join(sched, " "))
		}
		fmt.Println()
	}
}

// readPlanFile loads a proposal JSON body (the shape the orchestrator
// prompt documents: {"req","title","need","version","tasks":[…]}).
func readPlanFile(cmd, path string) (*plan.Plan, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		return nil, false
	}
	var p plan.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(os.Stderr, "%s: 提案 JSON 不合法: %v\n", cmd, err)
		return nil, false
	}
	return &p, true
}

// runPlanSubmit sends plan_submit over WS, dialing INTO the project's
// room (hello.project) under the orchestrator's own name.
func runPlanSubmit(args []string) int {
	fs := flag.NewFlagSet("plan submit", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key the plan targets (required)")
	file := fs.String("file", "", "proposal JSON file (required)")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(os.Stderr, "plan submit: --file required (提案 JSON 草稿)")
		return 2
	}
	p, ok := readPlanFile("plan submit", *file)
	if !ok {
		return 2
	}
	if *project != "" {
		p.ProjectKey = *project
	}
	if p.ProjectKey == "" {
		p.ProjectKey = "default"
	}
	if tf.name == "" {
		return failNoName("plan submit")
	}
	frame := chat.Message{Type: chat.MsgPlanSubmit, Plan: p.Wire()}
	return runPlanWrite(tf, "plan submit", frame, *project, tf.name)
}

// runPlanAcceptReject sends plan_accept / plan_reject over WS, dialing
// as the host (the confirmation gate is host-only server-side).
func runPlanAcceptReject(msgType string, args []string) int {
	cmd := "plan " + map[string]string{chat.MsgPlanAccept: "accept", chat.MsgPlanReject: "reject"}[msgType]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key (default: the dialed room = lobby)")
	revisedFile := fs.String("revised-file", "", "entry-edited full plan JSON (accept only)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintf(os.Stderr, "%s: plan id required, e.g. %s p_01\n", cmd, cmd)
		return 2
	}
	var revised *plan.Plan
	if msgType == chat.MsgPlanAccept && *revisedFile != "" {
		var rok bool
		if revised, rok = readPlanFile(cmd, *revisedFile); !rok {
			return 2
		}
	}
	// the host's own name: with the GUI seat live this dials into the
	// seatless mirror channel; otherwise it seats as the host outright.
	// Never guessed from the hostname (the 「3StripFishde」 ghost) — the
	// same /kb/people local:true probe kick/rank use; a missing host is
	// a loud error, not an invented identity.
	name := tf.name
	if name == "" {
		port, ok := tf.resolvePort(cmd)
		if !ok {
			return 1
		}
		host, err := detectLocalName(port)
		if err != nil {
			fmt.Fprintln(os.Stderr, cmd+":", err)
			return 1
		}
		name = host
	}
	frame := chat.Message{Type: msgType, PlanID: pos[0], Project: *project, Revised: revised.Wire()}
	return runPlanWrite(tf, cmd, frame, *project, name)
}

// runPlanWrite performs one plan WS write and waits for the answer:
// the private "denied" or the plan frame the room broadcast in our
// name (the cross-room receipt covers a lobby-dialed host). Exits
// non-zero on denial.
func runPlanWrite(tf *taskFlags, cmd string, frame chat.Message, project, name string) int {
	port, ok := tf.resolvePort(cmd)
	if !ok {
		return 1
	}
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, me, err := planDial(ctx, url, name, project, cmd)
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
		if m.Type != chat.MsgPlan {
			continue // the room's concurrent traffic
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
			if m.Plan != nil {
				id = m.Plan.ID
			}
			fmt.Printf("[%s] %s（%s）\n", me.Name, m.Text, id)
		}
		taskBye(ctx, conn)
		return 0
	}
}

// planDial is dialTask with the hello.project room selector: a submit
// dials into the plan's project; accept/reject default to the lobby
// ("" keeps the legacy dial) and carry the project per-frame. The
// lobby key dials the legacy way (an explicit "default" project routes
// nowhere anyway — and the seatless mirror welcome echoes no project).
func planDial(ctx context.Context, url, name, project, cmd string) (*websocket.Conn, chat.Member, error) {
	if project == chat.LobbyKey {
		project = ""
	}
	dialOnce := func() (*websocket.Conn, chat.Member, error) {
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			return nil, chat.Member{}, err
		}
		hello := chat.Message{Type: chat.MsgHello, Name: name, Replay: boolPtr(false), Project: project}
		if tok := loadSeatToken(name); tok != "" {
			hello.Token = tok // take our dispatcher-held seat back (no -2 twin)
		}
		authHello(&hello)
		if err := wsjson.Write(ctx, conn, hello); err != nil {
			_ = conn.CloseNow()
			return nil, chat.Member{}, err
		}
		var w chat.Message
		if err := wsjson.Read(ctx, conn, &w); err != nil {
			_ = conn.CloseNow()
			return nil, chat.Member{}, err
		}
		if w.Type == chat.MsgSystem {
			// an unusable project (or another refusal) answers before
			// any seat logic — surface the reason, close the transport.
			_ = conn.CloseNow()
			return nil, chat.Member{}, fmt.Errorf("%s", w.Text)
		}
		if w.Type != chat.MsgWelcome {
			_ = conn.CloseNow()
			return nil, chat.Member{}, fmt.Errorf("unexpected handshake reply: %+v", w)
		}
		if project != "" && w.Project != project {
			_ = conn.CloseNow()
			return nil, chat.Member{}, fmt.Errorf("办公室路由回显不符（want %s got %q）", project, w.Project)
		}
		var me chat.Member
		if w.You != nil {
			me = *w.You
		}
		return conn, me, nil
	}
	conn, me, err := dialOnce()
	if err != nil || project != "" || me.Name == name {
		return conn, me, err
	}
	// the mirror channel seats nothing; a seated "-2" dedup on a
	// project dial keeps the task-write refusal rule (identity binds
	// to the exact name) — say bye, let the grace clear, dial once more.
	taskBye(ctx, conn)
	conn, me, err = dialOnce()
	if err != nil || me.Name == name {
		return conn, me, err
	}
	_ = conn.CloseNow()
	return nil, me, fmt.Errorf("座位被 %s 的既有连接占用（本次被顶为 %s）；请稍后重试或用 members 检查残留连接", name, me.Name)
}

func boolPtr(b bool) *bool { return &b }
