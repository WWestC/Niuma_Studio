package roomops

// verbs.go — the lobby management verbs (kick/rank_set/agent_archive),
// registered by the SHELL at Start: their frame constants are pre-wire
// history living in this package (kick.go/rank.go/archive.go), so the
// family moved wholesale. agent_save rides along (the archive store's
// write face). Sync.Once keeps test double-Starts honest.

import (
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

var verbsOnce sync.Once

// RegisterVerbs folds the lobby-management family into the registry.
func RegisterVerbs(rc *Face) {
	verbsOnce.Do(func() {
		verbs.Register(map[string]verbs.Func{
			MsgKick: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if hub != rc.Hub {
					verbs.DenyLobbyOnly(hub, client, "kick")
				} else {
					rc.HandleKick(client, in.Name)
				}
			},
			MsgRankSet: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if hub != rc.Hub {
					verbs.DenyLobbyOnly(hub, client, "rank_set")
				} else {
					rc.HandleRankSet(client, in.Name, in.Rank)
				}
			},
			MsgAgentArchive: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if hub != rc.Hub {
					verbs.DenyLobbyOnly(hub, client, "agent_archive")
				} else {
					rc.HandleAgentArchive(client, in.Name)
				}
			},
			chat.MsgAgentSave: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				if in.Agent != nil && rc.Stores.AgentStore != nil {
					a := in.Agent
					saved := rc.Stores.AgentStore.Upsert(a.Name, a.Role, a.Manual, a.Prompt)
					hub.Broadcast(chat.Message{Type: chat.MsgAgent, Event: "saved",
						Agent: &chat.AgentConfig{Name: saved.Name, Role: saved.Role, Manual: saved.Manual},
						From:  member.Name, TS: time.Now().Unix()})
				}
			},
		})
	})
}
