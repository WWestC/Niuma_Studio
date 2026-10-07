// Package cli implements the agent-facing subcommands: say, listen,
// members, kb. Everything talks to a running niuma instance
// via its WebSocket/HTTP endpoints.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/recruit"
	"github.com/WWestC/Niuma_Studio/server"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Run executes a subcommand and returns the process exit code.
// args are the arguments after the program name, e.g. ["say", "hi"].
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma <say|report|listen|wait|ack|dispatch|mirror|kick|rank|members|task|kb|drive|plugin> [flags]")
		return 2
	}
	switch args[0] {
	case "say":
		return runSay(args[1:])
	case "report":
		return runReport(args[1:])
	case "listen":
		return runListen(args[1:])
	case "wait":
		return runWait(args[1:])
	case "mirror":
		return runMirror(args[1:])
	case "ack":
		return runAck(args[1:])
	case "agent":
		return runAgent(args[1:])
	case "kick":
		return runKick(args[1:])
	case "rank":
		return runRank(args[1:])
	case "offboard":
		return runOffboard(args[1:])
	case "dispatch":
		return runDispatch(args[1:])
	case "members":
		return runMembers(args[1:])
	case "task":
		return runTask(args[1:])
	case "plan":
		return runPlan(args[1:])
	case "meeting":
		return runMeeting(args[1:])
	case "req":
		return runReq(args[1:])
	case "autopilot":
		return runAutopilot(args[1:])
	case "recruit":
		return runRecruit(args[1:])
	case "recall":
		return runRecall(args[1:])
	case "kb":
		return runKB(args[1:])
	case "skill":
		return runSkill(args[1:])
	case "mcp":
		return runMCP(args[1:])
	case "assemble":
		return runAssemble(args[1:])
	case "drive":
		return recruit.RunDrive(args[1:])
	case "plugin":
		return runPlugin(args[1:])
	case "vcs":
		return runVCS(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `niuma — 牛马工作室（Niuma Studio）：牛马全是 AI 的像素工作室

Usage:
  niuma                     start the app (server + pixel UI)
  niuma say TEXT...         send one message, then exit
  niuma report TEXT...      send a task report, then exit
  niuma listen              stream all messages (Ctrl+C to quit)
  niuma wait                block until someone @mentions me,
                                      print what was said, then exit  niuma ack                receipt every @mention this turn owes
                                      (the structured 「收到」 — rides
                                      the member's own seat token)
  niuma mirror --text T     relay the owner's session input into
                                      the room as an origin:"mirror" say
                                      (owner credential required: --token,
                                      $NIUMA_OWNER_TOKEN, or the saved
                                      seat-token file — members have no
                                      such credential and are refused)
  niuma kick --name N       remove a member (—name is the TARGET;
                                      operator is --as, default the host)
  niuma rank --name N --set RANK
                                      set a member's rank 1-9 (same --name/
                                      --as convention as kick)
  niuma agent archive ...  archive an offboarded member's config
                                      (agent archive --name 小明 [--as HR])
  niuma agent save ...      upsert a member's config (agent save
                                      --name 小简 --prompt "..."
                                      [--role r] [--as 小鸟])
  niuma offboard --name N   run the departure pipeline: precheck
                                      → reassign+notice → kick → archive →
                                      ledger (--reassign-to X / --force /
                                      --dry-run)
  niuma members             list online members
  niuma task SUBCMD         task ledger: list | show ID | create |
                                      update ID | confirm ID | decline ID |
                                      arbitrate ID approve|reject
  niuma plan SUBCMD         排期提案: show [--project K] | submit
                                      --file F --project K --name 编排者 |
                                      accept p_01 [--project K] |
                                      reject p_01 [--project K]
  niuma req SUBCMD          需求台账: list [--project K] | create
                                      --project K --title T [--body B]
                                      --name 编排者（全智能模式选题的进账路）
  niuma autopilot complete  目标制补货收摊（编排者专用）: --project K
                                      [--note 一句话结论] --name 编排者
                                      ——判定本次目标达成/无法达成后关掉
                                      本房的自动补货与自动推进
  niuma meeting end|extend  会议钟（主持专用）: end --project K
                                      --name 主持 立即结束当前议程段
                                      （讨论段直进总结、总结段散会）|
                                      extend [N] --project K --name 主持
                                      延长当前议程段 N 分钟（默认 10）
  niuma kb TOPIC            read the blackboard: manual | notice |
                                      people | person NAME
  niuma vcs SUBCMD          版本管理（本地仓库直读）: status |
                                      branches | log [-ref R -n N] |
                                      lint [-n N] | check-msg FILE
                                      （commit-msg 钩子的执行体）
  niuma skill SUBCMD        技能库: list | show KEY | rm KEY |
                                      save --key K --name 名 [--body T |
                                      --body-file F] [--manuals a,b]
  niuma mcp SUBCMD          MCP 服务库: list | show KEY | rm KEY |
                                      save --key K --type http|sse --url U
                                      [--header 名=值]
  niuma assemble --project P --person N [--skills a,b] [--mcps x,y]
                                      [--op set|add|remove] [--target
                                      staffing|profile]  给岗/档案装配技能与 MCP
  niuma drive --brief P    launch a GUI-native session by injecting
                                      the brief (recruit path); --dry-run prints
                                      the sequence instead
  niuma plugin SUBCMD     插件管理: list | install <目录> [--force] |
                                      enable/disable <publisher.name> |
                                      remove <publisher.name>（本地操作，
                                      刷新工作室窗口生效）
  niuma recall --name N    pull an existing member back online
                                      (config → prompt → drive → seat confirm;
                                      an archived member is a rehire)

Common flags:
  --name NAME     operator display name — REQUIRED for every command
                  that takes a room seat (say/report/listen/wait/task
                  writes/plan submit/kb writes): a missing --name
                  refuses without dialing (the CLI never invents an
                  identity — auto-names used to seat as a hostname
                  stranger and leave a ghost in the roster)
  --role ROLE     identity, e.g. "数据检索工程师" (recommended for agents)
  --project KEY   room selector (say/report/listen/wait): the office the
                  dial seats in. Default: the workspace's bound project
                  (a member session sits in ITS OWN room — the seat
                  credential only redeems there); unbound keeps the
                  Niuma_Studio lobby. An explicit key reaches another
                  room deliberately.
  --port N        target port (default: auto-discover running instance)
  --json          machine-readable JSON-lines output
  --force-seat    (say/report) speak even when the same name holds a
                  live seat — you talk as name-2 (a short-lived twin
                  seat); default refuses with zero side effects
  --wait DUR      (say) keep listening for replies, e.g. 10s / 3m
  --report-file P --every DUR (listen) periodically report a status file,
                  e.g. --report-file tasks.txt --every 5m; first report
                  is sent immediately on join, then on every tick
  --timeout DUR   (wait) exit after this long when nobody calls
                  (default 0 = stay online until @-mentioned); --any
                  (wait) wake on any chat line instead of only @mentions

Task ledger (--name is the operator identity for writes):
  task list [--assignee NAME] [--status todo|doing|done|cancelled] [--json]
  task show t_01 [--json]
  task brief t_01 [--project KEY]     # one-page pickup context: requirement
                                      # body, parents, deps, minutes, branch
  task create --name Bot --title "重建检索索引" [--desc ...] [--assignee NAME]
  task update t_01 --name Bot [--status doing] [--progress 60%] [--note ...]
                [--assignee NAME]
  task confirm t_01 --name Bot     # accept a pending change addressed to me
  task decline t_01 --name Bot     # reject it
  task arbitrate t_01 approve --name 房主   # host bypass: apply now (reject = discard)

Agent recipes:
  niuma say --name Bot --role 检索 "task done" --wait 10s
  niuma listen --json            # JSONL event stream
  niuma listen --name Worker --role 构建工程师       --report-file status.txt --every 5m  # periodic task reporting
  niuma wait --name Bot --role 检索 --json   # resident long-poll:
                  stays online until @Bot, then prints and exits — re-run it
                  after replying (the room's presence grace hides the gap)
  niuma kb manual                # room manual (how to behave here)
  niuma kb person Worker         # someone's identity / prompt / last report
  niuma task list                # who is doing what right now
`)
}

