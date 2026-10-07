package verbs

// verbs_tasks.go — 任务十字段动词族（建/改/确认/婉拒/仲裁）：
// v2.10 本房铸造、v2.8 关单验提交卡与房主仲裁的私拒都在这族。注册表
// 拆分的纯搬移——语义注释与 post-verb 清洗逐字保留（总纪律见
// session_verbs.go）。

import (
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/tasks"
)

func init() {
	Register(map[string]Func{
		chat.MsgTaskCreate: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if in.Task != nil {
				// v2.10：任务在本房架铸造——成员在项目房建的单带本项目的
				// t_NN 与挂靠（大厅建房＝池单，v1 形状不变）
				room := st.HubProject(hub)
				st.Taps.TaskOp(hub, client, member.Name, st.Taps.EngineOp(func(e *tasks.Engine) tasks.Outcome {
					return e.Create(room, member.Name, in.Task.Title, in.Task.Desc, in.Task.Assignee)
				}))
			}
		},
		chat.MsgTaskUpdate: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if in.Patch != nil {
				p := *in.Patch
				// v2.8 关单验提交：done 翻转先过 git 证据卡（非 git 项目/
				// 房主/未挂项目一律放行——卡是纪律不是刑罚）。
				st.Taps.TaskUpdateGateOp(hub, client, member.Name, in.TaskID, tasks.PatchFromWire(&p))
			}
		},
		chat.MsgTaskConfirm: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			// 确认也可能落 done（待确认变更带状态翻转）——同一道卡。
			st.Taps.TaskConfirmGateOp(hub, client, member.Name, in.TaskID)
		},
		chat.MsgTaskDecline: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			room := st.HubProject(hub)
			st.Taps.TaskOp(hub, client, member.Name, st.Taps.EngineOp(func(e *tasks.Engine) tasks.Outcome {
				return e.Decline(room, member.Name, in.TaskID)
			}))
		},
		chat.MsgTaskArbitrate: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			// 房主仲裁（Engine.Arbitrate 落账人固定是房主）：成员借道
			// 等于冒名顶替，私拒；approve 缺席同样拒——缺省读成驳回会
			// 把 assign 提案连任务一起取消，不能当默认值。
			if member.Name != st.Local {
				hub.SendTo(client, chat.Message{Type: chat.MsgTask, Event: "denied",
					TaskID: in.TaskID, From: member.Name,
					Text: i18n.S("仲裁仅限房主：请走接单/婉拒，或请房主代为仲裁"), TS: time.Now().Unix()})
			} else if in.Approve == nil {
				hub.SendTo(client, chat.Message{Type: chat.MsgTask, Event: "denied",
					TaskID: in.TaskID, From: member.Name,
					Text: i18n.S("仲裁缺 approve 判定（true 批准 / false 驳回）"), TS: time.Now().Unix()})
			} else {
				room := st.HubProject(hub)
				st.Taps.TaskOp(hub, client, member.Name, st.Taps.EngineOp(func(e *tasks.Engine) tasks.Outcome {
					return e.Arbitrate(room, in.TaskID, *in.Approve)
				}))
			}
		},
	})
}
