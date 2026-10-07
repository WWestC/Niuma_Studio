package cli

// dispatch — the v1.0 member dispatcher's management face (HTTP
// against the running room):
//
//	dispatch status                    # managed members: tri-state, queues
//	dispatch birth --name 小策 --role dev [--prompt ...] [--model ...]
//	        [--project book]           # target project's dispatcher
//	dispatch bind --name 小策 --session sess_... [--project book]
//	dispatch steer --name 小策 --text "方向修正" [--project book]
//
// birth creates the member's ZCode session through the dispatcher
// (the room process drives it; the prompt defaults to the stored
// config's onboarding prompt). birth/bind/steer take the member from
// parseCommon's --name/--role — they must NOT define those flags
// again (a second definition panics the FlagSet, t_招人 fix).
//
// --project rides pickScope (v2.9): an explicit key wins, else the
// workspace's bound project, and the fleet's HTTP face routes the op
// to that project's dispatcher. An empty scope omits the field, so
// legacy callers keep the lobby exactly as before — a birth run from
// an unbound cwd cannot silently land a hire in the wrong office.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/WWestC/Niuma_Studio/server"
)

func runDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: dispatch <status|birth|bind|steer> [flags]")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return dispatchStatus(rest)
	case "birth":
		fs := flag.NewFlagSet("dispatch birth", flag.ContinueOnError)
		c := parseCommon(fs) // --name/--role are the newborn's identity
		prompt := fs.String("prompt", "", "onboarding prompt (default: the stored config's)")
		model := fs.String("model", "", "birth-time model pick (providerId/modelId or bare id)")
		reasoning := fs.String("reasoning", "", "thinking intensity (the CLI's reasoningLevel, e.g. low/high/max; empty = the model's default tier; rides the default-chain model too when --model is empty)")
		project := fs.String("project", "", "the project whose dispatcher drives the birth (default: the workspace's bound project; empty = the lobby)")
		post := fs.String("post", "", "establishment post key (岗位锚): anchors the staffing row to the table; with no --role the 身份 resolves from the table row itself")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		return dispatchPost(c, "birth", map[string]string{
			"name": c.name, "role": c.role, "prompt": *prompt,
			"model": *model, "reasoning": *reasoning, "post": *post,
			"project": pickScope(*project)})
	case "bind":
		fs := flag.NewFlagSet("dispatch bind", flag.ContinueOnError)
		c := parseCommon(fs)
		session := fs.String("session", "", "ZCode session id (sess_...; empty unbinds)")
		project := fs.String("project", "", "the project whose dispatcher holds the member (default: the workspace's bound project; empty = the lobby)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		return dispatchPost(c, "bind", map[string]string{
			"name": c.name, "session": *session, "project": pickScope(*project)})
	case "steer":
		fs := flag.NewFlagSet("dispatch steer", flag.ContinueOnError)
		c := parseCommon(fs)
		text := fs.String("text", "", "steer text (required)")
		project := fs.String("project", "", "the project whose dispatcher drives the member (default: the workspace's bound project; empty = the lobby)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		return dispatchPost(c, "steer", map[string]string{
			"name": c.name, "text": *text, "project": pickScope(*project)})
	default:
		fmt.Fprintf(os.Stderr, "dispatch: unknown subcommand %q\n", sub)
		return 2
	}
}

func dispatchPort(c *common) int {
	if c.port != 0 {
		return c.port
	}
	p, err := server.DiscoverPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
		return 0
	}
	return p
}

func dispatchStatus(args []string) int {
	fs := flag.NewFlagSet("dispatch status", flag.ContinueOnError)
	c := parseCommon(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	port := dispatchPort(c)
	if port == 0 {
		return 1
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/dispatch", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch status:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintln(os.Stderr, "dispatch status: 办公室未启用调度器（--no-dispatch 或 ZCode 不可用）")
		return 1
	}
	var rows []struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
		Queued    int    `json:"queued"`
		Inject    int    `json:"inject"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		fmt.Fprintln(os.Stderr, "dispatch status:", err)
		return 1
	}
	if len(rows) == 0 {
		fmt.Println("（调度器在线，暂无受管成员——dispatch birth --name ... 出生第一个）")
		return 0
	}
	fmt.Printf("%-14s %-10s %-9s %5s %7s  %s\n", "成员", "岗位", "状态", "排队", "背景", "会话")
	for _, r := range rows {
		sid := r.SessionID
		if len(sid) > 18 {
			sid = sid[:18] + "…"
		}
		fmt.Printf("%-14s %-10s %-9s %5d %7d  %s\n",
			r.Name, r.Role, r.Status, r.Queued, r.Inject, sid)
	}
	return 0
}

func dispatchPost(c *common, op string, body map[string]string) int {
	if body["name"] == "" {
		fmt.Fprintln(os.Stderr, "dispatch "+op+": --name 必填")
		return 2
	}
	if op == "steer" && body["text"] == "" {
		fmt.Fprintln(os.Stderr, "dispatch steer: --text 必填")
		return 2
	}
	port := dispatchPort(c)
	if port == 0 {
		return 1
	}
	for k, v := range body {
		if v == "" {
			delete(body, k)
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch "+op+":", err)
		return 1
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/dispatch/%s", port, op),
		"application/json", bytes.NewReader(b))
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch "+op+":", err)
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	text := string(out)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "dispatch %s: %s\n", op, trimNL(text))
		return 1
	}
	fmt.Println(trimNL(text))
	return 0
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
