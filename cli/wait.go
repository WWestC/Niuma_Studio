// The `wait` subcommand: a blocking long-poll for agents. Instead of
// running a full-duplex `listen` session (hard for tool-driven AI
// harnesses) or polling the room every few minutes, an agent runs
// `wait` and blocks: the process holds one quiet WebSocket connection,
// and the moment a message @-mentions the agent, everything said since
// its last response is printed to stdout and the process exits —
// waking the agent instantly.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// runWait implements the wait long-poll. It stays connected and silent
// until a message @-mentions us (or any chat line arrives, with --any),
// then prints everything said since our last response and exits —
// waking the agent instantly. There is no timeout by default: the
// process is the agent's seat in the room and holds it for as long as
// it runs; during the exit-and-reply gap the room's presence grace
// keeps the member visibly online. Messages sent while the agent was
// between two runs (offline) are not lost: history is replayed on
// connect and a small cursor file remembers the last delivered message,
// so a mention from the gap still triggers the next run.
func runWait(args []string) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	c := parseCommon(fs)
	project := fs.String("project", "", "project key of the office to sit in (default: the workspace's bound project; empty = Niuma_Studio)")
	timeout := fs.Duration("timeout", 0, "exit after this long when nobody calls (default 0 = stay until @-mentioned)")
	anyMsg := fs.Bool("any", false, "wake on any chat line, not just @mentions of me")
	lookback := fs.Duration("lookback", 5*time.Minute, "first run: also react to mentions from the last DUR")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if c.name == "" {
		return failNoName("wait")
	}
	url, err := c.resolveURL()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wait:", err)
		return 1
	}
	// 常驻座位跟成员绑定房走（say 同律）：wait 的连接就是成员在本房
	// 的座位——大厅拨号会把人钉在错误的房间里。
	room := dialRoom(*project)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	switch waitSession(ctx, url, c.name, c.role, c.json, *anyMsg, *lookback, *timeout, false, room) {
	case waitKicked:
		// the t_12 contract: a removed member's one-shot wait stops
		// with exit code 2, never reconnects (guard's loop reads the
		// same outcome; this case was missing and fell to default 0 —
		// t_49's rework).
		return 2
	case waitDisconnected:
		return 1
	default:
		return 0
	}
}

// waitOutcome classifies how one wait connection ended.
type waitOutcome int

const (
	waitWoke         waitOutcome = iota // @-mentioned: context printed to stdout
	waitKicked                          // removed from the room
	waitDisconnected                    // transport error without a kick (room restart, dead link)
	waitSuperseded                      // our own seat was taken over by a same-token dial (seat-M1)
	waitSeatHeld                        // the name's live seat belongs to another connection (seat-M3)
	waitTimeout                         // --timeout elapsed with nobody calling
	waitInterrupted                     // Ctrl+C
)

// waitSession runs ONE wait connection to its end and prints exactly
// what the wait command has always printed: the banner (unless quiet),
// the buffered context on stdout when the wake trigger arrives, and
// the kick notices. Extracted from runWait so `guard` can loop whole
// sessions — reconnect logic now lives in one Go implementation
// instead of every agent's shell wrapper (t_28).
func waitSession(ctx context.Context, url, name, role string, jsonOut, anyMsg bool, lookback, timeout time.Duration, quiet bool, room string) waitOutcome {
	out, _ := waitSessionSeat(ctx, url, name, role, jsonOut, anyMsg, lookback, timeout, quiet, "", room)
	return out
}

