package server

// notice.go — the per-room group announcement's faces (飞书式群公告):
// GET /p/{key}/notice is the read side (the chat head's pinned entry
// and the CLI's `kb notice` resync truth); noticeOp is the owner's
// publish/update/clear behind the seatless face's notice_save verb.
// Broadcasts follow the task/kb frame contract — a reminder-only
// "notice" data-sync frame, never recorded in the chat history — plus
// one SystemRecordedEvent("notice", …) trail line so late joiners meet
// the announcement in the history replay. A non-silent publish also
// taps Options.NoticeWake (the staff push, main-wired to the fleet);
// its failure is an ambient note, never a refusal — the store write
// already stands.

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/notice"
)

// handleProjectNotice serves the room's current announcement (GET
// /p/{key}/notice): {"project", "notice" (the snapshot or null)}.
func (s *Server) handleProjectNotice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Stores.Notices == nil || s.opts.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if !s.opts.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	var slot any // JSON null when the room holds no notice
	if n, ok := s.opts.Stores.Notices.Get(key); ok {
		slot = wireNotice(n)
	}
	writeJSON(w, map[string]any{"project": key, "notice": slot})
}

// wireNotice converts the store type into the wire shape (the
// AgentConfig discipline: chat keeps its own projections).
func wireNotice(n notice.Notice) *chat.Notice {
	return &chat.Notice{Content: n.Content, By: n.By, TS: n.TS, Rev: n.Rev}
}

// noticeOp applies one owner notice_save against the DIALED room:
// empty text clears, anything else publishes/updates under the kb
// expect_rev contract. The receipt frame comes back for the seatless
// channel's writeBack; successes also broadcast the data-sync frame
// and record the trail line. expectRev < 0 means unset
// (last-writer-wins), 0 "must not exist yet" — the kb_write rule.
func (s *Server) noticeOp(h *chat.Hub, actor, text string, expectRev int, silent bool) chat.Message {
	if s.opts.Stores.Notices == nil {
		return noticeDenied(actor, i18n.S("公告未启用"))
	}
	key := s.roomKey(h)
	if strings.TrimSpace(text) == "" {
		if _, ok := s.opts.Stores.Notices.Get(key); !ok {
			return noticeDenied(actor, i18n.S("本房当前没有公告，无需撤下"))
		}
		if err := s.opts.Stores.Notices.Clear(key, actor, expectRev); err != nil {
			return noticeErr(actor, err)
		}
		frame := chat.Message{Type: chat.MsgNotice, Event: "cleared",
			From: actor, TS: time.Now().Unix()}
		h.Broadcast(frame)
		h.SystemRecordedEvent("notice", i18n.Sf("%s 撤下了群公告", actor))
		return frame
	}
	n, err := s.opts.Stores.Notices.SetExpect(key, text, actor, expectRev)
	if err != nil {
		return noticeErr(actor, err)
	}
	event := "updated"
	trail := i18n.S("更新了群公告")
	note := i18n.S("已更新")
	if n.Rev == 1 {
		event = "published"
		trail = i18n.S("发布了群公告")
		note = i18n.S("已发布")
	}
	frame := chat.Message{Type: chat.MsgNotice, Event: event,
		Notice: wireNotice(n), From: actor, TS: time.Now().Unix()}
	h.Broadcast(frame)
	h.SystemRecordedEvent("notice",
		i18n.Sf("%s %s：%s", actor, trail, firstLine(n.Content, 60)))
	if s.opts.Fleet != nil {
		// The staff push runs off the connection's read loop: one
		// member's cold-session reactivation must not stall the owner's
		// next frame. wake=false (a silent publish) lands the text in
		// every member's background lane instead of delivering now.
		// Failure is an ambient note (the DispatchAssemble contract) —
		// the publish itself already stands.
		go func() {
			if err := s.opts.Fleet.NoticeWake(key, actor, n.Content, !silent); err != nil {
				log.Printf("[公告] %s 通知牛马失败：%v", key, err)
				h.System(i18n.Sf("群公告%s，但通知牛马未送达（%v）——成员下次被点名时仍会看到公告", note, err))
			}
		}()
	}
	return frame
}

// noticeDenied is the private refusal frame (the task/kb contract).
func noticeDenied(actor, reason string) chat.Message {
	return chat.Message{Type: chat.MsgNotice, Event: "denied",
		From: actor, Text: reason, TS: time.Now().Unix()}
}

// noticeErr shapes a store error into the denial frame, attaching the
// room's current rev on an expect_rev conflict so the caller can
// re-read, merge and retry.
func noticeErr(actor string, err error) chat.Message {
	frame := noticeDenied(actor, err.Error())
	var rc *notice.RevConflictError
	if errors.As(err, &rc) {
		frame.CurrentRev = rc.CurrentRev
	}
	return frame
}

// firstLine is the trail line's excerpt: the content's first non-empty
// line, capped at n runes with an ellipsis.
func firstLine(content string, n int) string {
	line := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	r := []rune(line)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return line
}
