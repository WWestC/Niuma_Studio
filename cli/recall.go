package cli

// recall — 一行拉回既有成员（v0.10 seat-M4 / t_85；v2 P5-a 薄壳化）：
//
//	recall --name 小明 [--project proj-x] [--port N]
//
// 处置选型（P5-a 注记）：CLI 改薄壳——统一 POST /dispatch/recall（带
// --project 路由到该项目调度器；不带按编制占用行路由），与看护自动
// 补员、应用端走同一条 dispatcher Recall 路径（Birth 同公式）。
// 唯一保留的旧通道：调度未启用（端点 404）且未指定 --project 时回落
// v1 的 GUI drive（recruit.RecallMember）——「红线不动，绝不无头拉人」
// 的降级通道不因薄壳化而丢失；--project 且调度关闭则明确报错（项目
// 编制重生本就依赖调度器）。
// 退出码 0 成功 / 1 失败 / 2 用法错误。

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/WWestC/Niuma_Studio/recruit"
	"github.com/WWestC/Niuma_Studio/server"
)

func runRecall(args []string) int {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	name := fs.String("name", "", "member to pull back online (required)")
	project := fs.String("project", "", "route through this project's dispatcher (staffing row rebirth)")
	port := fs.Int("port", 0, "server port (default: auto-discover)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "recall: --name 必填（例：recall --name 小明）")
		return 2
	}
	if *port == 0 {
		p, err := server.DiscoverPort()
		if err != nil {
			fmt.Fprintln(os.Stderr, "recall:", err)
			return 1
		}
		*port = p
	}

	body, _ := json.Marshal(map[string]string{"name": *name, "project": *project})
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/dispatch/recall", *port),
		"application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "recall:", err)
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode == http.StatusOK:
		if *project != "" {
			fmt.Printf("已按项目 %s 的编制拉回 %s\n", *project, *name)
		} else {
			fmt.Printf("已拉回 %s（调度器路径，按编制占用行路由）\n", *name)
		}
		return 0
	case resp.StatusCode == http.StatusNotFound && *project == "":
		// dispatch off: the v1 GUI-drive channel is the recall red
		// line's own degradation path — keep it reachable headless of
		// the dispatcher.
		fmt.Println("调度未启用（/dispatch 不可达），回落 GUI drive 通道（v1 路径）")
		if err := recruit.RecallMember(*port, *name); err != nil {
			fmt.Fprintf(os.Stderr, "recall: %v\n", err)
			return 1
		}
		fmt.Printf("已拉回 %s（在座确认）\n", *name)
		return 0
	case resp.StatusCode == http.StatusNotFound:
		fmt.Fprintf(os.Stderr, "recall: 项目 %s 无调度器（不存在、未开张或调度未启用）\n", *project)
	default:
		fmt.Fprintf(os.Stderr, "recall: %s\n", strings.TrimSpace(string(out)))
	}
	return 1
}
