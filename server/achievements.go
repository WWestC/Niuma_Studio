package server

// achievements.go — the achievements' SHELL face (the engine moved to
// server/achv): the event taps the shell's ops feed through, the
// night-light gate, the runners' report HTTP face, and the re-export
// aliases keeping every existing reference (tests included) unchanged.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/achv"
	"github.com/WWestC/Niuma_Studio/util"
)

// Re-export aliases: the domain types live in server/achv now.
type (
	AchievementDef    = achv.AchievementDef
	Event             = achv.Event
	AchieveState      = achv.AchieveState
	AchievementEngine = achv.AchievementEngine
)

var (
	Evaluate             = achv.Evaluate
	DefaultAchievements  = achv.DefaultAchievements
	NewAchievementEngine = achv.NewAchievementEngine
)

// observeAchievements is the server's event tap: one event through
// the engine, then the fired medals' broadcast face (system 行——
// star 泡经 reminder-only 帧由前端 emoter 消费，与服务端一行同拍).
func (s *Server) observeAchievements(hub interface{ System(string) }, ev Event) {
	if s.achieve == nil {
		return
	}
	// night-10 深夜亮灯接线（t_189 遗留）：成就事件流本身就是「办公室
	// 在动」的信号——任何一拍落在 0–6 点（提示音深夜静音同窗）都先过
	// 夜灯门，每夜至多一粒 night-light 事件（C3 night-owl 数的是夜数
	// 不是次数）。门内递归无害：夜灯事件再进本函数时同一夜已被门闩住。
	s.nightLightTap(hub, time.Unix(util.Now(), 0))
	fired := s.achieve.Observe(ev, hub)
	for _, f := range fired {
		if hub != nil {
			who := ""
			if f.PerMember && ev.Member != "" {
				who = ev.Member + " "
			}
			hub.System(i18n.Sf("🏆 %s达成成就「%s」——%s", who, i18n.S(f.Name), i18n.S(f.Text)))
		}
	}
}

// nightLightTap is the night-10 (C3 night-owl) emitter's gate: one
// night-light event per local deep-night (0–6 点), studio-wide and
// passive — the office was lit, nobody is named. In-memory gate: a
// restart inside the same deep-night may count that night twice —
// flavor-grade drift, accepted (persisting the gate buys a medal
// nothing). Say/report lines tap the same gate via the hub OnSay
// feed (wireNightTap) so a chat-only night also counts as 亮灯.
func (s *Server) nightLightTap(hub interface{ System(string) }, at time.Time) {
	if hr := at.Hour(); hr < 0 || hr >= 6 {
		return
	}
	night := at.Format("2006-01-02")
	s.nightMu.Lock()
	if s.nightLast == night {
		s.nightMu.Unlock()
		return
	}
	s.nightLast = night
	s.nightMu.Unlock()
	s.observeAchievements(hub, Event{Kind: "night-light", At: at.Unix()})
}

// handleTestsGreen is the runners' one-line report face (r_18 t_187):
//
//	curl -X POST localhost:7777/achieve/tests-green -d '{"count":102}'
//
// 工作室测试都是成员跑的——跑完顺手报数，无 CI 依赖。GET 回当前达成
// 快照（荣誉墙前端的数据面同源）。
func (s *Server) handleTestsGreen(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if s.achieve == nil {
			writeJSON(w, map[string]any{"unlocked": map[string]int64{}, "achievements": []map[string]any{}})
			return
		}
		writeJSON(w, s.achieve.WallSnapshot())
		return
	}
	var body struct {
		Count int `json:"count"`
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
	if s.achieve == nil {
		writeJSON(w, map[string]any{"fired": 0})
		return
	}
	// 大厅 hub（全室事件的广播面）
	hub := s.planHub(chat.LobbyKey)
	fired := s.achieve.Observe(Event{
		Kind: "tests-green", TestCount: body.Count, At: time.Now().Unix(),
	}, hub)
	for _, f := range fired {
		if hub != nil {
			hub.System(i18n.Sf("🏆 达成成就「%s」——%s", i18n.S(f.Name), i18n.S(f.Text)))
		}
	}
	writeJSON(w, map[string]any{"fired": len(fired)})
}
