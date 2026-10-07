package cli

// autopilot.go — 目标制补货的成员侧动词：niuma autopilot complete。
// 选题注入（目标版）教的收摊路：编排者判定本次目标已达成（或确认
// 无法达成）后，从本房拨号上报——服务端关掉该项目的自动补货与自动
// 推进并在房内留一条系统行；本动词的回执是 PRIVATE 的 "autopilot"
// 帧（req create 的形状）。房主不走这里（设置卡·智能才是房主的手）。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket/wsjson"
)

// runAutopilot implements the autopilot CLI（目标制补货）: complete 是
// 编排者的收摊动词。读面（开关状态/目标）走 GET /p/{key}/autopilot
// （房主的设置卡真源），成员侧只需要写。
func runAutopilot(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma autopilot <complete> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "complete":
		return runAutopilotComplete(rest)
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "autopilot: unknown subcommand %q (complete)\n", sub)
		return 2
	}
}

// runAutopilotComplete sends autopilot_done over WS, dialing into the
// project's room under the orchestrator's own name (the req-create
// shape). --note 是一句话结论（目标怎么达成的/为何无法达成），会随
// 房内系统行播报。
func runAutopilotComplete(args []string) int {
	fs := flag.NewFlagSet("autopilot complete", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key to wrap up (required)")
	note := fs.String("note", "", "一句话结论（达成路径或无法达成的原因）")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	if *project == "" {
		fmt.Fprintln(os.Stderr, "autopilot complete: --project required")
		return 2
	}
	if tf.name == "" {
		return failNoName("autopilot complete")
	}
	port, ok := tf.resolvePort("autopilot complete")
	if !ok {
		return 1
	}
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, me, err := planDial(ctx, url, tf.name, *project, "autopilot complete")
	if err != nil {
		fmt.Fprintln(os.Stderr, "autopilot complete:", err)
		return 1
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgAutopilotDone, Text: *note}); err != nil {
		fmt.Fprintln(os.Stderr, "autopilot complete:", err)
		return 1
	}
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "autopilot complete: no answer from the room: %v\n", err)
			return 1
		}
		if m.Type != chat.MsgAutopilot || m.From != me.Name {
			continue // the room's concurrent traffic
		}
		if tf.json {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		}
		if m.Event == "denied" {
			if !tf.json {
				fmt.Fprintf(os.Stderr, "autopilot complete: 被拒：%s\n", m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
		if !tf.json {
			fmt.Printf("[%s] %s\n", me.Name, m.Text)
		}
		taskBye(ctx, conn)
		return 0
	}
}