// waitSessionSeat is waitSession carrying a seat credential (seat-M1):
// the token is presented on hello, the welcome's token comes back —
// guard persists it for the next reconnect; one-shot wait ignores it.
// room rides hello.project ("" keeps the legacy lobby dial).
func waitSessionSeat(ctx context.Context, url, name, role string, jsonOut, anyMsg bool, lookback, timeout time.Duration, quiet bool, seatToken, room string) (waitOutcome, string) {
	conn, me, welcomeToken, err := dialSeat(ctx, url, name, role, true, seatToken, room)
	if err != nil {
		if !quiet {
			fmt.Fprintln(os.Stderr, "wait:", err)
		}
		return waitDisconnected, ""
	}
	if me.Name != name {
		// renamed to Name-2: the live seat is held by a connection whose
		// token does not match ours (a same-token dial never lands here —
		// the hub supersedes instead). Say bye, redial ONCE (a freed or
		// grace seat resolves there); a still-renamed dial is a held
		// seat (seat-M3): one-shot wait explains and exits, guard
		// refuses to stand up under a twin name.
		byeAndDrainWait(ctx, conn)
		_ = conn.CloseNow()
		if conn2, me2, _, err2 := dialSeat(ctx, url, name, role, true, seatToken, room); err2 == nil {
			if me2.Name == name {
				conn, me = conn2, me2
			} else {
				byeAndDrainWait(ctx, conn2)
				_ = conn2.CloseNow()
			}
		}
		if me.Name != name {
			if !quiet {
				fmt.Fprintf(os.Stderr, "wait: 「%s」的座位被别的连接占用（本次被顶为 %s）——多半是同名值守进程在跑。一次性 wait 不顶座；要上岗请先停同名值守（pkill 后 pgrep 确认清零）；值守进程请用新版 guard 重启，座位权 token 会自动顶替自己的旧座。\n", name, me.Name)
			}
			return waitSeatHeld, ""
		}
	}
	defer conn.CloseNow()

	// Keepalive: a healthy but idle connection carries no traffic for
	// hours, so ping periodically — a dead room then surfaces as a read
	// error instead of a silent hang. Pong frames are consumed by the
	// read loop below, which is always blocked in a Read.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()

	curPath, cur := loadCursor(me.Name)
	after := func(m chat.Message) bool { return afterCursor(m, cur) }
	if cur.TS == 0 {
		// first ever run: don't resurrect stale history, only the
		// recent window (the "someone called me while I was gone" case)
		startTS := time.Now().Add(-lookback).Unix()
		after = func(m chat.Message) bool { return afterCursor(m, cur) && m.TS >= startTS }
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "wait: 在线等待中，身份 %s", me.Name)
		if me.Role != "" {
			fmt.Fprintf(os.Stderr, "（%s）", me.Role)
		}
		if timeout > 0 {
			fmt.Fprintf(os.Stderr, "，最长 %s", timeout)
		}
		fmt.Fprintln(os.Stderr, "；有人 @你 即返回，在此之前一直在线（Ctrl+C 退出）")
	}

	// New say/report lines buffer up as context; stdout only ever sees
	// them when a trigger arrives, so "output" reliably means "called".
	// System notices (not kept in history) buffer too, so a removal
	// notice can still be surfaced when the stream ends.
	var pending []chat.Message
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			if flushKickNotice(pending, err, me.Name) {
				return waitKicked, ""
			}
			// our own newer dial took the seat over (same token): a
			// healthy event for guard — plain reconnect, never a kick
			if strings.Contains(fmt.Sprint(err), chat.CloseReasonSuperseded) {
				return waitSuperseded, ""
			}
			switch ctx.Err() {
			case context.DeadlineExceeded:
				if !quiet {
					fmt.Fprintf(os.Stderr, "wait: %s 内没有人 @你（stdout 为空；直接再次运行继续等待）\n", timeout)
				}
				return waitTimeout, ""
			case context.Canceled:
				if !quiet {
					fmt.Fprintln(os.Stderr, "wait: 已中断")
				}
				return waitInterrupted, ""
			default:
				if !quiet {
					fmt.Fprintln(os.Stderr, "wait: 连接断开（办公室可能重启了，稍后重试）:", err)
				}
				return waitDisconnected, ""
			}
		}
		switch m.Type {
		case chat.MsgSay, chat.MsgReport:
			if !after(m) {
				continue
			}
			pending = append(pending, m)
			if m.From == me.Name || !waitTrigger(m, me.Name, anyMsg) {
				continue
			}
			for _, pm := range pending {
				printWaitMsg(pm, jsonOut, me.Name)
			}
			saveCursor(curPath, waitCursor{TS: m.TS, From: m.From, Text: m.Text, Seq: m.Seq})
			wctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = wsjson.Write(wctx, conn, chat.Message{Type: chat.MsgBye})
			cancel()
			// Drain until the server finishes Leave: exiting the instant
			// the Bye is written lets the NEXT same-name run race the
			// seat into a "-2" rename — and a renamed agent starts from
			// a zero cursor, whose first-run lookback then resurrects
			// already-delivered history (the count>=3 e2e flake).
			dctx, dcancel := context.WithTimeout(context.Background(), time.Second)
			for {
				var drop chat.Message
				if err := wsjson.Read(dctx, conn, &drop); err != nil {
					break
				}
			}
			dcancel()
			return waitWoke, welcomeToken
		case chat.MsgSystem:
			pending = append(pending, m)
		}
	}
}

