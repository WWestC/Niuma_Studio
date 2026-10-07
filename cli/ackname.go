package cli

// ackname.go — 成员身份反查：`niuma ack` 在成员自己的回合里跑，操作者
// 是成员而非房主，detectLocalName（房主探测）在这里是错的身份源。本文件
// 从房间的真实台账反推「谁在跑这条命令」：
//
//   1. ~/.niuma/ws-projects.json 把 cwd 绑到项目 key；
//   2. ~/.niuma/staffing.json 每行一名成员（含 ZCode session_id）；
//   3. ~/.zcode/cli/db/db.sqlite 的 session.directory 是该会话的出生
//      工作区——cwd 与之相等的 staffing 行就是操作者。
//
// 任一环节缺失都返回 ""（调用方 loud 报错，绝不猜名——身份纪律
// owner-M1 的成员侧同款：the CLI never invents an identity）。SQLite
// 读取走 python3+sqlite3 桥（purge.go 同款缝），不引驱动依赖。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/zcode"
)

// staffingRow is one member row of ~/.niuma/staffing.json.
type staffingRow struct {
	ProjectKey string `json:"project_key"`
	Person     string `json:"person"`
	SessionID  string `json:"session_id"`
}

// staffingMemberForProject resolves the member whose session was born
// in the CURRENT directory and staffs the given project ("" = any).
func staffingMemberForProject(project string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	rows := readStaffing()
	if len(rows) == 0 {
		return ""
	}
	var candidates []string
	for _, r := range rows {
		if r.SessionID == "" || r.Person == "" {
			continue
		}
		if project != "" && r.ProjectKey != project {
			continue
		}
		if dir := sessionDirectory(r.SessionID); dir != "" &&
			filepath.Clean(dir) == filepath.Clean(cwd) {
			candidates = append(candidates, r.Person)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	// Ambiguity (two members share one workspace, e.g. the reset-born
	// book room): the member IN A TURN is the one whose terminal
	// transcript is being written right now — the trace journal is
	// per-member and live. Freshness window 90s.
	if len(candidates) > 1 {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		var live []string
		for _, name := range candidates {
			if terminalFresh(home, project, name, 90*time.Second) {
				live = append(live, name)
			}
		}
		if len(live) == 1 {
			return live[0]
		}
	}
	return "" // 0 or still ambiguous — the caller asks for --name
}

// terminalFresh reports whether the member's trace journal saw a line
// within the window ("" room key scans every project's file).
func terminalFresh(home, project, name string, window time.Duration) bool {
	pattern := filepath.Join(home, ".niuma", "terminal", "*", name+".jsonl")
	if project != "" {
		pattern = filepath.Join(home, ".niuma", "terminal", project, name+".jsonl")
	}
	matches, _ := filepath.Glob(pattern)
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil &&
			time.Since(fi.ModTime()) < window {
			return true
		}
	}
	return false
}

// readStaffing loads the staffing table (nil on any error).
func readStaffing() []staffingRow {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".niuma", "staffing.json"))
	if err != nil {
		return nil
	}
	var rows []staffingRow
	if json.Unmarshal(b, &rows) != nil {
		return nil
	}
	return rows
}

// sessionDirectory reads a session's birth directory from the ZCode
// session store, natively ("" when absent or unreadable).
func sessionDirectory(sessID string) string {
	if strings.TrimSpace(sessID) == "" {
		return ""
	}
	return zcode.CliSessionDirectory(sessID)
}
