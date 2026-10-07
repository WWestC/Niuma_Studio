package roomops

import (
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// MsgRankSet is the C->S frame {"type":"rank_set","name":"小明","rank":6}.
// It lives in the server package for now (same batching rationale as
// MsgKick); folding into chat's const block is a one-liner if wanted.
const MsgRankSet = "rank_set"

// rankAs applies one rank_set on behalf of actor and returns the
// receipt text for the requester: a denial ("rank_set 被拒：…") or the
// success receipt ("已将 X 的等级调整为 N", PRD §4.1 wording). The
// announcement broadcast (engine wording, "" when nothing changed)
// happens inside and reaches every seat.
func (rc *Face) RankAs(actor, target string, rank int) string {
	if target == "" {
		return i18n.S("rank_set 被拒：缺少目标成员名")
	}
	if rank < 1 || rank > 9 {
		// Frame-level defense: reject loudly, never silently clamp
		// (v0.8 §6.3 — the engine's clamp is the second net).
		return i18n.Sf("rank_set 被拒：等级须为 1–9 的整数（收到 %d）", rank)
	}
	if target == rc.Local {
		return i18n.S("rank_set 被拒：房主等级不可设置")
	}
	if actor == target {
		return i18n.S("rank_set 被拒：不可调整自己的等级")
	}
	if rc.Stores.Engine == nil {
		return i18n.S("rank_set 被拒：等级系统未启用")
	}
	if ar := rc.Stores.Engine.Rank(actor); ar != tasks.RankHost {
		if ar < kickMinRank {
			return i18n.Sf("rank_set 被拒：需要 Lv.%d 及以上（%s 当前 Lv.%d）", kickMinRank, actor, ar)
		}
		if tr := rc.Stores.Engine.Rank(target); tr >= ar {
			return i18n.Sf("rank_set 被拒：仅可调整等级低于你的成员（%s 是 Lv.%d，你是 Lv.%d）", target, tr, ar)
		}
		if rank > ar-2 {
			return i18n.Sf("rank_set 被拒：最多可设到 Lv.%d（你的等级 %d − 2）；设更高请房主操作", ar-2, ar)
		}
	}
	// Announce with the engine's wording ("" when nothing changed —
	// same-rank sets stay silent on the broadcast, like the UI).
	if text := rc.Stores.Engine.SetRank(target, rank); text != "" {
		rc.Hub.System(text)
		// The ZCode sidebar title carries the Lv.N slot — refresh it the
		// moment the registry moves (nil hook: embeds/tests skip).
		if rc.Fleet != nil {
			rc.Fleet.RankChanged(target)
		}
	}
	return i18n.Sf("已将 %s 的等级调整为 %d", target, rank)
}

// handleRankSet is the seated WS-frame entry: denials and receipts go
// privately to the requester via SendTo.
func (rc *Face) HandleRankSet(c *chat.Client, target string, rank int) {
	rc.Hub.SendTo(c, chat.Message{Type: chat.MsgSystem,
		Text: rc.RankAs(c.Name(), target, rank), TS: time.Now().Unix()})
}
