package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// The listen duplex: a persistent seat with a token (auto-reconnect,
// kick detection) streaming the room.

func runListen(args []string) int {
	fs := flag.NewFlagSet("listen", flag.ContinueOnError)
	c := parseCommon(fs)
	project := fs.String("project", "", "project key of the office to sit in (default: the workspace's bound project; empty = Niuma_Studio)")
	noHistory := fs.Bool("no-history", false, "skip history replay")
	reportFile := fs.String("report-file", "", "periodically report this file's content as task status")
	every := fs.Duration("every", 5*time.Minute, "report interval (min 5s)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if c.name == "" {
		return failNoName("listen")
	}

	url, err := c.resolveURL()
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return 1
	}
	// 落座跟成员绑定房走（say 同律）：值班座位属于本人办公室——大厅
	// 裸拨会按名收回别房的宽限幽灵，人坐在错误的房间里。
	room := dialRoom(*project)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// 值守不顶座（同 say 的 t_34 探测）：对在座同名直接拒绝，避免首拨
	// 先产出一个 "-2" 分身座位（加入广播 + 宽限幽灵）再失败。宽限幽灵
	// 不算占用——首拨会静默收回，正是断线重连的路径。
	if held, herr := seatHeldIn(url, c.name, room); herr != nil {
		fmt.Fprintln(os.Stderr, "listen:", herr)
		return 1
	} else if held {
		fmt.Fprintf(os.Stderr, "listen: 「%s」已有在座连接（受管成员的座位由调度器持有）——listen 未拨号、无办公室副作用。请先停同名值守（pkill 后 pgrep 确认清零），或由房主移出该座位。\n", c.name)
		return 1
	}
	dialOwn := func() (*websocket.Conn, chat.Member, error) {
		return dialTokenRoom(ctx, url, c.name, c.role, !*noHistory, "", room)
	}
	conn, me, err := dialOwn()
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return 1
	}
	// never stream under a deduped "-2" name (seat-M3): the live seat
	// belongs to a connection whose token does not match. Say bye, redial
	// once (a freed/grace seat resolves), and a still-renamed dial is a
	// held seat — refuse with the way out.
	if me.Name != c.name {
		taskBye(ctx, conn)
		_ = conn.CloseNow()
		if conn2, me2, err2 := dialOwn(); err2 == nil {
			if me2.Name == c.name {
				conn, me = conn2, me2
			} else {
				taskBye(ctx, conn2)
				_ = conn2.CloseNow()
			}
		}
		if me.Name != c.name {
			fmt.Fprintf(os.Stderr, "listen: 「%s」的座位被别的连接占用（本次被顶为 %s）——listen 拒绝以 -2 分身收流。请先停同名值守（pkill 后 pgrep 确认清零）；常驻值守请改用 guard（座位权 token 自动顶替旧座）。\n", c.name, me.Name)
			return 1
		}
	}
	defer conn.CloseNow()
	fmt.Fprintf(os.Stderr, "listening as %s", me.Name)
	if me.Role != "" {
		fmt.Fprintf(os.Stderr, " (%s)", me.Role)
	}
	fmt.Fprintln(os.Stderr, " (Ctrl+C to quit)")

	// wsjson allows one writer at a time; stdin, the periodic reporter
	// and the farewell bye all funnel through writeMsg.
	var wmu sync.Mutex
	writeMsg := func(m chat.Message) error {
		wmu.Lock()
		defer wmu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return wsjson.Write(wctx, conn, m)
	}

	// Full-duplex session: each stdin line is sent as a message, so
	// `listen` alone is a complete agent/human client (pipe lines in, or
	// type interactively). EOF on stdin ends the session.
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if err := writeMsg(chat.Message{Type: chat.MsgSay, Text: line}); err != nil {
				stop()
				return
			}
		}
		_ = writeMsg(chat.Message{Type: chat.MsgBye})
		stop() // stdin closed: end the session
	}()

	// Periodic task reporting: send the report file immediately after
	// joining, then re-send its current content on every tick.
	if *reportFile != "" {
		interval := *every
		if interval < 5*time.Second {
			interval = 5 * time.Second
		}
		report := func() {
			b, err := os.ReadFile(*reportFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "report: cannot read %s: %v\n", *reportFile, err)
				return
			}
			text := strings.TrimSpace(string(b))
			if text == "" {
				return
			}
			if err := writeMsg(chat.Message{Type: chat.MsgReport, Text: text}); err != nil {
				stop()
				return
			}
		}
		go func() {
			report()
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					report()
				}
			}
		}()
	}

	printStream(ctx, conn, c.json, me.Name)
	return 0
}
