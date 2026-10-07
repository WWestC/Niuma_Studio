package cli

// ack.go — niuma ack：成员的一次性「收到」回执动词（结构化姊妹路）。
// 注入 footer 教的「整轮只回一次『收到』」是文本协议——措辞稍偏即失
// 效；这个动词让成员在回合里跑一条命令，把本轮欠的全部点名行一次性
// 挂上回执，不依赖任何措辞。凭座位 token 顶座（与 plan submit 同路），
// seq 缺席＝「回执全部」（服务端问调度器要本轮货单）。

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
	"github.com/coder/websocket/wsjson"
)

// runAck dials the member's own seat and receipts the whole turn.
// The operator identity is the MEMBER running this command — never the
// host (detectLocalName would dial the owner's seatless channel, whose
// ack frames are refused): --name wins, else the workspace's staffing
// table resolves cwd→member, else the seat-token files in the home
// directory are probed for the newest one (a dispatcher-held seat
// writes it on attach).
func runAck(args []string) int {
	fs := flag.NewFlagSet("ack", flag.ContinueOnError)
	name := fs.String("name", "", "member identity (default: resolve from this workspace's staffing row)")
	project := fs.String("project", "", "office to dial (default: the workspace's bound project; '-' = the lobby)")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == "" {
		*project = scopeProject()
	}
	*project = pickScope(*project)
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ack:", err)
			return 1
		}
		*port = p
	}
	memberName := strings.TrimSpace(*name)
	if memberName == "" {
		memberName = staffingMemberForProject(*project)
	}
	if memberName == "" {
		fmt.Fprintln(os.Stderr, "ack: 无法确定操作者身份——请显式传 --name <成员名>（成员身份不会从主机名猜测）")
		return 2
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", *port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := agentOpDial(ctx, url, memberName, *project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ack:", err)
		return 1
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgAck}); err != nil {
		fmt.Fprintln(os.Stderr, "ack:", err)
		return 1
	}
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "ack: 办公室无回执（%v）\n", err)
			return 1
		}
		if m.Type != chat.MsgSystem {
			continue
		}
		if !strings.HasPrefix(m.Text, "ack：") {
			continue
		}
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
