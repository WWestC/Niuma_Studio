package plans

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/util"
)

// handleProjectReqs serves the project's requirement ledger: GET lists
// {"project", "reqs"}; POST creates from {"title", "body?",
// "created_by?"} and answers {"project", "req"}. Unknown projects 404
// through the project store; a refused Create (empty title, bad key)
// is a 400 carrying the store's reason.
func (pn *Face) HandleProjectReqs(w http.ResponseWriter, r *http.Request) {
	if pn.Stores.Requirements == nil || pn.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !pn.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		httputil.WriteJSON(w, map[string]any{
			"project": key,
			"reqs":    pn.Stores.Requirements.ListByProject(pn.Subject, key),
		})
	case http.MethodPost:
		var body struct {
			Title     string `json:"title"`
			Body      string `json:"body"`
			CreatedBy string `json:"created_by"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("需求载荷缺失"))
		}
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("需求载荷解析失败: %s", err))
			return
		}
		if body.CreatedBy == "" {
			body.CreatedBy = pn.Local
		}
		req, err := pn.Stores.Requirements.Create(pn.Subject, key, body.Title, body.Body, body.CreatedBy)
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if hub := pn.RoomHub(key); hub != nil {
			hub.Broadcast(reqCreatedBroadcast(req.CreatedBy, &req))
		}
		httputil.WriteJSON(w, map[string]any{"project": key, "req": req})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// handleProjectReqDelete serves DELETE /p/{key}/reqs/{id} — the
// ledger's cleanup face for mistaken open entries, same authority as
// the POST (the loopback host face: the local caller IS the host; no
// body — provenance is the host). The store refuses anything not open
// (split mothers a task tree, closed is the terminal record) and the
// handler refuses cross-project ids (the plan-submit rule: a global
// r_NN must not leave through another project's door); refusals are
// 400s carrying the reason. Success broadcasts one req{deleted} frame
// in the project room (t_170 声色同门 — the green-dot door hears it
// like any task mutation) and answers {"project", "req"} with the
// removed snapshot.
func (pn *Face) HandleProjectReqDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE only", http.StatusMethodNotAllowed)
		return
	}
	if pn.Stores.Requirements == nil || pn.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !pn.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	// 删除不可回滚：跨项目的 id 必须在动手前拦下（Delete 只认架上门
	// 牌，先核归属再放行——v2.10 号段分家后 GetIn 即归属核）
	if cur, ok := pn.Stores.Requirements.GetIn(pn.Subject, key, id); !ok {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("需求不存在: %s（项目 %s）", id, key))
		return
	} else if cur.ProjectKey != key {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("需求 %s 属于项目 %s，不能从 %s 的台账删除", id, cur.ProjectKey, key))
		return
	}
	req, err := pn.Stores.Requirements.Delete(pn.Subject, key, id)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if hub := pn.RoomHub(key); hub != nil {
		hub.Broadcast(reqDeletedBroadcast(pn.Local, &req))
	}
	httputil.WriteJSON(w, map[string]any{"project": key, "req": req})
}

// handleProjectReqPark serves POST /p/{key}/reqs/{id}/park (r_31) —
// the orchestrator's stocking verb: freeze an open requirement onto the
// parked shelf (拆解钟/会议钟不可见、水位合计可见). Authority follows
// the park ruling: 编排者单方（排期职权内）＋房主 override 说了算且
// 系统行留痕不追责. The note passes the 三不准 discipline (不写价值评
// 判/不写不可核对的模糊条件/不夹带情绪——「parking 原因应是一个可核对
// 的等待条件，不是一个观点」)；the store only stores. Body:
// {"note": "...", "review_hours": N} (both optional; review_after =
// now + review_hours, 巡检提醒用——不自动解禁).
func (pn *Face) HandleProjectReqPark(w http.ResponseWriter, r *http.Request) {
	pn.reqParkOp(w, r, true)
}

// handleProjectReqUnpark serves POST /p/{key}/reqs/{id}/unpark (r_31):
// thaw back to open (编排决策可追溯——park 原因保留在需求行上).
func (pn *Face) HandleProjectReqUnpark(w http.ResponseWriter, r *http.Request) {
	pn.reqParkOp(w, r, false)
}

func (pn *Face) reqParkOp(w http.ResponseWriter, r *http.Request, park bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if pn.Stores.Requirements == nil || pn.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !pn.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	cur, ok := pn.Stores.Requirements.GetIn(pn.Subject, key, id)
	if !ok {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("需求不存在: %s（项目 %s）", id, key))
		return
	}
	if cur.ProjectKey != key {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("需求 %s 属于项目 %s，不能从 %s 的台账操作", id, cur.ProjectKey, key))
		return
	}
	// 权限（r_31 裁定）：park/unpark 编排者单方；房主 override 说了算
	// （留痕不追责）。回环 host 面：本机调用者即房主——与 DELETE 同
	// 通道同纪律（服务端相信回环，远端本就不可达）。
	by := pn.Local
	note := ""
	reviewHours := 0
	if park {
		var body struct {
			Note        string `json:"note"`
			ReviewHours int    `json:"review_hours"`
		}
		if err := httputil.ReadJSONBody(r, &body); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %v", err))
			return
		}
		note = strings.TrimSpace(body.Note)
		if note == "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("暂缓必须带因（可核对的等待条件，如「等房主建第二项目」）——不写观点不写情绪"))
			return
		}
		// 三不准的机器面（价值评判/模糊条件）：只做关键词兜底，语义
		// 判断归编排者纪律——机器不装懂
		for _, banned := range []string{"不值得", "没意义", "不想做", "等氛围", "看情况"} {
			if strings.Contains(note, banned) {
				httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("暂缓原因含禁词「%s」——parking 是时机未到不是不值得做（不值得走删除或当面说）；原因应是可核对的等待条件", banned))
				return
			}
		}
		reviewHours = body.ReviewHours
	}
	var req requirements.Req
	var err error
	if park {
		reviewAfter := int64(0)
		if reviewHours > 0 {
			reviewAfter = util.Now() + int64(reviewHours)*3600
		}
		req, err = pn.Stores.Requirements.Park(pn.Subject, key, id, note, reviewAfter)
	} else {
		req, err = pn.Stores.Requirements.Unpark(pn.Subject, key, id)
	}
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hub := pn.RoomHub(key)
	if hub != nil {
		if park {
			hub.SystemRecorded(i18n.Sf("【需求】%s 将 %s「%s」转入暂缓（%s）——拆解钟与会议钟不再捞起，水位合计仍计", by, id, req.Title, note))
			pn.Chronicle.OnReqPark(key, id, req.Title, note, by)
		} else {
			hub.SystemRecorded(i18n.Sf("【需求】%s 解除 %s「%s」的暂缓——回 open 可拆解", by, id, req.Title))
			pn.Chronicle.OnReqUnpark(key, id, req.Title, by)
		}
	}
	httputil.WriteJSON(w, map[string]any{"project": key, "req": req})
}

// reqCreateAs is the WS req_create verb's landing (全智能模式 v2.8):
// the orchestrator (or the host) files a requirement from inside the
// project room — the autopilot poke's instructed path. The channel is
// orchestrator-only (isOrchestrator: the host may file on anyone's
// behalf) and room-bound (the plan-submit rule: a requirement lands
// in the filer's OWN office). The reply is PRIVATE to the requester;
// the room hears one req{created} broadcast (t_170) — the same
// snapshot the task family broadcasts, so the web's green-dot door
// (and the filer's own say line) carry the news.
func (pn *Face) ReqCreateAs(hub *chat.Hub, actor string, payload *requirements.Req, room string) chat.Message {
	if pn.Stores.Requirements == nil {
		return reqReply(actor, "denied", nil, i18n.S("需求系统未启用"))
	}
	if !pn.isOrchestrator(actor) {
		return reqReply(actor, "denied", nil, i18n.Sf("立案只能由编排者（岗位「%s」）或房主发起", agents.OrchestratorRole))
	}
	key := payload.ProjectKey
	if key == "" {
		key = room // convenience: the room the filer sits in
	}
	if key != room {
		return reqReply(actor, "denied", nil, i18n.Sf("需求只能立进本办公室项目（当前办公室 %s，载荷写的是 %s）", room, key))
	}
	req, err := pn.Stores.Requirements.Create(pn.Subject, key, payload.Title, payload.Body, actor)
	if err != nil {
		return reqReply(actor, "denied", nil, err.Error())
	}
	hub.Broadcast(reqCreatedBroadcast(actor, &req))
	return reqReply(actor, "created", &req,
		i18n.Sf("%s 立了需求 %s「%s」（open，等待评审会拆解）", actor, req.ID, req.Title))
}

// reqCreatedBroadcast is the room-facing leg of a freshly filed
// requirement: one req{created} frame with the full snapshot — the
// green-dot door's wire contract (web chat/rooms.js treats it exactly
// like a task mutation: dot + ding behind one door).
func reqCreatedBroadcast(actor string, req *requirements.Req) chat.Message {
	return chat.Message{Type: chat.MsgReq, Event: "created", Req: req.Wire(), From: actor,
		Text: i18n.Sf("%s 立了需求 %s「%s」（open，等待评审会拆解）", actor, req.ID, req.Title),
		TS:   time.Now().Unix()}
}

// reqDeletedBroadcast is the room-facing leg of a removed requirement:
// one req{deleted} frame with the removed snapshot — the same door the
// created frame rings (green dot + ding), so every viewer's room knows
// the entry left the ledger before their next GET.
func reqDeletedBroadcast(actor string, req *requirements.Req) chat.Message {
	return chat.Message{Type: chat.MsgReq, Event: "deleted", Req: req.Wire(), From: actor,
		Text: i18n.Sf("%s 删了需求 %s「%s」（open 误录清理，未拆解无牵连）", actor, req.ID, req.Title),
		TS:   time.Now().Unix()}
}

// reqReply builds the private S->C "req" frame.
func reqReply(actor, event string, req *requirements.Req, text string) chat.Message {
	return chat.Message{Type: chat.MsgReq, Event: event, Req: req.Wire(),
		From: actor, Text: text, TS: time.Now().Unix()}
}
