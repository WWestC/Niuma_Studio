package autopilot

// verbs.go — the goal-scoped wrap-up verb (autopilot_done), registered
// by the shell at Start with the rest of the seam-wired domains. This
// closes the verb layer's last domain tap.

import (
	"sync"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

var verbsOnce sync.Once

// RegisterVerbs folds the domain's verbs into the registry.
func RegisterVerbs(ap *Face) {
	verbsOnce.Do(func() {
		verbs.Register(map[string]verbs.Func{
			chat.MsgAutopilotDone: func(st *verbs.Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *verbs.Envelope) {
				// 目标制补货收摊：本房编排者判定本次目标已达成（或确认无法
				// 达成）——关掉双开关、房内留一条系统行；回执 PRIVATE（CLI
				// 动词的收条）。project 骑 hello，text 载结论。
				hub.SendTo(client, ap.AutoPilotDoneAs(hub, member.Name, in.Text, ap.RoomKey(hub)))
				in.Text = ""
			},
		})
	})
}
