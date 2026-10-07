package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/util"
)

// The kb readers: manual/notice/people/tasks/docs over the HTTP face
// (text or --json).

// runKB reads the room's blackboard knowledge base: the manual, the
// personnel list, one person's detail — or (notice) the chat face's
// per-room group announcement, the retired blackboard notice's
// successor.
func runKB(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma kb <manual [词|--index]|notice [--project KEY]|people|person NAME|docs|doc KEY|history KEY|write KEY|append KEY TEXT|restore KEY|archive KEY|unarchive KEY|delete KEY|export [DIR]|establishment> [--port N] [--json]")
		return 2
	}
	switch args[0] {
	case "manual":
		return runKBText(args[0], args[1:])
	case "notice":
		return runNoticeRead(args[1:])
	case "people":
		return runKBPeople(args[1:])
	case "person":
		return runKBPerson(args[1:])
	case "docs":
		return runKbDocs(args[1:])
	case "doc":
		return runKbDoc(args[1:])
	case "history":
		return runKbHistory(args[1:])
	case "write":
		return runKbWrite(args[1:])
	case "append":
		return runKbAppend(args[1:])
	case "restore":
		return runKbRestore(args[1:])
	case "archive":
		return runKbArchive(args[1:], true)
	case "unarchive":
		return runKbArchive(args[1:], false)
	case "delete":
		return runKbDelete(args[1:])
	case "export":
		return runKbExport(args[1:])
	case "establishment":
		return runKbEstablishment(args[1:])
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "kb: unknown topic %q (manual|notice|people|person|docs|doc|history|write|append|restore|archive|unarchive|delete|export)\n", args[0])
		return 2
	}
}

// kbResolvePort finds the target port: explicit flag or discovery.
func kbResolvePort(port int) (int, bool) {
	if port != 0 {
		return port, true
	}
	p, err := server.DiscoverPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 0, false
	}
	return p, true
}

// runKBText prints /kb/manual as plain text: the full book by default,
// --index for the numbered TOC, or a keyword (positional or --q) for
// only the matching sections — the server face composes both slice
// answers (kb/manualsec.go). Flags come first when mixing (Go 的
// flag 语义：位置词之后的 flag 不再被解析），--q 是等价的长形——
// 两条路都教，成员怎么顺手怎么来。
func runKBText(topic string, args []string) int {
	fs := flag.NewFlagSet("kb "+topic, flag.ContinueOnError)
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	index := fs.Bool("index", false, "只列手册目录（节标题索引）")
	qFlag := fs.String("q", "", "关键词：只回命中的节")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, ok := kbResolvePort(*port)
	if !ok {
		return 1
	}
	u := fmt.Sprintf("http://127.0.0.1:%d/kb/%s", p, topic)
	q := strings.TrimSpace(*qFlag)
	if q == "" && fs.NArg() > 0 {
		q = strings.TrimSpace(fs.Args()[0]) // 位置参数即关键词
	}
	switch {
	case *index:
		u += "?index=1"
	case q != "":
		u += "?q=" + url.QueryEscape(q)
	}
	resp, err := http.Get(u)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 1
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 1
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		fmt.Fprintln(os.Stderr, "kb: empty", topic)
		return 0
	}
	fmt.Println(s)
	return 0
}

