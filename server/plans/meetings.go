package plans

import (
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// handleProjectMeeting serves the meeting room's state (GET
// /p/{key}/meeting): {"project", "meeting" (live or null), "history"}
// — history capped at the 8 most recent finished reviews.
func (pn *Face) HandleProjectMeeting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if pn.Stores.Meetings == nil || pn.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !pn.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	m, live := pn.Stores.Meetings.Active(key)
	var slot any // JSON null when the room is free
	if live {
		slot = m
	}
	httputil.WriteJSON(w, map[string]any{
		"project": key,
		"meeting": slot,
		"history": pn.Stores.Meetings.History(key, 8),
	})
}

// meetingCtlAs applies the chair's meeting_end / meeting_extend verb
// (end=true fires the current window now; else minutes extends it):
// the gate is the live meeting's chair — the convening orchestrator —
// checked here for the loud denial and again in the dispatcher, which
// owns the agenda. Success needs no private receipt: the dispatcher's
// own broadcast ("advanced"/"extended"/"ended") is the answer the
// requester's CLI reads, the plan-verb contract; only the denial is
// private.
func (pn *Face) MeetingCtlAs(hub *chat.Hub, c *chat.Client, actor string, end bool, minutes int) chat.Message {
	deny := func(reason string) chat.Message {
		return chat.Message{Type: chat.MsgMeeting, Event: "denied", From: actor, Text: reason, TS: time.Now().Unix()}
	}
	if pn.Stores.Meetings == nil {
		return deny(i18n.S("会议系统未启用"))
	}
	if pn.Fleet == nil {
		return deny(i18n.S("调度器未接线，会议控制不可用"))
	}
	room := pn.RoomKey(hub)
	if m, live := pn.Stores.Meetings.Active(room); live {
		if actor != m.Chair {
			return deny(i18n.Sf("会议钟只归本场主持 %s（编排者）", m.Chair))
		}
	} else {
		return deny(i18n.S("会议室当前空闲，没有可控制的会议"))
	}
	if _, err := pn.Fleet.MeetingCtl(room, actor, end, minutes); err != nil {
		return deny(err.Error())
	}
	return chat.Message{Event: "done"} // not "denied": nothing private to send
}
