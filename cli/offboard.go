package cli

// offboard — 离职五步流水线的薄壳（v2 P5-a）：
//
//	offboard --name 小明 [--project proj-x] [--as HR] [--reason "…"]
//	         [--reassign-to 小红] [--force] [--dry-run] [--json]
//	         [--timeout 30s] [--port N]
//
// 五步编排（预检→项目内通告→kick→staffing offboard＋left_ts→room-log
// 台账，全局归档视所有编制行而定）整体住服务端 POST /offboard——
// 一处权威：时序、--project 范围、在办拦截与归档条件只有引擎侧一份。
// 本命令只传参与渲染 ✓/✗/△ 步骤日志（v0.8 M2 起客户端自己走 WS 帧
// 时序的形态已删除，不缝缝补补）。退出码 0 成功 / 1 失败或预检拦截 /
// 2 用法错误。

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

	"github.com/WWestC/Niuma_Studio/server"
)

func runOffboard(args []string) int {
	fs := flag.NewFlagSet("offboard", flag.ContinueOnError)
	target := fs.String("name", "", "member leaving the room (required)")
	project := fs.String("project", "", "scope the departure to this project's room/staffing/ledger (default: the lobby)")
	as := fs.String("as", "", "operator identity (default: auto-detect the local host)")
	reason := fs.String("reason", "", "departure reason, quoted in the notice and ledger line")
	reassignTo := fs.String("reassign-to", "", "hand all in-progress tasks to this member")
	force := fs.Bool("force", false, "leave with in-progress tasks unassigned (skips the reassignment precheck only)")
	dryRun := fs.Bool("dry-run", false, "server prints every step it would run, executes nothing")
	asJSON := fs.Bool("json", false, "machine-readable step log (one JSON line per step)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall budget for the server-side pipeline")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*target) == "" {
		fmt.Fprintln(os.Stderr, "offboard: --name 目标成员必填（例：offboard --name 小明 [--reassign-to 小红]）")
		return 2
	}
	if *reassignTo != "" && *reassignTo == *target {
		fmt.Fprintln(os.Stderr, "offboard: --reassign-to 不能是离职者本人")
		return 2
	}
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "offboard:", err)
			return 1
		}
		*port = p
	}
	actor := *as
	if actor == "" {
		host, err := detectLocalName(*port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "offboard:", err)
			return 1
		}
		actor = host
	}

	body, err := json.Marshal(map[string]any{
		"name": *target, "project": *project, "as": actor,
		"reason": *reason, "reassign_to": *reassignTo,
		"force": *force, "dry_run": *dryRun,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "offboard:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/offboard", *port), bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "offboard:", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "offboard:", err)
		return 1
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var res struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Step string `json:"step"`
			OK   bool   `json:"ok"`
			Skip bool   `json:"skip,omitempty"`
			Note string `json:"note,omitempty"`
		} `json:"steps"`
		Blocked *struct {
			Reason string `json:"reason"`
			Tasks  []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Title  string `json:"title"`
			} `json:"tasks"`
		} `json:"blocked"`
		Note  string `json:"note,omitempty"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintf(os.Stderr, "offboard: 服务端回执不可解析（HTTP %d）: %v\n", resp.StatusCode, err)
		return 1
	}

	for _, st := range res.Steps {
		state := '✗'
		if st.OK {
			state = '✓'
		} else if st.Skip {
			state = '△'
		}
		if *asJSON {
			b, _ := json.Marshal(st)
			fmt.Println(string(b))
			continue
		}
		fmt.Printf("%c %s%s\n", state, st.Step, detailSuffix(st.Note))
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		if res.Note != "" {
			fmt.Println(res.Note)
		}
		return 0
	case res.Blocked != nil:
		fmt.Fprintf(os.Stderr, "offboard: %s：\n", res.Blocked.Reason)
		for _, t := range res.Blocked.Tasks {
			fmt.Fprintf(os.Stderr, "  %s %s %s\n", t.ID, t.Status, t.Title)
		}
		return 1
	default:
		fmt.Fprintf(os.Stderr, "offboard: %s\n", firstNonEmpty(res.Error, strings.TrimSpace(string(raw))))
		return 1
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func detailSuffix(d string) string {
	if d == "" {
		return ""
	}
	return ": " + d
}
