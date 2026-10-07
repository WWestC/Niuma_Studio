package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// The say/report senders (范式重构 S8): one shared runSend — connect,
// deliver one line, drain the echo, leave. seatHeld is the supersede
// probe the mirror path shares.

func runSay(args []string) int { return runSend(args, chat.MsgSay) }

// runReport sends a one-shot task report (same flags as say).
func runReport(args []string) int { return runSend(args, chat.MsgReport) }

// runSend implements say and report: one-shot send with flags allowed
// after the text (agents tend to write: say hello --wait 10s).
func runSend(args []string, msgType string) int {
	var (
		textParts []string
		name      string
		role      string
		port      int
		project   string
		asJSON    bool
		wait      time.Duration
		forceSeat bool
	)
	need := func(flag string, i int) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", flag)
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			v, err := need("--name", i)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say:", err)
				return 2
			}
			name, i = v, i+1
		case "--role":
			v, err := need("--role", i)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say:", err)
				return 2
			}
			role, i = v, i+1
		case "--project":
			v, err := need("--project", i)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say:", err)
				return 2
			}
			project, i = v, i+1
		case "--port":
			v, err := need("--port", i)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say:", err)
				return 2
			}
			p, err := strconv.Atoi(v)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say: bad --port:", v)
				return 2
			}
			port, i = p, i+1
		case "--wait":
			v, err := need("--wait", i)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say:", err)
				return 2
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				fmt.Fprintln(os.Stderr, "say: bad --wait (use e.g. 10s / 2m):", v)
				return 2
			}
			wait, i = d, i+1
		case "--json":
			asJSON = true
		case "--force-seat":
			forceSeat = true
		case "-h", "--help":
			printUsage(os.Stdout)
			return 0
		default:
			if strings.HasPrefix(args[i], "-") && args[i] != "-" {
				fmt.Fprintf(os.Stderr, "say: unknown flag %s\n", args[i])
				return 2
			}
			textParts = append(textParts, args[i])
		}
	}

	cmd := msgType
	text := strings.Join(textParts, " ")
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, cmd+": empty message")
		return 2
	}
	if name == "" {
		return failNoName(cmd)
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	if port == 0 {
		u, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "say:", err)
			return 1
		}
		url = fmt.Sprintf("ws://127.0.0.1:%d/ws", u)
	}
	// 落座拨号跟成员绑定房走（小苗-2 根治第一刀）：座位凭据只在
	// 本人房间 redeem——大厅拨号带着项目房的 token 要么撞宽限幽灵
	// 成 -2 分身、要么被座位门拒绝。显式 --project 可跨房发言（编排
	// 者向大厅汇报的正当通路）；未绑定（房主在任何目录）保持大厅
	// 旧拨号字节不变。
	room := dialRoom(project)
	// 值守不顶座 (v0.10 t_34, plan b): probe for a live same-name seat
	// BEFORE dialing — a plain dial would already spawn the "name-2"
	// phantom seat (join broadcast + grace ghost) even if we refuse to
	// speak afterwards. Default: refuse with zero side effects and the
	// two proper paths; --force-seat dials anyway and speaks as the
	// deduped name, eyes open.
	if !forceSeat {
		held, herr := seatHeldIn(url, name, room)
		if herr != nil {
			fmt.Fprintln(os.Stderr, cmd+":", herr)
			return 1
		}
		if held {
			fmt.Fprintf(os.Stderr, "%s: 检测到「%s」已有在线连接（多半是你的值守进程），本次未拨号、无任何办公室副作用。\n  · 要发言：在值守进程里处理，或先停值守（pkill 后 pgrep 确认清零）再执行；\n  · 确要临时发言：加 --force-seat（将以「%s-2」分身身份发言，退出后 10 分钟宽限自动消失）。\n", cmd, name, name)
			return 1
		}
	}

	ctx := context.Background()
	// --force-seat rides hello.force so the server's seat door lets the
	// documented twin through (an unforced mismatched dial is refused
	// at the door instead of spawning the phantom).
	dialSeat := func() (*websocket.Conn, chat.Member, error) {
		if forceSeat {
			return dialHello(ctx, url, name, role, true, loadSeatToken(name), room, true)
		}
		return dialTokenRoom(ctx, url, name, role, true, loadSeatToken(name), room)
	}
	conn, me, err := dialSeat()
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 1
	}
	// Refuse to speak under a deduped "-2" name: the room held our
	// seat — typically our own live guard. A one-shot say that talks
	// as Name-2 spawns a phantom seat that keeps re-"joining" until
	// its grace expires (tonight's 后端-2 loop). Hand the seat to the
	// presence grace and dial once more; a still-taken name means the
	// seat is genuinely occupied — fail loudly instead of speaking as
	// someone else.
	if me.Name != name {
		taskBye(ctx, conn)
		conn, me, err = dialSeat()
		if err != nil {
			fmt.Fprintln(os.Stderr, cmd+":", err)
			return 1
		}
		if me.Name != name {
			if !forceSeat {
				_ = conn.CloseNow()
				fmt.Fprintf(os.Stderr, "%s: 座位被 %s 的既有连接占用（本次被顶为 %s）——发言请先停同名值守、或改用值守进程自带的发言通道；已拒绝以 %s 身份发言\n", cmd, name, me.Name, me.Name)
				return 1
			}
			fmt.Fprintf(os.Stderr, "%s: --force-seat：以「%s」分身身份发言（退出后 10 分钟宽限自动消失）\n", cmd, me.Name)
		}
	}
	defer conn.CloseNow()

	if err := wsjson.Write(ctx, conn, chat.Message{Type: msgType, Text: text}); err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 1
	}
	if msgType == chat.MsgReport {
		fmt.Printf("[%s][汇报] %s\n", me.Name, text)
	} else {
		fmt.Printf("[%s] %s\n", me.Name, text)
	}
	if wait <= 0 {
		_ = wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgBye})
		return 0
	}

	fmt.Fprintf(os.Stderr, "listening for replies for %s ...\n", wait)
	dctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	printStream(dctx, conn, asJSON, me.Name)
	_ = wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgBye})
	return 0
}

