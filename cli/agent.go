package cli

// agent — 档案管理命令族（v0.8 M1 / t_11）。当前子命令：archive、save。
//
//	$EXE agent archive --name 小明 [--as 房主名] [--port N] [--json]
//	$EXE agent save --name 小简 --prompt "..." [--role r] [--manual m]
//	        [--prompt-file f] [--as 小鸟] [--project book] [--port N] [--json]
//
// archive 的参数面与 kick 一致：--name 是目标，操作者身份是 --as（缺省自动探测
// 房主名，本机单房主模型）；显式 --as 走服务端等级判定（与 kick 同档：
// 房主任意、Lv.8+ 仅严格低于自己者）。幂等：重复归档成功。复职 = 同名
// recruit（agent_save 清 archived），无独立 unarchive 命令。
// save 是成员面的建档/改档动词（小鸟-2 事故的缺口：HR 想给在编成员更新
// 档案提示词，没有动词可用，只能手写裸 WS 程序顶座）。upsert 空字段保留
// 原值（agents.Store.Upsert 语义），只传 --prompt 不会抹掉岗位/手册；拨号
// 走操作者自己的座位凭据（同 token 顶替，无分身），服务端座位门挡裸拨。
// 退出码 0 成功（含幂等重归档）/ 1 被拒（权限、档案不存在、办公室不在）/
// 2 用法错误。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func runAgent(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma agent <archive|save> [flags]")
		return 2
	}
	switch args[0] {
	case "archive":
		return runAgentArchive(args[1:])
	case "save":
		return runAgentSave(args[1:])
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "agent: unknown subcommand %q (archive|save)\n", args[0])
		return 2
	}
}

func runAgentArchive(args []string) int {
	fs := flag.NewFlagSet("agent archive", flag.ContinueOnError)
	target := fs.String("name", "", "member whose config to archive (required)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*target) == "" {
		fmt.Fprintln(os.Stderr, "agent archive: --name 目标成员必填（例：agent archive --name 小明 [--as HR]）")
		return 2
	}
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent archive:", err)
			return 1
		}
		*port = p
	}
	actor := *as
	if actor == "" {
		host, err := detectLocalName(*port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent archive:", err)
			return 1
		}
		actor = host
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", *port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// 管理面拨号跟操作者绑定房走（kick 同律）。
	conn, _, err := opDial(ctx, url, actor, dialRoom(""))
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent archive:", err)
		return 1
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chat.Message{Type: server.MsgAgentArchive, Name: *target}); err != nil {
		fmt.Fprintln(os.Stderr, "agent archive:", err)
		return 1
	}
	// The receipt (denial or success) is always a private system frame —
	// one success check serves the seated and the seatless path alike.
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "agent archive: 办公室无回执（%v）；服务端为 v0.8-M1 之前的版本时没有该帧\n", err)
			return 1
		}
		if m.Type != chat.MsgSystem {
			continue
		}
		if strings.HasPrefix(m.Text, "agent_archive 被拒") {
			if *asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Fprintf(os.Stderr, "agent archive: %s\n", m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
		if strings.HasPrefix(m.Text, "已归档") {
			if *asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Println(m.Text)
			}
			taskBye(ctx, conn)
			return 0
		}
	}
}

// agentOpDial is opDial naming the room: the actor dials their own
// office (the seat their credential supersedes lives there), so the
// save's broadcast lands in the room that knows them — never a fresh
// seat under their name in the wrong room.
func agentOpDial(ctx context.Context, url, name, project string) (*websocket.Conn, chat.Member, error) {
	dial := func() (*websocket.Conn, chat.Member, error) {
		return dialTokenRoom(ctx, url, name, "", false, loadSeatToken(name), project)
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
	return nil, me, fmt.Errorf("座位被 %s 的既有连接占用（本次被顶为 %s）——档案写入按精确名字留痕，请稍后重试", name, me.Name)
}

// capOpDialRoom is opDial naming the room: the same refuse-the-twin
// discipline, but the dial (and the seat token that takes the seat
// back) belongs to the operator's project office — a member's saved
// credential only redeems inside its own room.
func capOpDialRoom(ctx context.Context, url, name, project string) (*websocket.Conn, chat.Member, error) {
	dial := func() (*websocket.Conn, chat.Member, error) {
		return dialTokenRoom(ctx, url, name, "", false, loadSeatToken(name), project)
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
	return nil, me, fmt.Errorf("座位被 %s 的既有连接占用（本次被顶为 %s）；管理权限按精确名字的等级判定，请先停同名值守或稍后重试", name, me.Name)
}

// runAgentSave upserts a member's config (agent_save) from the CLI —
// the verb the 小鸟-2 incident was missing: an HR member updating a
// profile had no command and hand-rolled a raw WS dial (a phantom twin
// while the dispatcher held the seat). --name is the target, --as the
// acting identity (default: auto-detect the local host — a member
// session passes its own name). The upsert keeps every field not
// passed (empty = unchanged). --project names the office to dial
// (default: the workspace's bound project; '-' the lobby).
func runAgentSave(args []string) int {
	fs := flag.NewFlagSet("agent save", flag.ContinueOnError)
	target := fs.String("name", "", "member whose config to upsert (required)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	role := fs.String("role", "", "role line (empty = unchanged)")
	manual := fs.String("manual", "", "manual key, e.g. roles/writer (empty = unchanged)")
	prompt := fs.String("prompt", "", "onboarding/task prompt (empty = unchanged)")
	promptFile := fs.String("prompt-file", "", "read the prompt from this file")
	project := fs.String("project", "", "office to dial (default: the workspace's bound project; '-' = the lobby)")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*target) == "" {
		fmt.Fprintln(os.Stderr, "agent save: --name 目标成员必填（例：agent save --name 小简 --prompt \"...\" --as 小鸟）")
		return 2
	}
	promptText := *prompt
	if *promptFile != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent save:", err)
			return 1
		}
		promptText = strings.TrimSpace(string(b))
	}
	if *role == "" && *manual == "" && strings.TrimSpace(promptText) == "" {
		fmt.Fprintln(os.Stderr, "agent save: 至少传 --role/--manual/--prompt 之一（空跑一次改档没有意义）")
		return 2
	}
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent save:", err)
			return 1
		}
		*port = p
	}
	actor := *as
	if actor == "" {
		host, err := detectLocalName(*port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent save:", err)
			return 1
		}
		actor = host
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", *port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	proj := pickScope(*project)
	conn, _, err := agentOpDial(ctx, url, actor, proj)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent save:", err)
		return 1
	}
	defer conn.CloseNow()
	cfg := chat.AgentConfig{Name: *target, Role: *role, Manual: *manual, Prompt: strings.TrimSpace(promptText)}
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgAgentSave, Agent: &cfg}); err != nil {
		fmt.Fprintln(os.Stderr, "agent save:", err)
		return 1
	}
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "agent save: 办公室无回执（%v）\n", err)
			return 1
		}
		if m.Type != chat.MsgAgent || m.Agent == nil || m.Agent.Name != cfg.Name {
			continue
		}
		if *asJSON {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		} else {
			manualShown := m.Agent.Manual
			if manualShown == "" {
				manualShown = "—"
			}
			fmt.Printf("已更新档案 %s（岗位 %s，手册 %s；由 %s 落笔）\n", m.Agent.Name, m.Agent.Role, manualShown, actor)
		}
		taskBye(ctx, conn)
		return 0
	}
}
