package server

// sendwait.go — 消息注入等待: how long the dispatcher waits for a
// member session/send's ack before declaring the result unknown,
// dropping the line and telling the room owner (the 「注入超时」
// notice). Served at GET/POST /dispatch-wait — the settings card's
// 「智能」分节 reads it and writes it. The policy (sendwait.json under
// the v2 root, seconds, default 300 = 5 分钟) applies live: the POST
// face calls SendWaitSink (main wires it to Fleet.SetSendWait), so the
// next deliver reads the new value with no restart. A nil sink still
// persists — dispatch off (--no-dispatch) or an embedded server keeps
// the setting for the next boot that has a fleet. Without a registry
// root (in-memory tests, bare embeds) the whole face 404s — same
// discipline as /retention.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
)

const (
	// SendWaitDefaultSeconds mirrors dispatch.DefaultSendWait (5
	// minutes) — the value a missing or corrupt policy file falls back
	// to. Kept as a local twin so the server package stays free of the
	// dispatch import (the /dispatch face stays an opaque http.Handler
	// by design); main.go is where both constants meet — it applies
	// this file's value onto the fleet at boot.
	SendWaitDefaultSeconds = 300
	// SendWaitMinSeconds bounds the setting from below: under ~10s a
	// merely-busy app-server's ack starts looking like a death, and
	// the 结果未知 drop fires on lines that actually landed.
	SendWaitMinSeconds = 10
	// SendWaitMaxSeconds bounds it from above: past an hour, the lane
	// a dead session blocks stops being a lane.
	SendWaitMaxSeconds = 3600
)

// SendWaitPath is the policy file's slot under a v2 root (shared by
// the handler and main.go's boot apply, so the filename is said once).
func SendWaitPath(root string) string {
	return filepath.Join(root, "sendwait.json")
}

// LoadSendWait reads the persisted injection-ack wait in seconds
// (missing/corrupt/out-of-range file = the default). Exported for
// main.go's boot apply onto the fleet.
func LoadSendWait(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return SendWaitDefaultSeconds
	}
	var cfg struct {
		Seconds int `json:"seconds"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil ||
		cfg.Seconds < SendWaitMinSeconds || cfg.Seconds > SendWaitMaxSeconds {
		return SendWaitDefaultSeconds
	}
	return cfg.Seconds
}

// SaveSendWait persists the policy atomically (tmp + rename — every
// ledger write's discipline). The caller validates seconds.
func SaveSendWait(path string, seconds int) error {
	b, err := json.Marshal(struct {
		Seconds int `json:"seconds"`
	}{seconds})
	if err != nil {
		return err
	}
	return persist.Save(path, b, 0o600)
}

func (s *Server) handleSendWait(w http.ResponseWriter, r *http.Request) {
	if s.opts.Registry == nil || s.opts.Registry.Root() == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"seconds":         LoadSendWait(SendWaitPath(s.opts.Registry.Root())),
			"default_seconds": SendWaitDefaultSeconds,
			"min_seconds":     SendWaitMinSeconds,
			"max_seconds":     SendWaitMaxSeconds,
		})
	case http.MethodPost:
		var body struct {
			Seconds int `json:"seconds"`
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
		if body.Seconds < SendWaitMinSeconds || body.Seconds > SendWaitMaxSeconds {
			writeJSONErr(w, http.StatusBadRequest,
				i18n.S("seconds 需为 10–3600 秒（更久建议排查会话健康，而不是等更久）"))
			return
		}
		if err := SaveSendWait(SendWaitPath(s.opts.Registry.Root()), body.Seconds); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("注入等待写入失败: %s", err.Error()))
			return
		}
		if s.opts.Fleet != nil {
			s.opts.Fleet.SendWait(time.Duration(body.Seconds) * time.Second)
		}
		writeJSON(w, map[string]any{"seconds": body.Seconds})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}
