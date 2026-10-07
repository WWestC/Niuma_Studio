package skills

import (
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

func init() {
	verbs.Register(map[string]verbs.Func{
		chat.MsgSkillSave: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
			// The success path broadcast inside skillSaveAs reaches
			// this seat too; only the refusal is private.
			if in.Skill != nil {
				if msg := FaceOf(st).SkillSaveAs(hub, member.Name, in.Skill, in.Via); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
			}
			in.Skill, in.Via = nil, ""
		},
		chat.MsgMCPSave: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
			if in.Mcp != nil {
				if msg := FaceOf(st).McpSaveAs(hub, member.Name, in.Mcp, in.Via); msg.Event == "denied" {
					hub.SendTo(client, msg)
				}
			}
			in.Mcp, in.Via = nil, ""
		},
		chat.MsgAssemble: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
			if msg := FaceOf(st).AssembleAs(hub, member.Name, AssembleReq{
				Project: in.Project, Person: in.Person, Target: in.Target,
				Op: in.Op, Skills: in.Skills, MCPServers: in.MCPServers,
			}, in.Via); msg.Event == "denied" {
				hub.SendTo(client, msg)
			}
			in.Person, in.Target, in.Op, in.Project, in.Via = "", "", "", "", ""
			in.Skills, in.MCPServers = nil, nil
		},
	})
}
