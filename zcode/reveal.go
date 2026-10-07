package zcode

// reveal.go — the sidebar reveal nudge's MODE, promoted from a boot-only
// env knob to a runtime setting (设置窗·连接面板的「ZCode 侧栏即时刷新」
// 开关). The nudge itself (why the desktop's event-driven list cache needs
// one, which channel each platform rides) stays in taskindex.go; this file
// owns only the question "deliver or not, under which channel".
//
// 模式词表与 NIUMA_REVEAL 完全同源，不另造概念：
//
//	"link" — 主动投递，且走 zcode:// 深链（macOS 上唯一干净的通道）
//	"off"  — 完全不投递（行照常落库，等桌面端自然刷新）
//	"auto" — 平台默认：Windows/Linux 投递（第二实例零弹窗），macOS 不投
//	""     — 未表态：落在 env（NIUMA_REVEAL/DH_REVEAL），env 也没设时
//	         等同 "auto"
//
// 真源优先级：设置文件（reveal.json，v2 根下，同 retention.json 的存法）
// > 环境变量 > 平台默认。文件是运行时写入的（POST /reveal），所以进程
// 内以原子覆写镜像一份——reveal 的触发点全在 goroutine 里（出生的
// registerTaskIndexAsync、boot 回填的 4 路并发），锁不起，原子值够用。
// main 在 fleet 起来之前装载（boot 回填的第一次投递就已按设置走），
// POST /reveal 写盘后当场换镜像——下一次投递立刻按新模式执行，无需
// 重启（macOS 开关弹的第一只信任框，就是它刚拉回的置顶行）。

import (
	"encoding/json"
	"os"
	"sync/atomic"

	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/util"
)

// revealOverride mirrors the on-disk mode in-process (atomic.Value
// holds a string; the zero Value reads as "no override" — env governs).
var revealOverride atomic.Value

// SetRevealOverride installs the runtime mode ("" clears back to env).
// Callers: main at boot (loads reveal.json), POST /reveal after saving.
func SetRevealOverride(mode string) { revealOverride.Store(mode) }

// RevealMode is the resolved delivery mode RIGHT NOW (the settings
// mirror > the env var > "" = platform default). Exported for the
// settings face's GET: the switch paints from the same truth the next
// nudge will obey, so the UI can never disagree with the process.
// Within one process the mirror equals the file (main loads it before
// the fleet starts, POST /reveal saves then swaps it in one breath).
func RevealMode() string {
	if v, ok := revealOverride.Load().(string); ok && v != "" {
		return v
	}
	return util.Env("REVEAL")
}

// RevealDelivers answers whether mode actually delivers on goos — the
// ONE truth table shared by the nudge itself (taskindex.go) and the
// settings face's paint (server/reveal.go), so the switch's on-state
// can never drift from what the process really does:
//
//	"off"          → never
//	"link"         → always (the macOS opt-in; elsewhere it just
//	                 means "deliver", the deep link is only the
//	                 fallback channel there)
//	anything else  → platform default: every goos but darwin delivers
//	                 (darwin's both second-instance shapes leave Dock
//	                 tiles — the taskindex.go verdict)
func RevealDelivers(mode, goos string) bool {
	switch mode {
	case "off":
		return false
	case "link":
		return true
	}
	return goos != "darwin"
}

// revealConfigFile is the persisted shape under the v2 root.
type revealConfigFile struct {
	Mode string `json:"mode"`
}

// LoadRevealMode reads the setting ("" = 没表态: env governs). A
// missing or unreadable file is the honest "" — persistence must never
// refuse the office (LoadRetention's discipline); a corrupt mode
// string passes through as-is, same tolerance the env always had.
func LoadRevealMode(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg revealConfigFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		return ""
	}
	return cfg.Mode
}

// SaveRevealMode persists the mode atomically (tmp + rename — the same
// discipline every ledger write uses). The caller validates the mode.
func SaveRevealMode(path, mode string) error {
	b, err := json.Marshal(revealConfigFile{Mode: mode})
	if err != nil {
		return err
	}
	return persist.Save(path, b, 0o600)
}
