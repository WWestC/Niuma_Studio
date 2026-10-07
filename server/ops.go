package server

import (
	"errors"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/tasks"
	"time"
)

// The engine/KB op wrappers: WS writes and the board-side host writes
// land here — one broadcast path per outcome so every client (and the
// mirror) stays in sync.

// engineOp runs op against the task engine; without one configured the
// operation is denied (main always passes an engine — this guards
// embedders and tests).
func (s *Server) engineOp(op func(*tasks.Engine) tasks.Outcome) tasks.Outcome {
	if s.opts.Stores.Engine == nil {
		return tasks.Outcome{Denied: true, Reason: i18n.S("任务系统未启用")}
	}
	return op(s.opts.Stores.Engine)
}

// taskOp reports an engine outcome to the actor's room (v2 P3-a: h is
// the hub the actor's connection routed to — the lobby or a project
// room): denials go privately to the requester, everything else
// broadcasts a reminder-only "task" event (never recorded in the chat
// history).
func (s *Server) taskOp(h *chat.Hub, c *chat.Client, actor string, out tasks.Outcome) {
	if out.Denied {
		h.SendTo(c, chat.Message{Type: chat.MsgTask, Event: "denied",
			From: actor, Text: out.Reason, TS: time.Now().Unix()})
		return
	}
	t := out.Task
	h.Broadcast(chat.Message{Type: chat.MsgTask, Event: out.Event,
		Task: t.Wire(), From: actor, Text: out.Text, TS: time.Now().Unix()})
	// r_10 编年史钩子（t_152）：task done 追加一行（链闭环判定宁录不漏）；
	// 行落任务所属办公室的志（ProjectKey 空 = 大厅）
	if out.Event == "updated" && t.Status == "done" {
		s.chronicle.OnTaskDone(t.ProjectKey, t.ID, t.Title, t.Assignee)
		// r_18 成就引擎（t_187）：同一事件喂判定——零新埋点，与编年史
		// 同位；达成即广播 system 行
		s.observeAchievements(h, Event{Kind: "task-done", Member: t.Assignee, At: time.Now().Unix()})
	}
}

// kbDiffFor renders one successful write's old→new rows for the kb
// broadcast frame (黑板红绿对比). DiffAround reads the pair under one
// store lock, so a concurrent writer can't skew the before/after; any
// miss — store disabled, doc gone, a newer write already landed — just
// drops the rows: the metadata broadcast stays truthful without them.
func kbDiffFor(store *kb.DocsStore, meta kb.DocMeta) ([]chat.DiffLine, int) {
	if store == nil {
		return nil, 0
	}
	old, cur, err := store.DiffAround(meta.Key, meta.Rev)
	if err != nil {
		return nil, 0
	}
	return chat.LineDiff(old, cur)
}

// kbOp applies one room-memory write into the actor's room (h — same
// routing rule as taskOp, v2 P3-a): errors go privately to the
// requester as a "denied" kb event, successes broadcast the doc's new
// metadata as a reminder-only event (never recorded in the chat
// history — same contract as taskOp). An expect_rev conflict attaches
// the room's current rev (v0.6 §3.3) so the caller can re-read, merge
// and retry. Successes also carry the write's line diff — the chat
// card's red/green rows (kbDiffFor).
func (s *Server) kbOp(h *chat.Hub, c *chat.Client, actor, event string, op func(*kb.DocsStore) (kb.DocMeta, error)) {
	if s.opts.Stores.Docs == nil {
		h.SendTo(c, chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: i18n.S("办公室记忆未启用"), TS: time.Now().Unix()})
		return
	}
	meta, err := op(s.opts.Stores.Docs)
	if err != nil {
		denied := chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: err.Error(), TS: time.Now().Unix()}
		var rc *kb.RevConflictError
		if errors.As(err, &rc) {
			denied.CurrentRev = rc.CurrentRev
		}
		h.SendTo(c, denied)
		return
	}
	diff, more := kbDiffFor(s.opts.Stores.Docs, meta)
	h.Broadcast(chat.Message{Type: chat.MsgKb, Event: event,
		KbDoc: &chat.KbDoc{Key: meta.Key, Title: meta.Title, Owner: meta.Owner,
			UpdatedBy: meta.UpdatedBy, UpdatedTS: meta.UpdatedTS, Rev: meta.Rev, Size: meta.Size,
			Archived: meta.Archived},
		Diff: diff, DiffMore: more,
		From: actor, TS: time.Now().Unix()})
	// r_18（t_189 表换入）：kb write 事件喂判定——A6 落笔定稿（design/
	// 首笔）与 A12 著书立说（累计十篇）的零新埋点事件源
	if event == "written" {
		s.observeAchievements(h, Event{Kind: "kb-write", Member: actor, DocKey: meta.Key, At: time.Now().Unix()})
	}
}

