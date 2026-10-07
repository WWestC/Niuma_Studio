package verbs

// verbs_say.go — the verb registry's core pair (say/report), moved
// verbatim from the shell's session_verbs.go: the connection loop's
// old giant switch became this lookup table (the natural decomposition
// point — each verb's handler is a closure over exactly the five
// things a verb can touch). Semantics notes: a case-level `break` in
// the old switch is a `return` here (same "end of verb" meaning); the
// post-verb envelope scrubs (in.X = nil) stay inside each handler
// verbatim; unknown verbs still fall through silently (a map miss);
// MsgBye stays in the shell's loop — it controls the connection's
// lifecycle, not a room verb.

import "github.com/WWestC/Niuma_Studio/chat"

func init() {
	Register(map[string]Func{
		chat.MsgSay: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			if in.Origin == chat.OriginMirror && member.Name == st.Local && hub == st.Hub {
				hub.SayMirror(client, in.Text)
			} else if in.Quote != nil {
				// 结构化引用回复（引用即点名的根治形态）：引用走字段，
				// 正文保持干净。镜像线无引用面（会话窗口中继不带引用条），
				// 图片仓不在场时图片按纯文本 say 纪律落地。
				imgs := in.Images
				if st.Stores.MediaStore == nil {
					imgs = nil
				}
				hub.SayQuoted(client, in.Text, in.Quote, imgs)
			} else if len(in.Images) > 0 && st.Stores.MediaStore != nil {
				// 输入图片：仓库在场才收（nil 面 = 图片引用无处可去，按
				// 纯文本 say 落地，不丢消息）。镜像线不带图——镜像来自
				// 会话窗口，图片走正规上传面。
				hub.SayImages(client, in.Text, in.Images)
			} else {
				hub.Say(client, in.Text)
			}
			in.Text = ""
			in.Origin = ""
			in.Images = nil
			in.Quote = nil
		},
		chat.MsgReport: func(st *Seat, hub *chat.Hub, client *chat.Client, member *chat.Member, in *Envelope) {
			hub.Report(client, in.Text)
			in.Text = ""
		},
	})
}
