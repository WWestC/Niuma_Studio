package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"os"
	"slices"
	"time"
)

// The stream printers: raw or human-formatted room lines (shared by
// say/listen/wait), the member-color renderer and the default seat
// name.

// printStream reads messages and prints them until ctx is done. me is
// the local member name, used to flag messages that @-mention us.
func printStream(ctx context.Context, conn *websocket.Conn, asJSON bool, me string) {
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return
		}
		switch m.Type {
		case chat.MsgSay, chat.MsgReport, chat.MsgJoin, chat.MsgLeave, chat.MsgSystem, chat.MsgTask:
			if asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else if m.Type == chat.MsgSay && me != "" && slices.Contains(m.Mentions, me) {
				fmt.Println("[@你] " + human(m))
			} else {
				fmt.Println(human(m))
			}
		}
	}
}

func human(m chat.Message) string {
	ts := time.Unix(m.TS, 0).Format("15:04:05")
	switch m.Type {
	case chat.MsgSay:
		return fmt.Sprintf("%s [%s] %s", ts, m.From, m.Text)
	case chat.MsgReport:
		return fmt.Sprintf("%s [%s][汇报] %s", ts, m.From, m.Text)
	case chat.MsgJoin:
		return fmt.Sprintf("%s -- %s 加入了办公室 --", ts, m.From)
	case chat.MsgLeave:
		return fmt.Sprintf("%s -- %s 离开了办公室 --", ts, m.From)
	case chat.MsgTask:
		return fmt.Sprintf("%s [任务] %s", ts, m.Text)
	default:
		return fmt.Sprintf("%s -- %s --", ts, m.Text)
	}
}

// failNoName is the shared refusal for room-dialing commands run
// without --name. The CLI never invents an operator identity: the old
// hostname-derived default (「3StripFishde」 — hostname prefix) joined
// the room as a stranger and left a grace ghost in the roster, the
// documented 「凡 CLI 操作必带 --name」 rule now enforced in code.
// Called before any probe or dial: zero side effects.
func failNoName(cmd string) int {
	fmt.Fprintf(os.Stderr, "%s: 缺少 --name（操作者身份）——不自动取名（自动名会以陌生身份占座、退出后留幽灵席位），本次未拨号、无办公室副作用。请带 --name <名字> 重试；在线名单：niuma members\n", cmd)
	return 2
}
