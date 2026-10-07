package roomops

import (
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// MsgKick is the C->S frame {"type":"kick","name":"小明"}. It lives in
// the server package for now (t_05 lands in batches around t_02's
// occupancy of chat/hub.go); folding it into chat's const block is a
// one-liner if that home ever matters.
const MsgKick = "kick"

// kickMinRank is the management-rank threshold for delegated kicks
// (v0.6 PRD §3.2 / §13-1: Lv.8 is the default ruling; the host may
// retune it, and any change must be frozen into kb/manual.md).
const kickMinRank = verbs.KickMinRank

// rankOf resolves a member's rank; without an engine nobody qualifies
// for delegated management (the host path never consults ranks).
func (rc *Face) rankOf(name string) int {
	if rc.Stores.Engine == nil {
		return 1
	}
	return rc.Stores.Engine.Rank(name)
}

// kickAs applies one kick in the server's own room (the lobby) on
// behalf of actor — the WS frame's entry point. Project rooms go
// through kickIn (v2 P5-a: the offboard orchestration kicks in the
// departing member's project room).
func (rc *Face) KickAs(actor, target string) string {
	return rc.kickIn(rc.Hub, actor, target)
}

// kickIn is kickAs generalized over the target room: same permission
// ladder (ranks are global), same receipt contract, the notice and the
// kick landing in h. The receipt is the denial ("kick 被拒：…") or the
// very notice string the room just broadcast ("已将 X 移出办公室") —
// callers echo that back so one success check serves every path.
// Broadcasts only happen on success.
func (rc *Face) kickIn(h *chat.Hub, actor, target string) string {
	if target == "" {
		return i18n.S("kick 被拒：缺少目标成员名")
	}
	if target == rc.Local {
		return i18n.S("kick 被拒：房主不可被移出")
	}
	if ar := rc.rankOf(actor); ar != tasks.RankHost {
		if ar < kickMinRank {
			return i18n.Sf("kick 被拒：需要 Lv.%d 及以上（%s 当前 Lv.%d）", kickMinRank, actor, ar)
		}
		if tr := rc.rankOf(target); tr >= ar {
			return i18n.Sf("kick 被拒：只能移出等级低于你的成员（%s 是 Lv.%d，你是 Lv.%d）", target, tr, ar)
		}
	}
	// Existence pre-check so the notice is never broadcast for a name
	// the room does not hold (the UI path needs no check: its names
	// come from the rendered roster).
	found := false
	for _, m := range h.Members() {
		if m.Name == target {
			found = true
			break
		}
	}
	if !found {
		return i18n.Sf("kick 被拒：%s 不在办公室（在线成员与幽灵席位中均无此名）", target)
	}
	// The notice text is load-bearing: t_12-era clients match it to
	// stop retrying, and the CLI's success receipt is the same string.
	notice := i18n.Sf("已将 %s 移出办公室", target)
	h.System(notice)
	if _, ok := h.Kick(target); !ok {
		// The target left between the pre-check and the kick — the
		// notice is already out (same race the UI path has); tell the
		// requester what actually happened.
		return i18n.Sf("kick 被拒：%s 在处理瞬间已离开办公室，请核对后重试", target)
	}
	return notice
}

// handleKick is the seated WS-frame entry: denials go privately to the
// requester via SendTo (never broadcast, never in history); on success
// the requester already saw the broadcast notice, so nothing more is
// sent.
func (rc *Face) HandleKick(c *chat.Client, target string) {
	if r := rc.KickAs(c.Name(), target); strings.HasPrefix(r, i18n.S("kick 被拒")) {
		rc.Hub.SendTo(c, chat.Message{Type: chat.MsgSystem, Text: r, TS: time.Now().Unix()})
	}
}
