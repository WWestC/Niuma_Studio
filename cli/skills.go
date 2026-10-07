package cli

// skill / mcp / assemble — 技能与 MCP 命令族。
//
//	$EXE skill list                                      技能库列表（HTTP 读）
//	$EXE skill show KEY                                  单条详情（HTTP 读）
//	$EXE skill rm KEY                                    删技能（HTTP DELETE，回环本机）
//	$EXE skill save --key K --name 名 [--desc ...]
//	      [--body T | --body-file F] [--manuals a,b]     技能库 upsert（WS 写）
//	$EXE mcp list                                        MCP 服务库列表（HTTP 读）
//	$EXE mcp show KEY                                    单条详情（HTTP 读，含请求头原值）
//	$EXE mcp rm KEY                                      删服务（HTTP DELETE）
//	$EXE mcp save --key K --name 名 --type http|sse --url U
//	      [--header 名=值（可重复）]                     服务库 upsert（WS 写）
//	$EXE assemble --project P --person N
//	      [--skills a,b] [--mcps x,y] [--op set|add|remove]
//	      [--target staffing|profile]                    装配（WS 写）
//
// 写通道走 WS（skill_save / mcp_save / assemble 帧，回执即 skill/mcp 帧），
// 操作者身份 --as（缺省自动探测房主名）；--via 由本 CLI 自动标记（审计的
// surface 列记 cli）。退出码 0 成功 / 1 被拒或办公室不可达 / 2 用法错误。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server"

	"github.com/coder/websocket/wsjson"
)

func runSkill(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma skill <list|show|rm|save> [flags]")
		return 2
	}
	switch args[0] {
	case "list":
		return runSkillList(args[1:])
	case "show":
		return runSkillShow(args[1:])
	case "rm":
		return runSkillRemove(args[1:])
	case "save":
		return runSkillSave(args[1:])
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "skill: unknown subcommand %q (list|show|rm|save)\n", args[0])
		return 2
	}
}

func runMCP(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma mcp <list|show|rm|save> [flags]")
		return 2
	}
	switch args[0] {
	case "list":
		return runMCPList(args[1:])
	case "show":
		return runMCPShow(args[1:])
	case "rm":
		return runMCPRemove(args[1:])
	case "save":
		return runMCPSave(args[1:])
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "mcp: unknown subcommand %q (list|show|rm|save)\n", args[0])
		return 2
	}
}

func capPort(fs *flag.FlagSet) *int { return fs.Int("port", 0, "server port (default: auto-discover)") }

// libHTTPGet GETs one read endpoint, decoding the body into out on
// success; returns the exit code, or -1 for the caller to print.
func libHTTPGet(url string, out any) int {
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "办公室不可达（", err, "）")
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := ""
		if b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10)); err == nil {
			msg = strings.TrimSpace(string(b))
		}
		fmt.Fprintf(os.Stderr, "GET %s: status %d %s\n", url, resp.StatusCode, msg)
		return 1
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		fmt.Fprintln(os.Stderr, "解析响应失败：", err)
		return 1
	}
	return -1 // caller prints
}

func discoverPort(verb string, port int) (int, int) {
	if port != 0 {
		return port, -1
	}
	p, err := server.DiscoverPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, verb+":", err)
		return 0, 1
	}
	return p, -1
}