// byeAndDrainWait says bye and drains the close handshake.
func byeAndDrainWait(ctx context.Context, conn *websocket.Conn) {
	wctx, wcancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = wsjson.Write(wctx, conn, chat.Message{Type: chat.MsgBye})
	wcancel()
	dctx, dcancel := context.WithTimeout(context.Background(), time.Second)
	for {
		var drop chat.Message
		if err := wsjson.Read(dctx, conn, &drop); err != nil {
			break
		}
	}
	dcancel()
}

// flushKickNotice surfaces the operator's removal when the stream ends
// because we were kicked: the UI broadcasts a system notice ("已将 X
// 移出办公室") just before the hub drops the transport, and the
// websocket close error carries the reason as well. Being removed is
// the one condition that stops the agent's wait loop for good, so it
// must be distinguishable from an ordinary disconnect — and from
// removal notices about OTHER members: a ghost-cleanup broadcast plus
// a restart drop must not read as "I was kicked" (t_12). The notice
// counts only when it names exactly me, compared as a full string:
// a Contains match would also fire on name-prefix siblings
// (已将 小验-2 移出办公室 must not kick 小验).
func flushKickNotice(pending []chat.Message, err error, me string) bool {
	kicked := strings.Contains(fmt.Sprint(err), "removed from room")
	mine := fmt.Sprintf("已将 %s 移出办公室", me)
	var notices []string
	for _, m := range pending {
		if m.Type == chat.MsgSystem {
			notices = append(notices, m.Text)
			if m.Text == mine {
				kicked = true
			}
		}
	}
	if !kicked {
		return false
	}
	for _, n := range notices {
		fmt.Println(n)
	}
	fmt.Fprintln(os.Stderr, "wait: 你已被移出办公室——停止值守循环，不要再重连")
	return true
}

// waitTrigger reports whether m should wake the agent: it @-mentions
// me — via the server-resolved mentions roster, or a plain "@me" in the
// text for messages sent while we were offline — or anyMsg is set and
// it is a regular chat line.
func waitTrigger(m chat.Message, me string, anyMsg bool) bool {
	if anyMsg && m.Type == chat.MsgSay {
		return true
	}
	if slices.Contains(m.Mentions, me) {
		return true
	}
	return len(chat.ExtractMentions(m.Text, [][]rune{[]rune(me)})) > 0
}

func printWaitMsg(m chat.Message, asJSON bool, me string) {
	if asJSON {
		b, _ := json.Marshal(m)
		fmt.Println(string(b))
		return
	}
	line := human(m)
	if slices.Contains(m.Mentions, me) {
		line = "[@你] " + line
	}
	fmt.Println(line)
}

// waitCursor remembers the last message handed to the agent, so the
// next `wait` run resumes after it. The full message identity (not
// just the second-granularity ts) keeps same-second follow-ups from
// being skipped or replayed.
type waitCursor struct {
	TS   int64  `json:"ts"`
	From string `json:"from,omitempty"`
	Text string `json:"text,omitempty"`
	Seq  int64  `json:"seq,omitempty"` // hub order; orders same-second messages
}

// afterCursor reports whether m is newer than the cursor message.
// Seq decides when both sides have it; the ts/from/text identity is
// the legacy fallback for pre-Seq cursors — it cannot order
// same-second messages, which is how a delivered mention resurrected.
func afterCursor(m chat.Message, cur waitCursor) bool {
	if m.Seq != 0 && cur.Seq != 0 {
		return m.Seq > cur.Seq
	}
	if m.TS != cur.TS {
		return m.TS > cur.TS
	}
	return m.From != cur.From || m.Text != cur.Text
}

// loadCursor reads the per-agent cursor file; a missing or corrupt
// file simply means "first run".
func loadCursor(name string) (path string, cur waitCursor) {
	p, err := cursorFilePath(name)
	if err != nil {
		return "", cur
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	return p, cur
}

func saveCursor(path string, cur waitCursor) {
	if path == "" {
		return
	}
	if b, err := json.Marshal(cur); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

// cursorFilePath names the per-agent cursor file:
// ~/.niuma_wait_<sanitized name>.
func cursorFilePath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := b.String()
	if r := []rune(s); len(r) > 48 {
		s = string(r[:48])
	}
	if s == "" {
		s = "agent"
	}
	return filepath.Join(home, ".niuma_wait_"+s), nil
}
