package chat

// The quote-reply prefix 「引用 #N @X：…」 rides the say text; its @X
// must resolve through the ordinary mention machinery so the quoted
// member is summoned — the reply IS the notification (the user-facing
// 引用即点名 contract, 飞书式「回复 @某人」). The legacy prefix
// 「引用 X：…」 (no @, no seq — history written before v2.6) keeps its
// no-summons shape: old lines must not start waking people.

import (
	"strings"
	"testing"
)

func TestQuotePrefixMentionsQuotedPerson(t *testing.T) {
	h := NewHub()
	h.Join("李四", "", false)
	h.Join("王五", "", false)
	sp, _ := h.Join("房主", "", false)

	msg, ok := h.Say(sp, "「引用 #12 @李四：方案 B 定稿」\n同意，按这个来")
	if !ok {
		t.Fatal("say 未送达")
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "李四" {
		t.Fatalf("引用前缀的 @李四 该随 mentions 解析点名: %+v", msg.Mentions)
	}
	if got := string(msg.Text); got == "" || msg.Seq == 0 {
		t.Fatalf("引用回复该照常入史: %+v", msg)
	}

	// 同前缀里再 @第二人：引用者与正文点名并存，都在 mentions 里
	msg2, _ := h.Say(sp, "「引用 #12 @李四：方案 B 定稿」\n@王五 也看一下")
	if len(msg2.Mentions) != 2 {
		t.Fatalf("引用@与正文@该并存: %+v", msg2.Mentions)
	}

	// 旧格式（无 @ 无 seq）不点名——历史存量行为不变
	msg3, _ := h.Say(sp, "「引用 李四：方案 B 定稿」\n同意")
	for _, m := range msg3.Mentions {
		if m == "李四" || m == "王五" {
			t.Fatalf("旧格式前缀不该点名: %+v", msg3.Mentions)
		}
	}

	// 引用离席者（宽限幽灵仍在名册）照常解析；彻底离场的名字解析不到、
	// 不误伤——mentions 只落真名
	msg4, _ := h.Say(sp, "「引用 #9 @不在场的人：…」\n收到")
	if len(msg4.Mentions) != 0 {
		t.Fatalf("不在场的人不该被记进 mentions: %+v", msg4.Mentions)
	}

	// snip 是被引人的原话，不是发言人的点名：摘要里字面的 @王五、
	// @所有人 都不解析、不召唤——引用一条「里面 @ 了人」的消息不会
	// 误唤醒那人，也不开全房点名（真要点名写在正文里）
	msg5, _ := h.Say(sp, "「引用 #12 @李四：@王五 你看看这个 @所有人」\n收到")
	if len(msg5.Mentions) != 1 || msg5.Mentions[0] != "李四" {
		t.Fatalf("snip 里的 @王五/@所有人 不该点名（只该有被引人 @李四）: %+v", msg5.Mentions)
	}
}

// 根治版：引用走结构化字段（Message.Quote），正文不再嵌文本前缀——
// At=true 时被引人随 mentions 点名（引用即点名按字段解析，正文无需
// @）；At=false 的机械引用（迟到回答的自动语境头）不点名；正文里的
// @ 与引用点名叫并存；无 From 的引用按无引用落地；snip/Via 超长被掐。
func TestStructuredQuoteSay(t *testing.T) {
	h := NewHub()
	h.Join("李四", "", false)
	h.Join("王五", "", false)
	sp, _ := h.Join("房主", "", false)

	// At=true：字段点名，正文干净
	msg, ok := h.SayQuoted(sp, "同意，按这个来", &Quote{Seq: 12, From: "李四", At: true, Snip: "方案 B 定稿"}, nil)
	if !ok {
		t.Fatal("say 未送达")
	}
	if msg.Quote == nil || msg.Quote.From != "李四" || !msg.Quote.At || msg.Quote.Seq != 12 {
		t.Fatalf("引用字段该随消息落地: %+v", msg.Quote)
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "李四" {
		t.Fatalf("At 引用该按字段点名李四: %+v", msg.Mentions)
	}
	if strings.Contains(msg.Text, "「引用") || strings.Contains(msg.Text, "@李四") {
		t.Fatalf("正文该保持干净（不嵌引用头、不带 @）: %q", msg.Text)
	}

	// At=false：机械语境引用不点名（迟到回答的自动语境头纪律不变）
	msg2, _ := h.SayQuoted(sp, "收到", &Quote{Seq: 9, From: "李四", Snip: "稍等"}, nil)
	if len(msg2.Mentions) != 0 {
		t.Fatalf("At=false 的机械引用不该点名: %+v", msg2.Mentions)
	}

	// 正文 @ 与引用点名并存
	msg3, _ := h.SayQuoted(sp, "@王五 也看一下", &Quote{Seq: 12, From: "李四", At: true, Snip: "方案 B 定稿"}, nil)
	if len(msg3.Mentions) != 2 {
		t.Fatalf("引用点名与正文 @ 该并存: %+v", msg3.Mentions)
	}

	// 无 From = 不是引用：按普通 say 原样落地
	msg4, _ := h.SayQuoted(sp, "没有引用的话", &Quote{Seq: 1, Snip: "孤儿引用"}, nil)
	if msg4.Quote != nil || len(msg4.Mentions) != 0 {
		t.Fatalf("无 From 的引用该被丢弃: %+v", msg4)
	}

	// snip/Via 超长被掐帽（snip 掐在渲染面的解析帽上，Via 是转发来源房名）
	long := strings.Repeat("长", 500)
	msg5, _ := h.SayQuoted(sp, "收到", &Quote{From: "李四", At: true, Snip: long, Via: long}, nil)
	if len([]rune(msg5.Quote.Snip)) != quoteSnipCap || len([]rune(msg5.Quote.Via)) != 60 {
		t.Fatalf("snip/Via 该被掐帽: %d/%d", len([]rune(msg5.Quote.Snip)), len([]rune(msg5.Quote.Via)))
	}
}