func runSkillList(args []string) int {
	fs := flag.NewFlagSet("skill list", flag.ContinueOnError)
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, code := discoverPort("skill list", *port)
	if code >= 0 {
		return code
	}
	var skills []map[string]any
	if code := libHTTPGet(fmt.Sprintf("http://127.0.0.1:%d/skills", p), &skills); code >= 0 {
		return code
	}
	if *asJSON {
		b, _ := json.Marshal(skills)
		fmt.Println(string(b))
		return 0
	}
	if len(skills) == 0 {
		fmt.Println("技能库为空（skill save --key … 先建一个，或房里 /skill new）")
		return 0
	}
	for _, sk := range skills {
		line := fmt.Sprintf("%s\t%s", sk["key"], sk["name"])
		var slots []string
		if n, _ := sk["body_bytes"].(float64); n > 0 {
			slots = append(slots, fmt.Sprintf("正文 %dB", int(n)))
		}
		if m, ok := sk["manuals"].([]any); ok && len(m) > 0 {
			ms := make([]string, 0, len(m))
			for _, v := range m {
				ms = append(ms, fmt.Sprint(v))
			}
			slots = append(slots, "手册 "+strings.Join(ms, ","))
		}
		if len(slots) > 0 {
			line += "\t[" + strings.Join(slots, ", ") + "]"
		}
		fmt.Println(line)
	}
	return 0
}

func runSkillShow(args []string) int {
	fs := flag.NewFlagSet("skill show", flag.ContinueOnError)
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: niuma skill show KEY")
		return 2
	}
	p, code := discoverPort("skill show", *port)
	if code >= 0 {
		return code
	}
	var sk map[string]any
	if code := libHTTPGet(fmt.Sprintf("http://127.0.0.1:%d/skills/%s", p, fs.Arg(0)), &sk); code >= 0 {
		return code
	}
	if *asJSON {
		b, _ := json.Marshal(sk)
		fmt.Println(string(b))
		return 0
	}
	b, _ := json.MarshalIndent(sk, "", "  ")
	fmt.Println(string(b))
	return 0
}

