package cli

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

	"github.com/coder/websocket/wsjson"
)

// mirror 是房主专属的反向同步通道（v0.6 M1）：把房主在某个 AI 会话
// 窗口里输入的话，以房主名义送进办公室——历史可见、@ 解析照常（被 @ 的
// 成员照常被唤醒）。
//
//	$EXE mirror --text "@小明 把检索结果写回手册" [--name 房主名]
//
// --name 缺省时自动探测房主名（GET /kb/people 找 local:true 者），这让
// hook 配置片段做到复制粘贴级通用。显式 --name 且非房主名 → 服务端
// 降级为普通发言，CLI 打印提示。
//
// owner-M1（p/book 事故根治）：本通道发言必须携带房主座位凭证——名字
// 不再是身份。凭证来源 --token > 环境变量 NIUMA_OWNER_TOKEN >
// ~/.niuma_token_<房主名>（工作室每次启动自动落盘）。没有凭证就是没有
// 发言权：AI 成员的工具箱里没有这份凭证，手册与出生提示词同日写明
// mirror 不是成员动词；服务端逐帧验凭，无凭帧一律拒绝入库。
func runMirror(args []string) int {
	fs := flag.NewFlagSet("mirror", flag.ContinueOnError)
	text := fs.String("text", "", "the line to relay (the owner's session input)")
	name := fs.String("name", "", "owner display name (default: auto-detect the local:true person)")
	role := fs.String("role", "房主", "seat role when the owner seat happens to be free")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	token := fs.String("token", "", "owner seat credential (default: $NIUMA_OWNER_TOKEN, then ~/.niuma_token_<owner>)")
	asJSON := fs.Bool("json", false, "JSON-lines output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// positional text is accepted alongside --text (agents write both)
	if fs.NArg() > 0 && *text == "" {
		*text = strings.Join(fs.Args(), " ")
	}
	*text = strings.TrimSpace(*text)
	if *text == "" {
		fmt.Fprintln(os.Stderr, "mirror: empty text (use --text \"…\")")
		return 2
	}

	p := *port
	if p == 0 {
		got, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "mirror:", err)
			return 1
		}
		p = got
	}

	owner := *name
	if owner == "" {
		got, err := probeOwner(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mirror:", err)
			return 1
		}
		owner = got
	}

	cred := resolveOwnerCredential(*token, owner)
	if cred == "" {
		fmt.Fprintf(os.Stderr, "mirror: 未找到房主凭证（%s 不存在且未设 NIUMA_OWNER_TOKEN）——以房主名义发言需要凭证，凭名字已不再受理；先启动一次工作室让落座铸凭，或用 --token 传入。\n", chat.SeatTokenPath(owner))
		return 1
	}

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", p)
	ctx := context.Background()
	// replay=false: the relay must not mistake replayed history for its
	// own echo.
	conn, me, err := dialToken(ctx, url, owner, *role, false, cred)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mirror:", err)
		return 1
	}
	defer conn.CloseNow()

	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgSay, Text: *text, Origin: chat.OriginMirror}); err != nil {
		fmt.Fprintln(os.Stderr, "mirror:", err)
		return 1
	}

	// Await the echo of our own line: the seatless path answers with the
	// delivered frame; a normal-seat path (owner seat was free, or a
	// non-owner --name got downgraded) echoes via the room broadcast.
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		var m chat.Message
		if err := wsjson.Read(rctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "mirror: 未收到服务端回显（超时）\n")
			return 1
		}
		if m.Type == chat.MsgSay && m.From == me.Name && m.Text == *text {
			_ = wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgBye})
			switch m.Origin {
			case chat.OriginMirror:
				if *asJSON {
					b, _ := json.Marshal(m)
					fmt.Println(string(b))
				} else {
					fmt.Printf("[%s][↩ 镜像] %s\n", m.From, m.Text)
				}
				return 0
			default:
				fmt.Fprintf(os.Stderr, "mirror: 非房主名（%s），已按普通发言发送\n", me.Name)
				fmt.Printf("[%s] %s\n", m.From, m.Text)
				return 0
			}
		}
		if m.Type == chat.MsgSystem {
			fmt.Fprintln(os.Stderr, "mirror:", m.Text)
			return 1
		}
	}
}

// resolveOwnerCredential resolves the mirror speaker's proof, three
// tiers: explicit --token, the NIUMA_OWNER_TOKEN environment (hook
// snippets that prefer not to touch files), then the persisted seat
// credential the studio writes at every boot. "" means none found —
// the caller refuses; a missing proof is a hard stop, never a
// downgrade to name-only speech.
func resolveOwnerCredential(explicit, owner string) string {
	if t := strings.TrimSpace(explicit); t != "" {
		return t
	}
	if t := strings.TrimSpace(os.Getenv("NIUMA_OWNER_TOKEN")); t != "" {
		return t
	}
	return loadSeatToken(owner)
}

// probeOwner fetches the roster and returns the local:true person — the
// one human this machine trusts (v0.6 §4.2). A missing room or a roster
// without a local entry is a hard error: mirror must never guess an
// identity.
func probeOwner(port int) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/kb/people", port))
	if err != nil {
		return "", fmt.Errorf("办公室不在（%v）——先启动 ./niuma", err)
	}
	defer resp.Body.Close()
	var people []kb.PersonSummary
	if err := json.NewDecoder(resp.Body).Decode(&people); err != nil {
		return "", fmt.Errorf("解析 /kb/people 失败: %v", err)
	}
	for _, pr := range people {
		if pr.Local {
			return pr.Name, nil
		}
	}
	return "", fmt.Errorf("牛马名册中未找到房主（local:true）——办公室可能未以房主身份运行")
}
