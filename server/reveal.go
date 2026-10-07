package server

// reveal.go — ZCode 侧栏即时刷新（设置窗·连接面板的开关）: the
// desktop-reveal nudge's runtime face. The nudge itself lives in
// zcode/taskindex.go (why the desktop's event-driven list cache needs
// one, which channel each platform rides); the MODE — off / link /
// auto — is persisted here (reveal.json under the v2 root, the same
// discipline as retention.json) and served at GET/POST /reveal.
//
// GET answers what the process does RIGHT NOW: {mode, on, darwin} —
// on is RevealDelivers' verdict for this platform, so the switch can
// never disagree with the next nudge; darwin lets the row's wording
// be platform-honest (the macOS trust-dialog cost only exists there).
//
// POST {on: bool} writes the mode (on → link on darwin — the only
// clean channel there — auto elsewhere; off → "off"), swaps the
// in-process mirror in the same breath (next delivery obeys, no
// restart), and — the healing half — when delivery just turned ON,
// fires the wired RevealIndexWorkspaces nudge ONCE: the pinned rows
// are usually already back in the desktop's index (the restart's fold
// was re-pinned by boot backfill seconds later); what's missing is
// only the nudge that makes the desktop re-read. Toggling the switch
// on therefore brings the missing employees back on screen at once —
// the exact complaint the switch exists to answer. Without a registry
// root (in-memory tests, bare embeds) the whole face 404s — same
// discipline as /retention.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// revealPath is the setting's slot under the v2 root.
func (s *Server) revealPath() string {
	return filepath.Join(s.opts.Registry.Root(), "reveal.json")
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	if s.opts.Registry == nil || s.opts.Registry.Root() == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		mode := zcode.RevealMode()
		writeJSON(w, map[string]any{
			"mode":   mode,
			"on":     zcode.RevealDelivers(mode, runtime.GOOS),
			"darwin": runtime.GOOS == "darwin",
		})
	case http.MethodPost:
		var body struct {
			On *bool `json:"on"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("载荷缺失"))
		}
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.On == nil {
			writeJSONErr(w, http.StatusBadRequest, i18n.S("载荷需要 on（布尔）"))
			return
		}
		mode := "off"
		if *body.On {
			if runtime.GOOS == "darwin" {
				mode = "link" // macOS 唯一干净的通道（代价：≥3.14 每次投递弹信任框）
			} else {
				mode = "auto" // 其余平台默认就是投递（第二实例，零弹窗）
			}
		}
		if err := zcode.SaveRevealMode(s.revealPath(), mode); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("设置写入失败: %s", err.Error()))
			return
		}
		zcode.SetRevealOverride(mode) // 落盘与换镜一口气——下一次投递立刻按新模式走
		if *body.On && s.opts.Fleet != nil {
			s.opts.Fleet.RevealIndexWorkspaces() // 治疗性强投：把已在库里置顶的行当场拉回屏幕
		}
		writeJSON(w, map[string]any{
			"mode":   mode,
			"on":     zcode.RevealDelivers(mode, runtime.GOOS),
			"darwin": runtime.GOOS == "darwin",
		})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}
