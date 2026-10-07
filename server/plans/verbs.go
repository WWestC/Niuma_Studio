package plans

// verbs.go — the plan review gate's verbs (submit/accept/reject) plus
// the meeting clock and requirement filing verbs, registered by the
// SHELL at Start (the chronicle/plan-hub seams ride the Face). The
// autopilot_done verb stays in the verbs layer until that domain's
// turn.

import (
	"sync"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

var verbsOnce sync.Once

// RegisterVerbs folds the domain's verbs into the registry.
func RegisterVerbs(pn *Face) {
	verbsOnce.Do(func() {
		verbs.Register(map[string]verbs.Func{
			chat.MsgPlanSubmit: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if in.Plan != nil {
					if msg := pn.PlanSubmitAs(hub, client, member.Name, plan.PlanFromWire(in.Plan)); msg.Event == "denied" {
						hub.SendTo(client, msg)
					}
				}
				in.Plan = nil
			},
			chat.MsgPlanAccept: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if msg := pn.PlanAcceptAs(hub, client, member.Name, in.PlanID, plan.PlanFromWire(in.Revised), in.Project); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
				in.PlanID, in.Revised, in.Project = "", nil, ""
			},
			chat.MsgPlanReject: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if msg := pn.PlanRejectAs(hub, client, member.Name, in.PlanID, in.Project); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
				in.PlanID, in.Project = "", ""
			},
			chat.MsgMeetingEnd: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if msg := pn.MeetingCtlAs(hub, client, member.Name, true, 0); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
			},
			chat.MsgMeetingExtend: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				minutes := in.Minutes
				if minutes == 0 {
					minutes = 10
				}
				if msg := pn.MeetingCtlAs(hub, client, member.Name, false, minutes); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
			},
			chat.MsgReqCreate: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if in.Req != nil {
					hub.SendTo(client, pn.ReqCreateAs(hub, member.Name, requirements.ReqFromWire(in.Req), pn.RoomKey(hub)))
				}
				in.Req = nil
			},
		})
	})
}