// seatProbe is one /kb/people read answering both questions a one-shot
// dial asks about a name: whether a LIVE connection holds its seat (the
// anti-twin probe) and whether the name is the room's owner (local:true
// — the human behind the GUI window). A presence-grace ghost does NOT
// count as held: a same-name dial silently reclaims it (no "-2" twin),
// so the grace window stays the natural path. /kb/people carries the
// distinction (grace flag). project scopes the probe to the room the
// dial will actually ride ("" = the lobby).
type seatProbe struct {
	held  bool
	owner bool
}

func probeSeatIn(wsURL, name, project string) (seatProbe, error) {
	httpURL := strings.Replace(wsURL, "ws://", "http://", 1)
	httpURL = strings.TrimSuffix(httpURL, "/ws") + withQuery("/kb/people", "project", project)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(httpURL)
	if err != nil {
		return seatProbe{}, fmt.Errorf("探测在线成员失败（%v）——办公室不在或网络抖动", err)
	}
	defer resp.Body.Close()
	var people []kb.PersonSummary
	if err := json.NewDecoder(resp.Body).Decode(&people); err != nil {
		return seatProbe{}, fmt.Errorf("解析 /kb/people 失败: %v", err)
	}
	for _, p := range people {
		if p.Name == name {
			return seatProbe{held: p.Online && !p.Grace, owner: p.Local}, nil
		}
	}
	return seatProbe{}, nil
}

// probeSeat is the lobby-scoped probe (legacy call sites).
func probeSeat(wsURL, name string) (seatProbe, error) {
	return probeSeatIn(wsURL, name, "")
}

// seatHeldIn is seatHeld scoped to the room the dial will ride.
func seatHeldIn(wsURL, name, project string) (bool, error) {
	p, err := probeSeatIn(wsURL, name, project)
	return p.held, err
}

// seatHeld reports whether a LIVE connection holds name's seat — the
// read-only probe behind say/report's default no-bump behavior (v0.10
// t_34). A presence-grace ghost does NOT count: a same-name dial
// silently reclaims it (no "-2" twin), so the grace window stays the
// natural path. /kb/people carries the distinction (grace flag).
func seatHeld(wsURL, name string) (bool, error) {
	p, err := probeSeat(wsURL, name)
	return p.held, err
}
