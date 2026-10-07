package gitops

// verbs.go — the merge review-gate verbs, registered by the SHELL at
// Start (RegisterVerbs carries the chronicle/plan-hub seams the Face
// drinks; the sync.Once keeps test double-Starts from double-registering).
// The plan verbs stay in the verbs layer until the plan domain's turn.

import (
	"sync"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

var verbsOnce sync.Once

// RegisterVerbs folds the merge trio into the verb registry.
func RegisterVerbs(f *Face) {
	verbsOnce.Do(func() {
		verbs.Register(map[string]verbs.Func{
			chat.MsgMergeSubmit: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if in.Merge != nil {
					if msg := f.MergeSubmitAs(hub, client, member.Name, merge.MergeFromWire(in.Merge)); msg.Event == "denied" {
						hub.SendTo(client, msg)
					}
				}
				in.Merge = nil
			},
			chat.MsgMergeAccept: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if msg := f.MergeAcceptAs(hub, client, member.Name, in.MergeID, in.Project); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
				in.MergeID, in.Project = "", ""
			},
			chat.MsgMergeReject: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if msg := f.MergeRejectAs(hub, client, member.Name, in.MergeID, in.Project); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
				in.MergeID, in.Project = "", ""
			},
		})
	})
}
