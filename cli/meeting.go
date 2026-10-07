package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket/wsjson"
)

// runMeeting implements the meeting-control CLI (需求评审会议): the
// chair's grip on the live agenda. end fires the current window now
// (讨论段直进总结、总结段当场散会); extend <minutes> pushes the
// current window's deadline out (default 10, clamped 1–60 a call,
// calls stack). Both write over WS dialing INTO the project's room
// under the chair's own name (the plan-submit shape); the answer is
// the dispatcher's own "meeting" broadcast (advanced/extended/ended),
// a denial the private frame.
func runMeeting(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma meeting <end|extend> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "end":
		return runMeetingEnd(rest)
	case "extend":
		return runMeetingExtend(rest)
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "meeting: unknown subcommand %q (end|extend)\n", sub)
		return 2
	}
}

// runMeetingEnd sends meeting_end — fire the current agenda window now.
func runMeetingEnd(args []string) int {
	const cmd = "meeting end"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key the meeting sits in (required)")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	return meetingCtlFrame(cmd, tf, chat.MsgMeetingEnd, 0, *project)
}

// runMeetingExtend sends meeting_extend {minutes} — the positional
// minutes default to 10.
func runMeetingExtend(args []string) int {
	const cmd = "meeting extend"
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key the meeting sits in (required)")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	minutes := 10
	if len(pos) > 0 {
		if n, err := strconv.Atoi(pos[0]); err == nil && n > 0 {
			minutes = n
		} else {
			fmt.Fprintf(os.Stderr, "%s: 分钟数应为正整数，得到 %q\n", cmd, pos[0])
			return 2
		}
	}
	return meetingCtlFrame(cmd, tf, chat.MsgMeetingExtend, minutes, *project)
}

// meetingCtlFrame validates the shared flags and performs the write.
func meetingCtlFrame(cmd string, tf *taskFlags, msgType string, minutes int, project string) int {
	if project == "" {
		fmt.Fprintf(os.Stderr, "%s: --project required\n", cmd)
		return 2
	}
	if tf.name == "" {
		return failNoName(cmd)
	}
	return runMeetingWrite(tf, cmd, chat.Message{Type: msgType, Minutes: minutes}, project)
}

// runMeetingWrite performs one meeting WS write and waits for the
// receipt: the dispatcher's "meeting" broadcast (advanced/extended/
// ended) or the private denial — the plan-write contract; the room's
// concurrent traffic skips. Exits non-zero on denial.
func runMeetingWrite(tf *taskFlags, cmd string, frame chat.Message, project string) int {
	port, ok := tf.resolvePort(cmd)
	if !ok {
		return 1
	}
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, me, err := planDial(ctx, url, tf.name, project, cmd)
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
		if m.Type != chat.MsgMeeting {
			continue // the room's concurrent traffic
		}
		if m.Event == "denied" {
			if tf.json {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Fprintf(os.Stderr, "%s: 被拒：%s\n", cmd, m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
		if tf.json {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		} else {
			id := ""
			if m.Meeting != nil {
				id = m.Meeting.ID
			}
			fmt.Printf("[%s] %s（%s）\n", me.Name, m.Text, id)
		}
		taskBye(ctx, conn)
		return 0
	}
}
