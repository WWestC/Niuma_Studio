package cli

import (
	"bytes"
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
	"github.com/WWestC/Niuma_Studio/requirements"

	"github.com/coder/websocket/wsjson"
)

// runReq implements the requirement CLI (全智能模式 v2.8): list reads
// over HTTP (GET /p/{key}/reqs), create writes over WS dialing INTO
// the project room under the orchestrator's own name (the plan-submit
// shape — the autopilot poke's instructed verb). The reply is the
// private "req" frame; a denial exits non-zero.
func runReq(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: niuma req <list|create> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runReqList(rest)
	case "create":
		return runReqCreate(rest)
	case "park":
		return runReqPark(rest, true)
	case "unpark":
		return runReqPark(rest, false)
	case "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "req: unknown subcommand %q (list|create|park|unpark)\n", sub)
		return 2
	}
}

// runReqList prints one project's requirement ledger.
func runReqList(args []string) int {
	fs := flag.NewFlagSet("req list", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "default", "project key")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	port, ok := tf.resolvePort("req list")
	if !ok {
		return 1
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/p/%s/reqs", port, *project))
	if err != nil {
		fmt.Fprintln(os.Stderr, "req list:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "req list: 项目 %s 不存在或需求系统未启用\n", *project)
		return 1
	}
	var body struct {
		Project string             `json:"project"`
		Reqs    []requirements.Req `json:"reqs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		fmt.Fprintln(os.Stderr, "req list:", err)
		return 1
	}
	if tf.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(body.Reqs)
		return 0
	}
	if len(body.Reqs) == 0 {
		fmt.Println("（没有需求）")
		return 0
	}
	fmt.Printf("共 %d 条\n", len(body.Reqs))
	for _, r := range body.Reqs {
		line := fmt.Sprintf("%s\t[%s]\t%s", r.ID, r.Status, r.Title)
		if r.Status == requirements.StatusParking {
			// r_31 徽章：原因全文＋日期戳（parking 行的完整面孔）
			note := r.ParkNote
			if note == "" {
				note = "（无原因记录）"
			}
			line += fmt.Sprintf("\t暂缓：%s", note)
			if r.ReviewAfter > 0 {
				line += fmt.Sprintf("\t复查期至 %s", time.Unix(r.ReviewAfter, 0).Local().Format("01-02 15:04"))
			}
		}
		if r.CreatedBy != "" {
			line += "\t" + r.CreatedBy
		}
		fmt.Println(line)
	}
	return 0
}

// runReqPark drives POST /p/{key}/reqs/{id}/park|unpark (r_31): the
// orchestrator's stocking verb from the CLI. --note is the 可核对的
// 等待条件 (park 必填，三不准在服务面校验)；--review-hours 可选复查期
// （巡检提醒用——不自动解禁）。
func runReqPark(args []string, park bool) int {
	cmd := "req " + map[bool]string{true: "park", false: "unpark"}[park]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "default", "project key")
	note := fs.String("note", "", "暂缓原因（可核对的等待条件，如「等房主建第二项目」）")
	reviewHours := fs.Int("review-hours", 0, "复查期小时数（巡检提醒用，0=不设）")
	pos, err := parseTaskFlags(fs, tf, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintf(os.Stderr, "%s: id required, e.g. %s r_01 --note ...\n", cmd, cmd)
		return 2
	}
	if park && strings.TrimSpace(*note) == "" {
		fmt.Fprintf(os.Stderr, "%s: --note 必填（暂缓必须带因——可核对的等待条件，不是观点）\n", cmd)
		return 2
	}
	port, ok := tf.resolvePort(cmd)
	if !ok {
		return 1
	}
	verb := map[bool]string{true: "park", false: "unpark"}[park]
	var payload io.Reader
	if park {
		body, _ := json.Marshal(map[string]any{"note": *note, "review_hours": *reviewHours})
		payload = bytes.NewReader(body)
	}
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/reqs/%s/%s", port, *project, pos[0], verb),
		"application/json", payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, cmd+":", err)
		return 1
	}
	defer resp.Body.Close()
	var out struct {
		Project string           `json:"project"`
		Req     requirements.Req `json:"req"`
		Error   string           `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "%s: %s\n", cmd, orStr(out.Error, resp.Status))
		return 1
	}
	fmt.Printf("[%s] %s %s（%s）\n", out.Req.ID, out.Req.Status, out.Req.Title, out.Project)
	return 0
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// runReqCreate sends req_create over WS, dialing into the project's
// room (hello.project) under the filer's own name.
func runReqCreate(args []string) int {
	fs := flag.NewFlagSet("req create", flag.ContinueOnError)
	tf := &taskFlags{}
	project := fs.String("project", "", "project key the requirement targets (required)")
	title := fs.String("title", "", "requirement title (required, ≤120 chars)")
	body := fs.String("body", "", "requirement body (验收标准：自动化测试或明确的检查步骤)")
	if _, err := parseTaskFlags(fs, tf, args); err != nil {
		return 2
	}
	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(os.Stderr, "req create: --title required")
		return 2
	}
	if *project == "" {
		fmt.Fprintln(os.Stderr, "req create: --project required")
		return 2
	}
	if tf.name == "" {
		return failNoName("req create")
	}
	frame := chat.Message{Type: chat.MsgReqCreate,
		Req: (&requirements.Req{Title: strings.TrimSpace(*title), Body: *body, ProjectKey: *project}).Wire()}
	return runReqWrite(tf, "req create", frame, *project)
}

// runReqWrite performs one req WS write and waits for the private
// "req" reply. Exits non-zero on denial.
func runReqWrite(tf *taskFlags, cmd string, frame chat.Message, project string) int {
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
		if m.Type != chat.MsgReq || m.From != me.Name {
			continue // the room's concurrent traffic
		}
		if tf.json {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
		}
		if m.Event == "denied" {
			if !tf.json {
				fmt.Fprintf(os.Stderr, "%s: 被拒：%s\n", cmd, m.Text)
			}
			taskBye(ctx, conn)
			return 1
		}
		if !tf.json {
			id := ""
			if m.Req != nil {
				id = m.Req.ID
			}
			fmt.Printf("[%s] %s（%s）\n", me.Name, m.Text, id)
		}
		taskBye(ctx, conn)
		return 0
	}
}
