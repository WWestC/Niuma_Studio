package roomops

import (
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// MsgAgentArchive is the C->S frame {"type":"agent_archive","name":"小明"}.
// Same server-package home as MsgKick/MsgRankSet (batching rationale
// there).
const MsgAgentArchive = "agent_archive"

// archiveAs archives target's config on behalf of actor and returns the
// receipt text: a denial ("agent_archive 被拒：…") or the success
// receipt the seatless path echoes back. The reminder broadcast
// (agent{event:"archived"}) only happens on success. Idempotent:
// re-archiving succeeds again with the same receipt.
func (rc *Face) ArchiveAs(actor, target string) string {
	if target == "" {
		return i18n.S("agent_archive 被拒：缺少目标成员名")
	}
	if target == rc.Local {
		return i18n.S("agent_archive 被拒：房主档案不可归档")
	}
	if rc.Stores.AgentStore == nil {
		return i18n.S("agent_archive 被拒：档案系统未启用")
	}
	if ar := rc.rankOf(actor); ar != tasks.RankHost {
		if ar < kickMinRank {
			return i18n.Sf("agent_archive 被拒：需要 Lv.%d 及以上（%s 当前 Lv.%d）", kickMinRank, actor, ar)
		}
		if tr := rc.rankOf(target); tr >= ar {
			return i18n.Sf("agent_archive 被拒：仅可归档等级低于你的成员（%s 是 Lv.%d，你是 Lv.%d）", target, tr, ar)
		}
	}
	cfg, ok := rc.Stores.AgentStore.Archive(target)
	if !ok {
		return i18n.Sf("agent_archive 被拒：档案不存在（%s 无已保存配置；纯座位清理请用 kick）", target)
	}
	// 牛马管理的归档就是全离：目标名下所有占用编制行（任意项目的
	// onboard/active）就地 offboard。watchSeat 只覆盖调度器接管过的席
	// 位，手招/外接成员的编制行没有那条路径——不在这里折起，档案与
	// 编制表会分叉（牛马管理已归档、项目编制还在岗）。
	var offboardFailed []string
	if rc.Stores.StaffStore != nil {
		for _, e := range rc.Stores.StaffStore.ListByPerson(target) {
			if !e.Occupying() {
				continue
			}
			if _, err := rc.Stores.StaffStore.Offboard(e.ProjectKey, target); err != nil {
				offboardFailed = append(offboardFailed, e.ProjectKey+"："+err.Error())
			}
		}
	}
	// The receipt promises 「名册不再显示」, and the roster's live face
	// is the seat: an archived member whose seat lingers keeps showing
	// (presence used to win over archive). Kick the seat wherever it
	// sits — lobby or a project room; the dispatcher's watchSeat folds
	// management, staffing and the task index off the same event, so
	// the archive lands as a full departure without the offboard
	// pipeline.
	if rc.Registry != nil {
		for _, h := range rc.Registry.Rooms() {
			if h == rc.Hub {
				continue // the lobby is kicked below (same ladder, once)
			}
			for _, m := range h.Members() {
				if m.Name == target {
					if r := rc.kickIn(h, actor, target); strings.HasPrefix(r, i18n.S("kick 被拒")) {
						return i18n.Sf("已归档 %s，但移出其项目房间未成：%s", cfg.Name, r)
					}
					break
				}
			}
		}
	}
	for _, m := range rc.Hub.Members() {
		if m.Name == target {
			if r := rc.KickAs(actor, target); strings.HasPrefix(r, i18n.S("kick 被拒")) {
				return i18n.Sf("已归档 %s，但移出办公室未成：%s", cfg.Name, r)
			}
			break
		}
	}
	rc.Hub.Broadcast(chat.Message{Type: chat.MsgAgent, Event: "archived",
		Agent: &chat.AgentConfig{Name: cfg.Name, Role: cfg.Role, Manual: cfg.Manual, Archived: true},
		From:  actor, TS: time.Now().Unix()})
	if len(offboardFailed) > 0 {
		return i18n.Sf("已归档 %s，但部分编制行离编未成：%s", cfg.Name, strings.Join(offboardFailed, "；"))
	}
	return i18n.Sf("已归档 %s（名册与 /agents 默认列表不再显示；同名 recruit 即复职）", cfg.Name)
}

// handleAgentArchive is the seated WS-frame entry: the receipt (denial
// or success) goes privately to the requester via SendTo — same shape
// as handleRankSet, so one CLI success check serves both entries.
func (rc *Face) HandleAgentArchive(c *chat.Client, target string) {
	rc.Hub.SendTo(c, chat.Message{Type: chat.MsgSystem,
		Text: rc.ArchiveAs(c.Name(), target), TS: time.Now().Unix()})
}