// runKeyedDelete DELETEs one asset over the loopback HTTP face — a
// thin write like /dispatch/recall (same localhost-trust model, no WS
// round trip needed for a keyed delete). The JSON body carries the
// result {ok, key, referenced_by}; the receipt text echoes the
// server's (it names the assemblies still holding the key).
func runKeyedDelete(verb, kind, key string, port int, asJSON bool) int {
	p, code := discoverPort(verb, port)
	if code >= 0 {
		return code
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/%s/%s", p, kind, key)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, verb+":", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, verb+": 办公室不可达（", err, "）")
		return 1
	}
	defer resp.Body.Close()
	var out struct {
		OK           bool     `json:"ok"`
		Key          string   `json:"key"`
		ReferencedBy []string `json:"referenced_by"`
		Error        string   `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fmt.Fprintf(os.Stderr, "%s: 解析响应失败：%v\n", verb, err)
		return 1
	}
	if resp.StatusCode != http.StatusOK || !out.OK {
		if out.Error != "" {
			fmt.Fprintln(os.Stderr, verb+":", out.Error)
		} else {
			fmt.Fprintf(os.Stderr, "%s: HTTP %d\n", verb, resp.StatusCode)
		}
		return 1
	}
	if asJSON {
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
		return 0
	}
	noun := "技能"
	if kind == "mcps" {
		noun = "MCP 服务"
	}
	text := fmt.Sprintf("已删除%s %s", noun, out.Key)
	if len(out.ReferencedBy) > 0 {
		text += "——仍在装配中，席位上已失效：" + strings.Join(out.ReferencedBy, "、")
	}
	fmt.Println(text)
	return 0
}

func runSkillRemove(args []string) int {
	fs := flag.NewFlagSet("skill rm", flag.ContinueOnError)
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: niuma skill rm KEY")
		return 2
	}
	return runKeyedDelete("skill rm", "skills", fs.Arg(0), *port, *asJSON)
}

func runSkillSave(args []string) int {
	fs := flag.NewFlagSet("skill save", flag.ContinueOnError)
	key := fs.String("key", "", "skill key, [a-z0-9_-]{1,32} (required)")
	name := fs.String("name", "", "display name (default: the key)")
	desc := fs.String("desc", "", "description")
	body := fs.String("body", "", "instruction body (the skill's fabric)")
	bodyFile := fs.String("body-file", "", "read the body from this file")
	manuals := fs.String("manuals", "", "comma-separated kb doc keys (≤4)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*key) == "" {
		fmt.Fprintln(os.Stderr, "skill save: --key 必填（例：skill save --key code-review --name 评审 --body-file review.md）")
		return 2
	}
	sk := chat.Skill{Key: strings.TrimSpace(*key)}
	if sk.Name = strings.TrimSpace(*name); sk.Name == "" {
		sk.Name = sk.Key
	}
	sk.Desc = *desc
	sk.Body = *body
	if *bodyFile != "" {
		b, err := os.ReadFile(*bodyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "skill save:", err)
			return 2
		}
		sk.Body = string(b)
	}
	if v := strings.TrimSpace(*manuals); v != "" {
		for _, m := range strings.Split(v, ",") {
			if m = strings.TrimSpace(m); m != "" {
				sk.Manuals = append(sk.Manuals, m)
			}
		}
	}
	return capWrite(*port, *as, *asJSON, chat.Message{
		Type: chat.MsgSkillSave, Skill: &sk, Via: "cli",
	}, "skill save")
}

func runMCPList(args []string) int {
	fs := flag.NewFlagSet("mcp list", flag.ContinueOnError)
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, code := discoverPort("mcp list", *port)
	if code >= 0 {
		return code
	}
	var ms []map[string]any
	if code := libHTTPGet(fmt.Sprintf("http://127.0.0.1:%d/mcps", p), &ms); code >= 0 {
		return code
	}
	if *asJSON {
		b, _ := json.Marshal(ms)
		fmt.Println(string(b))
		return 0
	}
	if len(ms) == 0 {
		fmt.Println("MCP 库为空（mcp save --key … 先建一个，或房里 /mcp add）")
		return 0
	}
	for _, m := range ms {
		fmt.Printf("%s\t%s\t%s %s\n", m["key"], m["name"], m["type"], m["url"])
	}
	return 0
}

func runMCPShow(args []string) int {
	fs := flag.NewFlagSet("mcp show", flag.ContinueOnError)
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: niuma mcp show KEY")
		return 2
	}
	p, code := discoverPort("mcp show", *port)
	if code >= 0 {
		return code
	}
	var m map[string]any
	if code := libHTTPGet(fmt.Sprintf("http://127.0.0.1:%d/mcps/%s", p, fs.Arg(0)), &m); code >= 0 {
		return code
	}
	if *asJSON {
		b, _ := json.Marshal(m)
		fmt.Println(string(b))
		return 0
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(b))
	return 0
}

func runMCPRemove(args []string) int {
	fs := flag.NewFlagSet("mcp rm", flag.ContinueOnError)
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: niuma mcp rm KEY")
		return 2
	}
	return runKeyedDelete("mcp rm", "mcps", fs.Arg(0), *port, *asJSON)
}

func runMCPSave(args []string) int {
	fs := flag.NewFlagSet("mcp save", flag.ContinueOnError)
	key := fs.String("key", "", "server key, [a-z0-9_-]{1,32} (required)")
	name := fs.String("name", "", "display name (default: the key)")
	desc := fs.String("desc", "", "description")
	typ := fs.String("type", "http", "transport: http|sse (default http)")
	url := fs.String("url", "", "http(s) endpoint (required)")
	header := fs.String("header", "", "request header 名=值（可重复；用逗号分隔多对）")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*key) == "" || strings.TrimSpace(*url) == "" {
		fmt.Fprintln(os.Stderr, "mcp save: --key 与 --url 必填（例：mcp save --key penpot --url http://127.0.0.1:4401/mcp）")
		return 2
	}
	m := chat.MCPServer{Key: strings.TrimSpace(*key), Type: strings.TrimSpace(*typ), URL: strings.TrimSpace(*url)}
	if m.Name = strings.TrimSpace(*name); m.Name == "" {
		m.Name = m.Key
	}
	m.Desc = *desc
	if v := strings.TrimSpace(*header); v != "" {
		for _, pair := range strings.Split(v, ",") {
			pair = strings.TrimSpace(pair)
			name, val, ok := strings.Cut(pair, "=")
			if !ok || strings.TrimSpace(name) == "" {
				fmt.Fprintf(os.Stderr, "mcp save: --header 形状应为 名=值，得 %q\n", pair)
				return 2
			}
			m.Headers = append(m.Headers, chat.MCPHeader{Name: strings.TrimSpace(name), Value: strings.TrimSpace(val)})
		}
	}
	return capWrite(*port, *as, *asJSON, chat.Message{
		Type: chat.MsgMCPSave, Mcp: &m, Via: "cli",
	}, "mcp save")
}

func runAssemble(args []string) int {
	fs := flag.NewFlagSet("assemble", flag.ContinueOnError)
	project := fs.String("project", "", "project key (required for --target staffing)")
	person := fs.String("person", "", "target member name (required)")
	skills := fs.String("skills", "", "comma-separated skill keys")
	mcps := fs.String("mcps", "", "comma-separated MCP server keys")
	op := fs.String("op", "set", "list op: set|add|remove (default set)")
	target := fs.String("target", "staffing", "assembly target: staffing|profile (default staffing)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	port := capPort(fs)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*person) == "" ||
		(strings.TrimSpace(*skills) == "" && strings.TrimSpace(*mcps) == "") {
		fmt.Fprintln(os.Stderr, "assemble: --person 与 --skills/--mcps 至少各给其一（例：assemble --project proj-x --person Delta --skills code-review）")
		return 2
	}
	msg := chat.Message{
		Type:    chat.MsgAssemble,
		Person:  strings.TrimSpace(*person),
		Target:  strings.TrimSpace(*target),
		Op:      strings.TrimSpace(*op),
		Project: strings.TrimSpace(*project),
		Via:     "cli",
	}
	for _, k := range strings.Split(*skills, ",") {
		if k = strings.TrimSpace(k); k != "" {
			msg.Skills = append(msg.Skills, k)
		}
	}
	for _, k := range strings.Split(*mcps, ",") {
		if k = strings.TrimSpace(k); k != "" {
			msg.MCPServers = append(msg.MCPServers, k)
		}
	}
	return capWrite(*port, *as, *asJSON, msg, "assemble")
}

// capWrite rides one library write frame over the operator's WS
// channel and waits out the receipt (denied included).
func capWrite(port int, actor string, asJSON bool, frame chat.Message, verb string) int {
	if port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, verb+":", err)
			return 1
		}
		port = p
	}
	if actor == "" {
		host, err := detectLocalName(port)
		if err != nil {
			fmt.Fprintln(os.Stderr, verb+":", err)
			return 1
		}
		actor = host
	}
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", port)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// The skill/MCP library is global but the operator's seat credential
	// is per-room: a member's saved token belongs to their project's
	// office, and a lobby dial with it collides into a ghost seat
	// ("-2"). Dial the workspace's bound room instead — the host stays
	// in the lobby under the same rule (its binding IS the lobby).
	room := pickScope("")
	if room == chat.LobbyKey {
		room = ""
	}
	conn, _, err := capOpDialRoom(ctx, url, actor, room)
	if err != nil {
		fmt.Fprintln(os.Stderr, verb+":", err)
		return 1
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, frame); err != nil {
		fmt.Fprintln(os.Stderr, verb+":", err)
		return 1
	}
	for {
		var m chat.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			fmt.Fprintf(os.Stderr, "%s: 办公室无回执（%v）；服务端为旧版本时没有该帧\n", verb, err)
			return 1
		}
		if m.Type != chat.MsgSkillEvt && m.Type != chat.MsgMcpEvt {
			continue
		}
		if asJSON {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		} else if m.Event == "denied" {
			fmt.Fprintln(os.Stderr, verb+":", m.Text)
		} else {
			fmt.Println(m.Text)
		}
		taskBye(ctx, conn)
		if m.Event == "denied" {
			return 1
		}
		return 0
	}
}
