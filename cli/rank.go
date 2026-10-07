package cli

// rank — 远程调级（v0.8 M1 / t_10）。CLI 写走 WS：
//
//	rank --name 小明 --set 6 [--as HR] [--port N] [--json]
//
// 参数面与 kick 同风格：--name 是**目标成员**，操作者身份是 --as
// （缺省自动探测房主名，显式 --as 走服务端委托判定——Lv.8+ 仅严格
// 低于自己者、新等级 ≤ 自己等级 − 2）。退出码 0 成功 / 1 被拒 /
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

	"github.com/coder/websocket/wsjson"
)

func runRank(args []string) int {
	fs := flag.NewFlagSet("rank", flag.ContinueOnError)
	target := fs.String("name", "", "member whose rank to set (required)")
	set := fs.Int("set", 0, "new rank, 1-9 (required)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*target) == "" || *set == 0 {
		fmt.Fprintln(os.Stderr, "rank: --name 与 --set 必填（例：rank --name 小明 --set 6 [--as HR]）")
		return 2
	}
	if *set < 1 || *set > 9 {
		fmt.Fprintf(os.Stderr, "rank: --set 须为 1–9 的整数（收到 %d）\n", *set)
		return 2
	}
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "rank:", err)
			return 1
		}
		*port = p
	}
	actor := *as
	if actor == "" {
		host, err := detectLocalName(*port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rank:", err)
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
		fmt.Fprintln(os.Stderr, "rank:", err)
		return 1
	}
	defer conn.CloseNow()
	// A local frame struct keeps the wire key "rank" as the PRD spells
	// it (chat.Message has no rank field, and borrowing Rev would put
	// "rev":6 on the wire).
	frame := struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Rank int    `json:"rank"`
	}{Type: server.MsgRankSet, Name: *target, Rank: *set}
	if err := wsjson.Write(ctx, conn, frame); err != nil {
		fmt.Fprintln(os.Stderr, "rank:", err)
		return 1
	}
	// Receipts: success is the private "已将 X 的等级调整为 N" frame;
	// failure the private "rank_set 被拒：…" one. The broadcast
	// announcement (UI-wording) may arrive too — it matches neither.
	receipt := fmt.Sprintf("已将 %s 的等级调整为 %d", *target, *set)
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "rank: 办公室无回执（%v）；服务端为 v0.7 及更早时没有 rank_set 帧，需 v0.8 M1\n", err)
			return 1
		}
		if m.Type != chat.MsgSystem {
			continue
		}
		if m.Text == receipt {
			if *asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Println(receipt)
			}
			taskBye(ctx, conn)
			return 0
		}
		if strings.HasPrefix(m.Text, "rank_set 被拒") {
			if *asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Fprintf(os.Stderr, "rank: %s\n", m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
	}
}
