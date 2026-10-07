package verbs

// verbs_receipts.go — 回执动词族（飞书式收到/表情回应）：事实
// 挂在原消息下，永不产生新聊天行。注册表拆分的纯搬移——语义注释与
// post-verb 清洗逐字保留（总纪律见 session_verbs.go）。

import (
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
)

func init() {
	Register(map[string]Func{
		chat.MsgAck: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			// 飞书式收到（in.Seq names the say line): a fact about the
			// original message, never a new chat line. The broadcast (and
			// the silent ignore of a self-ack / stale seq) is MarkAck's.
			// The SEQ-LESS shape is the CLI verb (niuma ack): receipt
			// everything this member's current turn owes — the dispatcher
			// holds the turn→cargo mapping (the structured sibling of the
			// bare 「收到」 text reply, footer-taught).
			if in.Seq == 0 && st.Fleet != nil {
				acked := st.Fleet.SeatAck(st.RoomKey(hub), member.Name)
				if acked {
					hub.SendTo(client, chat.Message{Type: chat.MsgSystem,
						Text: i18n.S("ack：本轮全部点名行已挂上「收到」回执")})
				} else {
					hub.SendTo(client, chat.Message{Type: chat.MsgSystem,
						Text: i18n.S("ack：当前没有欠回执的点名行")})
				}
				return
			}
			hub.MarkAck(member.Name, in.Seq)
			in.Seq = 0
		},
		chat.MsgReact: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			// 飞书式表情回应（in.Seq names the say line, in.Emoji the
			// palette slot, in.On adds/removes the member's own): same
			// fact discipline as the ack — never a new chat line, never
			// a wake. The broadcast (and the silent ignore of a stale
			// seq / bad emoji / full palette) is MarkReact's.
			hub.MarkReact(member.Name, in.Seq, in.Emoji, in.On)
			in.Seq, in.Emoji, in.On = 0, "", false
		},
	})
}