type common struct {
	name string
	role string
	port int
	json bool
}

func parseCommon(fs *flag.FlagSet) *common {
	c := &common{}
	fs.StringVar(&c.name, "name", "", "display name")
	fs.StringVar(&c.role, "role", "", "identity, e.g. 数据检索工程师")
	fs.IntVar(&c.port, "port", 0, "server port (default: auto-discover)")
	fs.BoolVar(&c.json, "json", false, "JSON-lines output")
	return c
}

func (c *common) resolveURL() (string, error) {
	port := c.port
	if port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			return "", err
		}
		port = p
	}
	return fmt.Sprintf("ws://127.0.0.1:%d/ws", port), nil
}

// dialToken is the bare/legacy wrapper: no credential, no room (the
// lobby dial keeps its pre-v2 shape byte-identical).
func dialToken(ctx context.Context, url, name, role string, replay bool, token string) (*websocket.Conn, chat.Member, error) {
	return dialTokenRoom(ctx, url, name, role, replay, token, "")
}

// dialTokenRoom is dialToken naming the room (v2.9): a non-empty
// project rides hello.project so the connection — seat, guest desk,
// says and kb writes — belongs to that project's office; empty keeps
// the legacy lobby dial byte-identical. Every seat-taking CLI face
// resolves its room through dialRoom (the workspace binding) and
// presents the member's persisted seat credential alongside it — the
// two only redeem together, in the member's own office.
func dialTokenRoom(ctx context.Context, url, name, role string, replay bool, token, project string) (*websocket.Conn, chat.Member, error) {
	return dialHello(ctx, url, name, role, replay, token, project, false)
}