// kbOpSeatless is kbOp's seatless twin (t_39): same store call, same
// reminder broadcast on success, same denied shape (with current_rev on
// an expect_rev conflict) — but the receipt frame is RETURNED for the
// seatless channel to write back, since that connection holds no seat
// and therefore receives no SendTo. The CLI matches the frame by
// From == the owner's name, which both paths carry.
func (s *Server) kbOpSeatless(actor, event string, op func(*kb.DocsStore) (kb.DocMeta, error)) chat.Message {
	if s.opts.Stores.Docs == nil {
		return chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: i18n.S("办公室记忆未启用"), TS: time.Now().Unix()}
	}
	meta, err := op(s.opts.Stores.Docs)
	if err != nil {
		denied := chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: err.Error(), TS: time.Now().Unix()}
		var rc *kb.RevConflictError
		if errors.As(err, &rc) {
			denied.CurrentRev = rc.CurrentRev
		}
		return denied
	}
	diff, more := kbDiffFor(s.opts.Stores.Docs, meta)
	msg := chat.Message{Type: chat.MsgKb, Event: event,
		KbDoc: &chat.KbDoc{Key: meta.Key, Title: meta.Title, Owner: meta.Owner,
			UpdatedBy: meta.UpdatedBy, UpdatedTS: meta.UpdatedTS, Rev: meta.Rev, Size: meta.Size,
			Archived: meta.Archived},
		Diff: diff, DiffMore: more,
		From: actor, TS: time.Now().Unix()}
	s.hub.Broadcast(msg)
	// r_18（t_189 表换入）：无座通道的 write 同样喂判定（CLI kb write 与
	// 面板写一视同仁——A6/A12 的事件源）
	if event == "written" {
		s.observeAchievements(s.hub, Event{Kind: "kb-write", Member: actor, DocKey: meta.Key, At: time.Now().Unix()})
	}
	return msg
}

// kbRestoreAs applies kb_restore under the v0.5 §6.2 permission: the
// host, the doc's owner, or (v0.4 ranks landed) anyone ranked at or
// above the owner. Everything stays reversible via the history chain,
// so the check is about friction, not safety.
func (s *Server) kbRestoreAs(actor, key string, rev int) (kb.DocMeta, error) {
	doc, err := s.opts.Stores.Docs.Get(key, 0)
	if err != nil {
		return kb.DocMeta{}, err
	}
	if actor != s.opts.LocalName && actor != doc.Owner {
		if s.opts.Stores.Engine == nil || s.opts.Stores.Engine.Rank(actor) < s.opts.Stores.Engine.Rank(doc.Owner) {
			return kb.DocMeta{}, errors.New(i18n.Sf("仅房主、owner %s 或等级不低于 owner 者可恢复版本", doc.Owner))
		}
	}
	return s.opts.Stores.Docs.Restore(key, rev, actor)
}

// kbArchiveAs applies kb_archive/kb_unarchive (v3 governance) under
// kbRestoreAs's permission — the host, the doc's owner, or rank ≥
// owner: a shelf move is a reversible metadata flip, the same
// friction class as a restore. Rev and body are untouched, so the
// history chain and expect_rev anchors hold exactly as before.
func (s *Server) kbArchiveAs(actor, key string, archive bool) (kb.DocMeta, error) {
	doc, err := s.opts.Stores.Docs.Get(key, 0)
	if err != nil {
		return kb.DocMeta{}, err
	}
	if actor != s.opts.LocalName && actor != doc.Owner {
		if s.opts.Stores.Engine == nil || s.opts.Stores.Engine.Rank(actor) < s.opts.Stores.Engine.Rank(doc.Owner) {
			return kb.DocMeta{}, errors.New(i18n.Sf("仅房主、owner %s 或等级不低于 owner 者可归档/恢复文档", doc.Owner))
		}
	}
	if archive {
		return s.opts.Stores.Docs.Archive(key, actor)
	}
	return s.opts.Stores.Docs.Unarchive(key, actor)
}

// kbDeleteOp applies kb_delete (v3 governance): host-only, because it
// is the one kb op that loses content on purpose (body + history
// out; the WAL keeps the audit event). The broadcast carries the key
// and no doc meta — there is no doc left to describe.
func (s *Server) kbDeleteOp(h *chat.Hub, c *chat.Client, actor, key string) {
	msg := s.kbDeleteFrame(h, actor, key)
	if msg.Event == "denied" && c != nil {
		h.SendTo(c, msg) // successes already broadcast inside
	}
}

func (s *Server) kbDeleteFrame(h *chat.Hub, actor, key string) chat.Message {
	if s.opts.Stores.Docs == nil {
		return chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: i18n.S("办公室记忆未启用"), TS: time.Now().Unix()}
	}
	if actor != s.opts.LocalName {
		return chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: i18n.S("删除文档仅限房主（归档可逆，删除不是）"), TS: time.Now().Unix()}
	}
	if err := s.opts.Stores.Docs.Delete(key, actor); err != nil {
		return chat.Message{Type: chat.MsgKb, Event: "denied",
			From: actor, Text: err.Error(), TS: time.Now().Unix()}
	}
	msg := chat.Message{Type: chat.MsgKb, Event: "deleted", Key: key,
		From: actor, TS: time.Now().Unix()}
	h.Broadcast(msg)
	return msg
}

// serveMirror runs the seatless owner connection (v0.6 M1, extended by
// t_05/t_10/t_11): no Join, no seat, no leave. The handshake echo, then the
// frames the owner's name may send without holding a seat —
// {"type":"say","origin":"mirror"} lines spoken as the owner's real
// seat (each echoed back so the relay CLI can verify the origin marker
// survived) and the management frames kick / rank_set / agent_archive,
// executed as the owner with the receipt written straight back, plus
// kb_write / kb_append under the owner's name (t_39 — the owner's CLI
// kb path; conditional-write semantics intact). The owner's GUI seat is
// always live here, so a seated join would dedup into "owner-2" — this
// channel is the only way an owner-identity CLI frame reaches the
// room. Any other frame draws a private system
// nudge so misuse is visible, never silent. The connection relies on
// the client closing it (the CLI sends bye right after its echo); a
// dead peer unblocks the read the usual transport way.
// writeBack sends one kb receipt frame to the seatless connection.
