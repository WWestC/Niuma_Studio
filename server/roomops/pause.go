package roomops

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// handleProjectPause serves GET/POST /p/{key}/pause: {paused: bool}。
// GET 回显当前态；POST 翻写（幂等——重复写同值只回执，不再广播）。
// 与全智能开关不同，两个方向都不需要确认令牌：暂停只会省不会费，
// 恢复也只是回到用户本来就在跑的日常。
func (rc *Face) HandleProjectPause(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if rc.Stores.StaffStore == nil || rc.Stores.ProjectStore == nil || !rc.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		httputil.WriteJSON(w, map[string]any{
			"project": key,
			"paused":  rc.Stores.StaffStore.SettingsOf(key).Paused,
		})
	case http.MethodPost:
		var body struct {
			Paused *bool `json:"paused"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("载荷缺失"))
		}
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.Paused == nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("载荷无事可做：请带 paused 字段（布尔）"))
			return
		}
		hub := rc.RoomHub(key)
		was := rc.Stores.StaffStore.SettingsOf(key).Paused
		set := rc.Stores.StaffStore.SetPaused(key, *body.Paused)
		// 幂等：同值重写只回执——重复的系统行与广播没有信息量。
		if *body.Paused != was {
			if hub != nil {
				hub.Broadcast(chat.Message{
					Type: chat.MsgPause, Event: pauseEvent(*body.Paused),
					From: rc.Local, TS: time.Now().Unix(),
				})
			}
			if *body.Paused {
				rc.pauseTrail(key, hub, i18n.S("暂停了办公室：对话、会议、巡逻全部停摆，小人定格——恢复前一切保持现状"))
			} else {
				rc.pauseTrail(key, hub, i18n.S("恢复了办公室：停摆的对话与会议按原顺序继续"))
				if rc.Fleet != nil {
					rc.Fleet.ResumeRoom(key) // 车道重踢＋议程窗放行
				}
			}
		}
		httputil.WriteJSON(w, map[string]any{
			"project": key,
			"paused":  set.Paused,
		})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// pauseEvent names the flip for the "pause" broadcast frame.
func pauseEvent(paused bool) string {
	if paused {
		return "paused"
	}
	return "resumed"
}

// pauseTrail lands the recorded history line (event=pause) — the flip
// is history-grade news, not a transient toast; a room without a hub
// (never instantiated) keeps the settings flip alone.
func (rc *Face) pauseTrail(key string, hub *chat.Hub, text string) {
	if hub == nil {
		return
	}
	who := rc.Local
	if who == "" {
		who = i18n.S("房主")
	}
	hub.SystemRecordedEvent("pause", i18n.Sf("%s %s", who, text))
}
