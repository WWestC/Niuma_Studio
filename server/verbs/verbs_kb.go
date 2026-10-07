package verbs

// verbs_kb.go — 知识库动词族（写/追加/恢复/归档/反归档/删除）：
// 全族过 kbWriteGate 门（房主/owner/rank≥owner），可逆动作与内容编辑
// 同门是 v3 治理的取舍。注册表拆分的纯搬移——语义注释与 post-verb
// 清洗逐字保留（总纪律见 session_verbs.go）。

import (
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
)

func init() {
	Register(map[string]Func{
		chat.MsgKbWrite: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if in.Doc != nil {
				if err := st.Taps.KbWriteGate(member.Name, in.Doc.Key); err != nil {
					hub.SendTo(client, chat.Message{Type: chat.MsgKb, Event: "denied",
						From: member.Name, Text: err.Error(), TS: time.Now().Unix()})
					return
				}
				d := *in.Doc
				expectRev := -1 // unset
				if in.ExpectRev != nil && *in.ExpectRev >= 0 {
					expectRev = *in.ExpectRev
				}
				st.Taps.KbOp(hub, client, member.Name, "written", func(store *kb.DocsStore) (kb.DocMeta, error) {
					if expectRev >= 0 {
						return store.WriteExpect(d.Key, d.Title, d.Body, member.Name, expectRev)
					}
					return store.Write(d.Key, d.Title, d.Body, member.Name)
				})
			}
		},
		chat.MsgKbAppend: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if err := st.Taps.KbWriteGate(member.Name, in.Key); err != nil {
				hub.SendTo(client, chat.Message{Type: chat.MsgKb, Event: "denied",
					From: member.Name, Text: err.Error(), TS: time.Now().Unix()})
				return
			}
			st.Taps.KbOp(hub, client, member.Name, "appended", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return store.Append(in.Key, in.Text, member.Name)
			})
			in.Text = ""
		},
		chat.MsgKbRestore: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if err := st.Taps.KbWriteGate(member.Name, in.Key); err != nil {
				hub.SendTo(client, chat.Message{Type: chat.MsgKb, Event: "denied",
					From: member.Name, Text: err.Error(), TS: time.Now().Unix()})
				return
			}
			st.Taps.KbOp(hub, client, member.Name, "restored", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return st.Taps.KbRestoreAs(member.Name, in.Key, in.Rev)
			})
		},
		chat.MsgKbArchive: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			// v3 治理：归档/恢复走 kbRestoreAs 同门（房主/owner/
			// rank≥owner）——可逆的图书馆动作，不是内容编辑
			if err := st.Taps.KbWriteGate(member.Name, in.Key); err != nil {
				hub.SendTo(client, chat.Message{Type: chat.MsgKb, Event: "denied",
					From: member.Name, Text: err.Error(), TS: time.Now().Unix()})
				return
			}
			st.Taps.KbOp(hub, client, member.Name, "archived", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return st.Taps.KbArchiveAs(member.Name, in.Key, true)
			})
		},
		chat.MsgKbUnarchive: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if err := st.Taps.KbWriteGate(member.Name, in.Key); err != nil {
				hub.SendTo(client, chat.Message{Type: chat.MsgKb, Event: "denied",
					From: member.Name, Text: err.Error(), TS: time.Now().Unix()})
				return
			}
			st.Taps.KbOp(hub, client, member.Name, "unarchived", func(store *kb.DocsStore) (kb.DocMeta, error) {
				return st.Taps.KbArchiveAs(member.Name, in.Key, false)
			})
		},
		chat.MsgKbDelete: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if err := st.Taps.KbWriteGate(member.Name, in.Key); err != nil {
				hub.SendTo(client, chat.Message{Type: chat.MsgKb, Event: "denied",
					From: member.Name, Text: err.Error(), TS: time.Now().Unix()})
				return
			}
			st.Taps.KbDeleteOp(hub, client, member.Name, in.Key)
		},
	})
}
