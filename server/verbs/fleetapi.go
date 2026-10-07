package verbs

// fleetapi.go — the server family's whole view of the dispatcher fleet, as
// ONE interface instead of the twenty-odd Options tap fields it used
// to be. Every member shares one contract (which used to be repeated
// field-by-field in Options): a call is an ambient notification or a
// delegated action; an error is a receipt note — never a refusal and
// never a rollback, the store write (if any) already stands; a nil
// Fleet means dispatch is off (embeds, tests) and the caller keeps its
// documented degraded path, exactly as a nil tap field did before.
//
// The concrete wiring lives in main (the composition root): an adapter
// over *dispatch.Fleet plus the few taps that route elsewhere (zcode
// purges, the reveal nudge). dispatch.Fleet may grow to implement this
// interface directly; nothing in server imports dispatch — the
// dependency arrow stays one-way, main's to lose.
//
// This is also the platform seam: a future remote or multi-process
// fleet satisfies the same interface behind the same call sites.

import (
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// FleetAPI is every ambient tap and delegation the server makes into
// the dispatcher fleet. See the file comment for the shared contract.
type FleetAPI interface {
	// SendWait relays a live 消息注入等待 write (POST /dispatch-wait)
	// to every dispatcher without a restart.
	SendWait(dt time.Duration)
	// Whisper delivers the owner's 私信 into one member's lane; an
	// error is reported to the sender honestly (a whisper never fakes
	// a delivery receipt).
	Whisper(project, member, text string) error
	// ResumeRoom re-kicks one room's lanes on an un-pause.
	ResumeRoom(project string)
	// Assemble pushes an accepted assembly into the member's live
	// dispatcher (the CAP-M2 effect matrix).
	Assemble(target, project, person string) error
	// ProjectActivated starts (or re-arms) the project's per-project
	// dispatcher; the room hub rides along.
	ProjectActivated(key string, hub *chat.Hub)
	// ProjectArchived retires the project's dispatcher (idempotent for
	// drafts that never had one).
	ProjectArchived(key string)
	// MergeLanded tells the branch's owner their merge reached the
	// mainline (merged broadcasts wake nobody on their own).
	MergeLanded(project, branch, into string) error
	// PlanLanded delivers the addressed wake lines for a landed plan
	// (assignees and the orchestrator hear their tasks).
	PlanLanded(project, planID, planTitle, submitter string, landed []*tasks.Task) error
	// EstablishmentAdded delivers the 【加编通知】 to the seated HR.
	EstablishmentAdded(project string, row kb.EstablishmentRow, prev int, by string) error
	// SeatAck converts the member's current armed turn cargo into
	// receipts for a seq-less CLI ack; false = nothing owed.
	SeatAck(project, name string) bool
	// GitBranchSwitched warns shared-tree members their files changed
	// under them (a branch checkout landed).
	GitBranchSwitched(projectKey, from, to string) error
	// GitUpdated warns shared-tree members a pull fast-forwarded the
	// workspace (incoming > 0).
	GitUpdated(projectKey, branch string, incoming int) error
	// GitSeat performs a git-seat move through the dispatcher (staffing
	// 落账、树内换枝、跨树迁移、背景告知、失败回滚). The error becomes
	// the click's 400 refusal.
	GitSeat(project, person, branch string) error
	// AnswerQuestion wakes the member's parked AskUserQuestion call
	// with the chosen answers; false = no dispatcher / no open ask.
	AnswerQuestion(project, qid, by string, answers map[string]string) bool
	// MeetingCtl fires or extends the live agenda's window (the chair's
	// clock grip); the receipt string rides the reply.
	MeetingCtl(project, actor string, end bool, minutes int) (string, error)
	// NoticeWake delivers a published announcement into staffed
	// members' sessions (wake=false = background lane).
	NoticeWake(project, from, content string, wake bool) error
	// RankChanged refreshes the member's sidebar title after a rank
	// write.
	RankChanged(name string)
	// PurgeMemberSessions deep-cleans the harvested ZCode session ids
	// (dismiss/reset/delete faces).
	PurgeMemberSessions(ids []string) error
	// PurgeProjectLanes wipes undelivered cargo for wiped people.
	PurgeProjectLanes(project string) (int, error)
	// KickRecruit re-arms the recruit loop after a successful project
	// reset.
	KickRecruit()
	// RebuildVoteCancel voids any open AI-rebuild vote in the room.
	RebuildVoteCancel(project string)
	// RevealIndexWorkspaces force-reveals the host workspace once (the
	// settings toggle's heal nudge).
	RevealIndexWorkspaces()
}

// FleetFuncs adapts plain functions to FleetAPI — tests and embeds set
// only the taps they care about; an absent fn is the method's
// documented neutral result. Production wiring lives in main.
type FleetFuncs struct {
	SendWaitFn           func(dt time.Duration)
	WhisperFn            func(project, member, text string) error
	ResumeRoomFn         func(project string)
	AssembleFn           func(target, project, person string) error
	ProjectActivatedFn   func(key string, hub *chat.Hub)
	ProjectArchivedFn    func(key string)
	MergeLandedFn        func(project, branch, into string) error
	PlanLandedFn         func(project, planID, planTitle, submitter string, landed []*tasks.Task) error
	EstablishmentAddedFn func(project string, row kb.EstablishmentRow, prev int, by string) error
	SeatAckFn            func(project, name string) bool
	GitBranchSwitchedFn  func(projectKey, from, to string) error
	GitUpdatedFn         func(projectKey, branch string, incoming int) error
	GitSeatFn            func(project, person, branch string) error
	AnswerQuestionFn     func(project, qid, by string, answers map[string]string) bool
	MeetingCtlFn         func(project, actor string, end bool, minutes int) (string, error)
	NoticeWakeFn         func(project, from, content string, wake bool) error
	RankChangedFn        func(name string)
	PurgeMemberSessFn    func(ids []string) error
	PurgeProjectLanesFn  func(project string) (int, error)
	KickRecruitFn        func()
	RebuildVoteCancelFn  func(project string)
	RevealIndexFn        func()
}

func (f FleetFuncs) SendWait(dt time.Duration) {
	if f.SendWaitFn != nil {
		f.SendWaitFn(dt)
	}
}
func (f FleetFuncs) Whisper(p, m, t string) error {
	if f.WhisperFn == nil {
		return nil
	}
	return f.WhisperFn(p, m, t)
}
func (f FleetFuncs) ResumeRoom(p string) {
	if f.ResumeRoomFn != nil {
		f.ResumeRoomFn(p)
	}
}
func (f FleetFuncs) Assemble(target, project, person string) error {
	if f.AssembleFn == nil {
		return nil
	}
	return f.AssembleFn(target, project, person)
}
func (f FleetFuncs) ProjectActivated(key string, hub *chat.Hub) {
	if f.ProjectActivatedFn != nil {
		f.ProjectActivatedFn(key, hub)
	}
}
func (f FleetFuncs) ProjectArchived(key string) {
	if f.ProjectArchivedFn != nil {
		f.ProjectArchivedFn(key)
	}
}
func (f FleetFuncs) MergeLanded(project, branch, into string) error {
	if f.MergeLandedFn == nil {
		return nil
	}
	return f.MergeLandedFn(project, branch, into)
}
func (f FleetFuncs) PlanLanded(project, planID, planTitle, submitter string, landed []*tasks.Task) error {
	if f.PlanLandedFn == nil {
		return nil
	}
	return f.PlanLandedFn(project, planID, planTitle, submitter, landed)
}
func (f FleetFuncs) EstablishmentAdded(project string, row kb.EstablishmentRow, prev int, by string) error {
	if f.EstablishmentAddedFn == nil {
		return nil
	}
	return f.EstablishmentAddedFn(project, row, prev, by)
}
func (f FleetFuncs) SeatAck(project, name string) bool {
	if f.SeatAckFn == nil {
		return false
	}
	return f.SeatAckFn(project, name)
}
func (f FleetFuncs) GitBranchSwitched(projectKey, from, to string) error {
	if f.GitBranchSwitchedFn == nil {
		return nil
	}
	return f.GitBranchSwitchedFn(projectKey, from, to)
}
func (f FleetFuncs) GitUpdated(projectKey, branch string, incoming int) error {
	if f.GitUpdatedFn == nil {
		return nil
	}
	return f.GitUpdatedFn(projectKey, branch, incoming)
}
func (f FleetFuncs) GitSeat(project, person, branch string) error {
	if f.GitSeatFn == nil {
		return nil
	}
	return f.GitSeatFn(project, person, branch)
}
func (f FleetFuncs) AnswerQuestion(project, qid, by string, answers map[string]string) bool {
	if f.AnswerQuestionFn == nil {
		return false
	}
	return f.AnswerQuestionFn(project, qid, by, answers)
}
func (f FleetFuncs) MeetingCtl(project, actor string, end bool, minutes int) (string, error) {
	if f.MeetingCtlFn == nil {
		return "", nil
	}
	return f.MeetingCtlFn(project, actor, end, minutes)
}
func (f FleetFuncs) NoticeWake(project, from, content string, wake bool) error {
	if f.NoticeWakeFn == nil {
		return nil
	}
	return f.NoticeWakeFn(project, from, content, wake)
}
func (f FleetFuncs) RankChanged(name string) {
	if f.RankChangedFn != nil {
		f.RankChangedFn(name)
	}
}
func (f FleetFuncs) PurgeMemberSessions(ids []string) error {
	if f.PurgeMemberSessFn == nil {
		return nil
	}
	return f.PurgeMemberSessFn(ids)
}
func (f FleetFuncs) PurgeProjectLanes(project string) (int, error) {
	if f.PurgeProjectLanesFn == nil {
		return 0, nil
	}
	return f.PurgeProjectLanesFn(project)
}
func (f FleetFuncs) KickRecruit() {
	if f.KickRecruitFn != nil {
		f.KickRecruitFn()
	}
}
func (f FleetFuncs) RebuildVoteCancel(p string) {
	if f.RebuildVoteCancelFn != nil {
		f.RebuildVoteCancelFn(p)
	}
}
func (f FleetFuncs) RevealIndexWorkspaces() {
	if f.RevealIndexFn != nil {
		f.RevealIndexFn()
	}
}
