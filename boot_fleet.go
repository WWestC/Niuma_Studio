package main

// boot_fleet.go — the composition root's FleetAPI adapter: everything
// the server used to know as twenty-odd Options tap closures, one type
// wrapping the dispatch fleet (plus the two taps that route elsewhere).
// A nil fleet means --no-dispatch; each method degrades exactly as its
// former Options closure did — same error strings, same no-ops.

import (
	"fmt"
	"log"
	"time"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/dispatch"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// 组合根是两侧都看得见的地方——FleetAPI 的实现关系钉在此处，
// dispatch.Fleet 改签名时这里先红（找实现从 grep 变一次跳转）。
var _ server.FleetAPI = bootFleet{}

// bootFleet adapts *dispatch.Fleet to server.FleetAPI. fleet may be nil
// (--no-dispatch): the zero-fleet methods keep the old nil-tap
// semantics verbatim.
type bootFleet struct {
	fleet *dispatch.Fleet
	staff *staffing.Store // assemble's occupancy lookup (profile-target routing)
}

func (b bootFleet) SendWait(dt time.Duration) {
	if b.fleet != nil {
		b.fleet.SetSendWait(dt)
	}
}

func (b bootFleet) Whisper(project, member, text string) error {
	if b.fleet == nil {
		return fmt.Errorf("调度器未启动（--no-dispatch）")
	}
	return b.fleet.Whisper(project, member, text)
}

func (b bootFleet) ResumeRoom(project string) {
	if b.fleet != nil {
		b.fleet.ResumeRoom(project)
	}
}

func (b bootFleet) Assemble(target, project, person string) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	d := b.fleet.Lobby()
	if target == capability.TargetStaffing {
		d = b.fleet.Get(project)
	} else if row, ok := b.staff.OccupancyOf(person); ok {
		d = b.fleet.Get(row.ProjectKey)
	}
	if d == nil {
		return fmt.Errorf("%s 当前不在调度中", person)
	}
	return d.Assemble(person)
}

func (b bootFleet) ProjectActivated(key string, hub *chat.Hub) {
	if b.fleet == nil {
		return
	}
	if _, err := b.fleet.EnsureRoom(key, hub); err != nil {
		log.Printf("project %s: dispatcher: %v", key, err)
	}
}

func (b bootFleet) ProjectArchived(key string) {
	if b.fleet != nil {
		// 归档即全离：先收拢该房成员的 ZCode 侧栏行（牛马置顶
		// 条随房折叠），再退掉调度器——侧栏不留"运行中"的幽灵。
		b.fleet.Archive(key)
	}
}

func (b bootFleet) MergeLanded(project, branch, into string) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.MergeLanded(project, branch, into)
}

func (b bootFleet) PlanLanded(project, planID, planTitle, submitter string, landed []*tasks.Task) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.PlanLanded(project, planID, planTitle, submitter, landed)
}

func (b bootFleet) EstablishmentAdded(project string, row kb.EstablishmentRow, prev int, by string) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.EstablishmentAdded(project, row, prev, by)
}

func (b bootFleet) SeatAck(project, name string) bool {
	if b.fleet == nil {
		return false
	}
	return b.fleet.SeatAck(project, name)
}

func (b bootFleet) GitBranchSwitched(projectKey, from, to string) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.BranchSwitched(projectKey, from, to)
}

func (b bootFleet) GitUpdated(projectKey, branch string, incoming int) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.WorkspaceUpdated(projectKey, branch, incoming)
}

func (b bootFleet) GitSeat(project, person, branch string) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.SeatBranch(project, person, branch)
}

func (b bootFleet) AnswerQuestion(project, qid, by string, answers map[string]string) bool {
	if b.fleet == nil {
		return false
	}
	return b.fleet.AnswerQuestion(project, qid, by, answers)
}

func (b bootFleet) MeetingCtl(project, actor string, end bool, minutes int) (string, error) {
	if b.fleet == nil {
		return "", fmt.Errorf("调度未启用")
	}
	return b.fleet.MeetingCtl(project, actor, end, minutes)
}

func (b bootFleet) NoticeWake(project, from, content string, wake bool) error {
	if b.fleet == nil {
		return fmt.Errorf("调度未启用")
	}
	return b.fleet.Announce(project, from, content, wake)
}

func (b bootFleet) RankChanged(name string) {
	if b.fleet != nil {
		b.fleet.RankChanged(name)
	}
}

func (b bootFleet) PurgeMemberSessions(ids []string) error {
	return purgeMemberSessionsDeep(ids) // 项目清退/重置的 ZCode 深清腿
}

func (b bootFleet) PurgeProjectLanes(project string) (int, error) {
	// 清退/重置/删除共用的车道清理腿：未达消息随人走，名字复用不继承前任积压。
	// 先排干该项目调度器的在途 inbox 写——wipe 删键与 flusher 写键相撞会把
	// 刚删的积压原样复活（reset 后旧车道回生的异步版防线）。
	if b.fleet != nil {
		b.fleet.FlushProjectInbox(project)
	}
	return dispatch.WipeProjectLanes("", project)
}

func (b bootFleet) KickRecruit() {
	if b.fleet != nil {
		b.fleet.KickProjectRecruit() // 项目重置的招聘重燃腿（nil-safe）
	}
}

func (b bootFleet) RebuildVoteCancel(project string) {
	if b.fleet != nil {
		b.fleet.CancelRebuildVote(project) // AI 重编译投票作废（nil-safe）
	}
}

func (b bootFleet) RevealIndexWorkspaces() {
	if b.fleet != nil {
		b.fleet.RevealIndexWorkspaces() // 侧栏即时刷新的治疗性强投（nil-safe）
	}
}
