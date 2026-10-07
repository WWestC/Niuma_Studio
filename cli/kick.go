package cli

// kick — 移出成员（v0.6 M2 / t_05）。CLI 写走 WS：
//
//	kick --name 小明 [--as 房主名|HR] [--port N] [--json]
//
// 注意参数面与其它子命令相反（PRD §4.3 既定）：--name 是**目标成员**，
// 操作者身份是 --as——缺省自动探测房主名（GET /kb/people 找
// local:true），显式 --as 走服务端等级判定。退出码 0 成功 / 1 失败
// （权限不足、目标不存在、办公室不在）/ 2 用法错误。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func runKick(args []string) int {
	fs := flag.NewFlagSet("kick", flag.ContinueOnError)
	target := fs.String("name", "", "member to remove (required)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*target) == "" {
		fmt.Fprintln(os.Stderr, "kick: --name 目标成员必填（例：kick --name 小明 [--as HR]）")
		return 2
	}
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "kick:", err)
			return 1
		}
		*port = p
	}
	actor := *as
	if actor == "" {
		host, err := detectLocalName(*port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "kick:", err)
			return 1
		}
		actor = host
	}
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", *port)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// 管理面拨号跟操作者绑定房走（房主未绑定＝大厅旧拨号；项目 HR
	// 从自己工作区拨自己的房——凭证只在本房 redeem）。
	conn, _, err := opDial(ctx, url, actor, dialRoom(""))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kick:", err)
		return 1
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, chat.Message{Type: server.MsgKick, Name: *target}); err != nil {
		fmt.Fprintln(os.Stderr, "kick:", err)
		return 1
	}
	// The receipt: success is the broadcast notice (byte-identical to
	// the UI kick), failure the private "kick 被拒：…" system frame.
	// Other system lines are concurrent room events — ignore.
	notice := fmt.Sprintf("已将 %s 移出办公室", *target)
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "kick: 办公室无回执（%v）；服务端为 v0.5 及更早时没有 kick 帧，需 v0.6 M2\n", err)
			return 1
		}
		if m.Type != chat.MsgSystem {
			continue
		}
		if m.Text == notice {
			if *asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Println(notice)
			}
			taskBye(ctx, conn)
			return 0
		}
		if strings.HasPrefix(m.Text, "kick 被拒") {
			if *asJSON {
				b, _ := json.Marshal(m)
				fmt.Println(string(b))
			} else {
				fmt.Fprintf(os.Stderr, "kick: %s\n", m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
	}
}

// opDial connects as the operator identity for a management command
// (kick / rank_set / …) and refuses a deduped "-2" name: management
// permissions bind to the exact name's rank, so operating from a
// phantom seat both misjudges the matrix and litters the room. project
// rides hello.project so an operator seated in a project office (an
// HR) dials their own room — the credential only redeems there.
func opDial(ctx context.Context, url, name, project string) (*websocket.Conn, chat.Member, error) {
	dialOp := func() (*websocket.Conn, chat.Member, error) {
		return dialTokenRoom(ctx, url, name, "", false, loadSeatToken(name), project)
	}
	conn, me, err := dialOp()
	if err != nil || me.Name == name {
		return conn, me, err
	}
	taskBye(ctx, conn)
	conn, me, err = dialOp()
	if err != nil || me.Name == name {
		return conn, me, err
	}
	_ = conn.CloseNow()
	return nil, me, fmt.Errorf("座位被 %s 的既有连接占用（本次被顶为 %s）；管理权限按精确名字的等级判定，请先停同名值守或稍后重试", name, me.Name)
}

// detectLocalName finds the local host's display name — the row with
// local:true in /kb/people. A missing host is a loud error: kick must
// never silently act under a guessed identity.
func detectLocalName(port int) (string, error) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/kb/people", port))
	if err != nil {
		return "", fmt.Errorf("办公室不可达（GET /kb/people）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /kb/people: status %d", resp.StatusCode)
	}
	var people []kb.PersonSummary
	if err := json.NewDecoder(resp.Body).Decode(&people); err != nil {
		return "", fmt.Errorf("解析 /kb/people: %w", err)
	}
	for _, pr := range people {
		if pr.Local {
			return pr.Name, nil
		}
	}
	return "", fmt.Errorf("未找到本机房主（/kb/people 无 local:true 行）；请显式指定操作者身份")
}
