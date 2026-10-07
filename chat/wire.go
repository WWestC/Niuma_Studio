package chat

// wire.go is now a COMPATIBILITY RE-EXPORT of the wire package: the
// protocol's single truth (frame vocabulary, Message, every payload
// shape) moved to wire/ so the contract is importable standalone and
// chat no longer imports any domain package (the old wire.go pulled
// tasks/plan/merge/meeting/requirements in — the one layering breach;
// see wire/wire.go for the contract and the mirror discipline). The
// chat.* spellings the server/dispatch/cli grew up with keep compiling
// through these aliases; new code imports wire directly. This file
// only ever shrinks.

import "github.com/WWestC/Niuma_Studio/wire"

// Frame vocabulary (byte-stable — wire/wire.go is the truth).
const (
	MsgHello              = wire.MsgHello
	MsgSay                = wire.MsgSay
	MsgReport             = wire.MsgReport
	MsgBye                = wire.MsgBye
	MsgWelcome            = wire.MsgWelcome
	MsgJoin               = wire.MsgJoin
	MsgLeave              = wire.MsgLeave
	MsgSystem             = wire.MsgSystem
	MsgRename             = wire.MsgRename
	MsgMemberWork         = wire.MsgMemberWork
	MsgPing               = wire.MsgPing
	EventAgg              = wire.EventAgg
	EventInterrupted      = wire.EventInterrupted
	EventRebuild          = wire.EventRebuild
	OriginMirror          = wire.OriginMirror
	OriginBridge          = wire.OriginBridge
	OriginWhisper         = wire.OriginWhisper
	CloseReasonSuperseded = wire.CloseReasonSuperseded
	MsgTaskCreate         = wire.MsgTaskCreate
	MsgTaskUpdate         = wire.MsgTaskUpdate
	MsgTaskConfirm        = wire.MsgTaskConfirm
	MsgTaskDecline        = wire.MsgTaskDecline
	MsgTaskArbitrate      = wire.MsgTaskArbitrate
	MsgTask               = wire.MsgTask
	MsgAgentSave          = wire.MsgAgentSave
	MsgAgent              = wire.MsgAgent
	MsgKbWrite            = wire.MsgKbWrite
	MsgKbAppend           = wire.MsgKbAppend
	MsgKbRestore          = wire.MsgKbRestore
	MsgKbArchive          = wire.MsgKbArchive
	MsgKbUnarchive        = wire.MsgKbUnarchive
	MsgKbDelete           = wire.MsgKbDelete
	MsgKb                 = wire.MsgKb
	MsgSkillSave          = wire.MsgSkillSave
	MsgMCPSave            = wire.MsgMCPSave
	MsgAssemble           = wire.MsgAssemble
	MsgSkillEvt           = wire.MsgSkillEvt
	MsgMcpEvt             = wire.MsgMcpEvt
	MsgPlanSubmit         = wire.MsgPlanSubmit
	MsgPlanAccept         = wire.MsgPlanAccept
	MsgPlanReject         = wire.MsgPlanReject
	MsgPlan               = wire.MsgPlan
	MsgReqCreate          = wire.MsgReqCreate
	MsgReq                = wire.MsgReq
	MsgAutopilotDone      = wire.MsgAutopilotDone
	MsgAutopilot          = wire.MsgAutopilot
	MsgPause              = wire.MsgPause
	MsgMeeting            = wire.MsgMeeting
	MsgMeetingEnd         = wire.MsgMeetingEnd
	MsgMeetingExtend      = wire.MsgMeetingExtend
	MsgNoticeSave         = wire.MsgNoticeSave
	MsgNotice             = wire.MsgNotice
	MsgRead               = wire.MsgRead
	MsgQueue              = wire.MsgQueue
	MsgAck                = wire.MsgAck
	MsgReact              = wire.MsgReact
	MsgQuestion           = wire.MsgQuestion
	MsgQuestionDone       = wire.MsgQuestionDone
	MsgAnswer             = wire.MsgAnswer
	MsgTrace              = wire.MsgTrace
	MsgTerm               = wire.MsgTerm
	MsgGit                = wire.MsgGit
	MsgMergeSubmit        = wire.MsgMergeSubmit
	MsgMergeAccept        = wire.MsgMergeAccept
	MsgMergeReject        = wire.MsgMergeReject
	MsgMerge              = wire.MsgMerge
	MsgFsDirty            = wire.MsgFsDirty
	TraceThink            = wire.TraceThink
	TraceDraft            = wire.TraceDraft
	TraceTool             = wire.TraceTool
	TraceModel            = wire.TraceModel
	TraceTurn             = wire.TraceTurn
	TraceError            = wire.TraceError
	TraceInput            = wire.TraceInput
	TraceAsk              = wire.TraceAsk
	TraceAnswer           = wire.TraceAnswer
	TraceSys              = wire.TraceSys
)

// Payload shapes (wire owns them; these aliases keep old spellings).
type (
	Message         = wire.Message
	Member          = wire.Member
	Look            = wire.Look
	LookAcc         = wire.LookAcc
	InterruptedSeat = wire.InterruptedSeat
	Quote           = wire.Quote
	AgentConfig     = wire.AgentConfig
	Skill           = wire.Skill
	MCPServer       = wire.MCPServer
	MCPHeader       = wire.MCPHeader
	GitFile         = wire.GitFile
	GitCommit       = wire.GitCommit
	KbDoc           = wire.KbDoc
	Notice          = wire.Notice
	TraceEntry      = wire.TraceEntry
	Question        = wire.Question
	QuestionAsk     = wire.QuestionAsk
	QuestionOption  = wire.QuestionOption
	DiffLine        = wire.DiffLine
)
