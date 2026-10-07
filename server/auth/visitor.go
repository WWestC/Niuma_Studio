package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/util"
)

// visitor limits（定稿 §三；r_12 旋钮族优先级链——env 兜底留待旋钮
// 接线，此处内置默认 + staffing 键可调的读取面）。
const (
	VisitorMaxConcurrent = 5 // 同项目在线访客帽
	VisitorMaxTTL        = 4 * time.Hour
	VisitorReconnectWin  = 10 // 每分钟重连帽
	VisitorReplay        = 50 // 历史回放条数帽
)

// VisitorGate is the per-server toggle+token state (owner-exclusive
// write via the HTTP face; reads are hot).
type VisitorGate struct {
	mu    sync.RWMutex
	on    bool
	token string // 16 字节随机 hex（32 字符）；关闭即废、重开换新
	since int64  // 当前 token 的签发时刻
}

// Toggle flips the visitor channel: on → mint a fresh token (旧的永不
// 复活); off → invalidate (历史链接全死). Returns the (token, on) for
// the receipt + the 留痕文案 the caller broadcasts.
func (g *VisitorGate) Toggle(on bool) (string, bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if on == g.on {
		return g.token, g.on, "" // 无翻转无文案
	}
	g.on = on
	if on {
		buf := make([]byte, 16)
		// 忽略错误会让坏熵源铸出全零常量令牌（安全核查修复）——fail-hard。
		if _, err := rand.Read(buf); err != nil {
			panic(fmt.Sprintf("visitor token: crypto/rand 不可用：%v", err))
		}
		g.token = hex.EncodeToString(buf)
		g.since = util.Now()
		return g.token, true, i18n.S("房主开启了访客通道（链接已生成）")
	}
	g.token = ""
	return "", false, i18n.S("房主关闭了访客通道（旧链接全部失效）")
}

// Check validates one access: off or wrong token → ok=false (caller
// answers 404 — 通道存在性不确认，定稿 §一). The compare rides
// subtle.ConstantTimeCompare — the owner credential's discipline
// (session_mirror): no byte-early-exit on a token guess.
func (g *VisitorGate) Check(token string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.on && token != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(g.token)) == 1
}

// On reports the switch state (the settings pane's read face).
func (g *VisitorGate) On() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.on
}

// visitorMeter is the per-project rate limiter: concurrent count +
// reconnect window (sliding minute).
type VisitorMeter struct {
	mu        sync.Mutex
	conns     map[string]int     // project → live count
	reconnect map[string][]int64 // project → connect ts in the last minute
}

func NewVisitorMeter() *VisitorMeter {
	return &VisitorMeter{conns: map[string]int{}, reconnect: map[string][]int64{}}
}

// admit enforces the two connection-time limits (定稿 §三). ok=false
// carries the HTTP status + friendly line.
func (m *VisitorMeter) Admit(project string) (ok bool, status int, line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conns[project] >= VisitorMaxConcurrent {
		return false, http.StatusServiceUnavailable, i18n.S("当前访客较多，请稍后再来")
	}
	now := util.Now()
	win := m.reconnect[project]
	kept := win[:0]
	for _, ts := range win {
		if now-ts < 60 {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= VisitorReconnectWin {
		m.reconnect[project] = kept
		return false, http.StatusTooManyRequests, i18n.S("连接过于频繁，请稍后再试")
	}
	m.reconnect[project] = append(kept, now)
	m.conns[project]++
	return true, 0, ""
}

// release is the disconnect leg (defer pair of admit).
func (m *VisitorMeter) Release(project string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conns[project] > 0 {
		m.conns[project]--
	}
}

// visitorCount is the 房主 GUI 的 👁 N 数据面（observers 计数的
// 外围快照——按项目聚合）。
func (m *VisitorMeter) count(project string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conns[project]
}

// handleView is the /view/{project} route: token-gated visitor page
// (same shell as the main app — the visitor mode is a frontend init
// branch, not a separate bundle, 定稿 §六·1). No project → 404; wrong
// token → 404 (never 403 — the channel's existence is not confirmed).
func (au *Face) HandleView(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if project == "" {
		http.NotFound(w, r)
		return
	}
	if !au.Visitor.Check(r.URL.Query().Get("t")) {
		http.NotFound(w, r)
		return
	}
	// 页面与主应用同壳同缓存（定稿 §六·1：不另出 bundle）：回主 index，
	// 访客态由前端按 /view URL 前缀自判初始化分支
	if au.WebFS != nil {
		data, err := fs.ReadFile(au.WebFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data)
		return
	}
	http.NotFound(w, r)
}

// handleVisitorToggle is the owner-exclusive write face: POST /visitor
// {on:true, confirm:"VISITOR"} flips the gate (both directions leave
// a system line in the lobby — 定稿 §一 留痕). GET returns {on, count}
// (the settings pane's read face + the 👁 N data).
func (au *Face) HandleVisitorToggle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		httputil.WriteJSON(w, map[string]any{
			"on":    au.Visitor.On(),
			"count": au.Visitors.count(chat.LobbyKey),
		})
		return
	case http.MethodPost:
		var body struct {
			On      bool   `json:"on"`
			Confirm string `json:"confirm"`
		}
		if err := readJSONBody(r, &body); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		// 开启需确认令牌（与 autopilot 同纪律——对外暴露面）；关闭免确认
		if body.On && body.Confirm != "VISITOR" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("开启访客通道需确认令牌 confirm=VISITOR——对外暴露只读面，请从前端设置卡的对话框发起"))
			return
		}
		token, on, line := au.Visitor.Toggle(body.On)
		if line != "" && au.Hub != nil {
			au.Hub.System(line)
		}
		httputil.WriteJSON(w, map[string]any{"on": on, "token": token})
		return
	}
	httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "GET/POST only")
}

// visitorFirstOnce broadcasts「窗外有人驻足观看」once per token epoch
// (重开通道 = 新 token = 新的一次；纯匿名，不显身份).
func (au *Face) VisitorFirstOnce(project string) {
	au.Visitor.mu.RLock()
	epoch := au.Visitor.token
	au.Visitor.mu.RUnlock()
	if epoch == "" {
		return
	}
	if au.FirstSeen.FireOnce(epoch) && au.Hub != nil {
		au.Hub.System(i18n.S("窗外有人驻足观看"))
	}
}

func readJSONBody(r *http.Request, v any) error { return httputil.ReadJSONBody(r, v) }

// firstVisitTracker is the 彩蛋去重（token epoch → 已播）：同 token 的
// 重连不重播；重开通道（新 token）是新的一次。
type FirstVisitTracker struct {
	mu     sync.Mutex
	epochs map[string]bool
}

func NewFirstVisitTracker() *FirstVisitTracker {
	return &FirstVisitTracker{epochs: map[string]bool{}}
}

// fireOnce reports whether this epoch should broadcast (first sighting).
func (t *FirstVisitTracker) FireOnce(epoch string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.epochs[epoch] {
		return false
	}
	t.epochs[epoch] = true
	return true
}