// runNoticeRead prints a room's group announcement (the chat face's
// per-room notice — the retired blackboard notice's successor):
// GET /p/{key}/notice, default the lobby (--project names another
// room). --json prints the full snapshot (content/by/ts/rev).
func runNoticeRead(args []string) int {
	fs := flag.NewFlagSet("kb notice", flag.ContinueOnError)
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	project := fs.String("project", "default", "room key (default: the lobby)")
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, ok := kbResolvePort(*port)
	if !ok {
		return 1
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/%s/notice", p, url.PathEscape(*project)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb notice:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "kb notice: %s\n", strings.TrimSpace(string(b)))
		return 1
	}
	var body struct {
		Project string `json:"project"`
		Notice  *struct {
			Content string `json:"content"`
			By      string `json:"by,omitempty"`
			TS      int64  `json:"ts,omitempty"`
			Rev     int    `json:"rev"`
		} `json:"notice"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		fmt.Fprintln(os.Stderr, "kb notice:", err)
		return 1
	}
	if body.Notice == nil {
		fmt.Fprintf(os.Stderr, "kb notice: 房间 %s 暂无公告（房主在对话区发布）\n", body.Project)
		return 0
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(body.Notice)
		return 0
	}
	if body.Notice.By != "" {
		who := body.Notice.By
		if body.Notice.TS > 0 {
			who += " · " + time.Unix(body.Notice.TS, 0).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(os.Stdout, "（%s 发布）\n", who)
	}
	fmt.Println(body.Notice.Content)
	return 0
}

// runKBPeople prints the merged personnel roster. Scoped runs (the
// workspace binding or --project) see one office's roster: its seats,
// its staffing rows and the host — never another project's people.
func runKBPeople(args []string) int {
	fs := flag.NewFlagSet("kb people", flag.ContinueOnError)
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "JSON output")
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project, NIUMA_PROJECT overrides; empty = studio-wide)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p, ok := kbResolvePort(*port)
	if !ok {
		return 1
	}
	scope := pickScope(*project)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/kb/people%s", p, withQuery("", "project", scope)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 1
	}
	defer resp.Body.Close()
	var people []kb.PersonSummary
	if err := json.NewDecoder(resp.Body).Decode(&people); err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(people)
		return 0
	}
	if len(people) == 0 {
		fmt.Println("(blackboard is empty)")
		return 0
	}
	for _, pr := range people {
		line := pr.Name
		if pr.Role != "" {
			line += "\t" + pr.Role
		}
		state := "离线"
		if pr.Online {
			state = "在线"
			if pr.Grace { // presence-grace seat: no live connection (v0.9 ME1)
				state = "在线(宽限)"
			}
			// 干活位（v2.10 同步收口）：调度器的回合进行中 tell——侧栏
			// chip 的同一位真相。文本面此前漏打，AI 成员（HR 查名册报
			// 「都空闲」）与房主所见各说各话；现在名册口径全线一致。
			if pr.Working {
				state += "·干活中"
			}
		}
		if pr.Local {
			state += "·本机"
		}
		if pr.Room != "" && pr.Room != "default" {
			state += "@" + pr.Room // seated in that project's office
		}
		line += "\t" + state
		if pr.LastReportTS > 0 {
			line += fmt.Sprintf("\t汇报于 %s", time.Unix(pr.LastReportTS, 0).Format("15:04"))
		}
		fmt.Println(line)
	}
	return 0
}

// runKBPerson prints one person's board page. Scoped runs can only
// name people on the office's roster (404 otherwise).
func runKBPerson(args []string) int {
	fs := flag.NewFlagSet("kb person", flag.ContinueOnError)
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	asJSON := fs.Bool("json", false, "JSON output")
	project := fs.String("project", "", "project key to scope to (default: the workspace's bound project; empty = studio-wide)")
	// flags may appear before or after the name (agents write both);
	// flag.Parse stops at the first positional, so keep parsing
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	name := ""
	if len(positional) > 0 {
		name = positional[0]
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "kb person: name required, e.g. kb person Bot")
		return 2
	}
	p, ok := kbResolvePort(*port)
	if !ok {
		return 1
	}
	scope := pickScope(*project)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/kb/people/%s%s", p, url.PathEscape(name), withQuery("", "project", scope)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "kb: no such person %q\n", name)
		return 1
	}
	var d kb.PersonDetail
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		fmt.Fprintln(os.Stderr, "kb:", err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(d)
		return 0
	}
	state := "离线"
	if d.Online {
		state = "在线"
	}
	if d.Local {
		state += " · 本机"
	}
	// 干活位与已进行时长（v2.10）：与侧栏 chip 同源——kb people 的详情
	// 面也照说，AI 成员点名查人时读到的是同一份真相。
	if d.Working {
		state += " · 干活中"
		if d.WorkingSince > 0 {
			state += fmt.Sprintf("（已 %s）", util.HumanWait(time.Now().Unix()-d.WorkingSince))
		}
	}
	role := d.Role
	if role == "" {
		role = "—"
	}
	fmt.Printf("名称: %s\n身份: %s\n状态: %s\n", d.Name, role, state)
	if d.Manual != "" {
		fmt.Printf("岗位手册: %s\n", d.Manual)
	}
	if d.LastReport != "" {
		ts := ""
		if d.LastReportTS > 0 {
			ts = " (" + time.Unix(d.LastReportTS, 0).Format("01-02 15:04") + ")"
		}
		fmt.Printf("最近汇报%s:\n", ts)
		for _, ln := range strings.Split(d.LastReport, "\n") {
			fmt.Println("  " + ln)
		}
	}
	if d.Prompt != "" {
		fmt.Println("提示词:")
		for _, ln := range strings.Split(d.Prompt, "\n") {
			fmt.Println("  " + ln)
		}
	} else {
		fmt.Println("提示词: （无保存配置）")
	}
	return 0
}

// printStream reads messages and prints them until ctx is done. me is
// the local member name, used to flag messages that @-mention us.