// dialHello is the one dial core every wrapper funnels into. force is
// the explicit dedup opt-in (the seat door refuses an unforced dial
// whose name a live seat holds under a different credential); a
// system-frame handshake reply is a door refusal (seat held, project
// unavailable) — surface its words instead of a raw struct dump so
// the CLI error reads like the server meant it. An unforced
// credential-less dial into a room the name is NOT credentialed for
// refuses here first（错房守卫——bare dials reclaim grace ghosts by
// name, the 串房 the per-project keys exist to end）.
func dialHello(ctx context.Context, url, name, role string, replay bool, token, project string, force bool) (*websocket.Conn, chat.Member, error) {
	if !force && token == "" {
		if err := wrongRoomDial(name, project); err != nil {
			return nil, chat.Member{}, err
		}
	}
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, chat.Member{}, err
	}
	hello := chat.Message{Type: chat.MsgHello, Name: name, Role: role, Replay: &replay, Project: project}
	if token != "" {
		hello.Token = token
	}
	if force {
		hello.Force = true
	}
	authHello(&hello)
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		_ = conn.CloseNow()
		return nil, chat.Member{}, err
	}
	var welcome chat.Message
	if err := wsjson.Read(ctx, conn, &welcome); err != nil {
		_ = conn.CloseNow()
		return nil, chat.Member{}, err
	}
	if welcome.Type != chat.MsgWelcome || welcome.You == nil {
		_ = conn.CloseNow()
		if welcome.Type == chat.MsgSystem && welcome.Text != "" {
			return nil, chat.Member{}, fmt.Errorf("%s", welcome.Text)
		}
		return nil, chat.Member{}, fmt.Errorf("unexpected handshake reply: %+v", welcome)
	}
	return conn, *welcome.You, nil
}
