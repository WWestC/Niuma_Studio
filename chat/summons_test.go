package chat

// 回显不召唤（deliverables are not summons）：bridge 回显是交付物不是
// 新指令——里面的 @所有人/@全员/@<岗位>组 字样只是引用，不得开点名窗、
// 不得全员展开、不得岗位群呼；镜像（mirror）保持完整点名语义（房主裁
// 定「会话里点名也算数」）；回显里单独 @某人 照常解析，且回显本身仍是
// 对进行中点名的有效回应。

import (
	"strings"
	"testing"
)

func drainSend(c *Client) []Message {
	var out []Message
	for {
		select {
		case m := <-c.send:
			out = append(out, m)
		default:
			return out
		}
	}
}

func hasSystemWith(msgs []Message, sub string) bool {
	for _, m := range msgs {
		if m.Type == MsgSystem && strings.Contains(m.Text, sub) {
			return true
		}
	}
	return false
}

func hasName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

// TestBridgedReplyDoesNotSummon pins the exemption's whole shape: a real
// @所有人 opens the window and expands; a bridged reply quoting the token
// does neither — but still acks the pending roll-call and still resolves
// the peers it literally names.
func TestBridgedReplyDoesNotSummon(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	甲, _ := h.Join("甲", "产品经理", false)
	乙, _ := h.Join("乙", "前端开发", false)
	丙, _ := h.Join("丙", "测试", false)
	drainSend(甲)
	drainSend(乙)
	drainSend(丙)

	// ① 真人点名：展开 + 开窗 + 系统播报。
	h.Say(owner, "@所有人 开会")
	frames := drainSend(丙)
	if len(frames) < 2 || frames[0].Type != MsgSay || !hasName(frames[0].Mentions, "乙") {
		t.Fatalf("点名 say 未全员展开: %+v", frames)
	}
	if !hasSystemWith(frames[1:], "点名已发起") {
		t.Fatalf("点名未播报开始: %+v", frames)
	}
	if len(h.allReqs) != 1 || len(h.allReqs[0].need) != 3 {
		t.Fatalf("点名窗形态不对: %+v", h.allReqs)
	}

	// ② 成员回显里引用 @所有人：不开新窗、不展开——但算作回应。
	h.SayBridged(甲, "收到 @所有人 指令！")
	frames = drainSend(丙)
	if hasSystemWith(frames, "点名已发起") {
		t.Fatal("回显引用点名词竟开了新的点名窗")
	}
	for _, f := range frames {
		if f.Type == MsgSay && hasName(f.Mentions, "乙") {
			t.Fatal("回显竟做了全员展开（乙 不在字面提及里）")
		}
	}
	if len(h.allReqs) != 1 {
		t.Fatalf("回显改变了点名窗数量: %d", len(h.allReqs))
	}
	if !h.allReqs[0].got["甲"] {
		t.Fatal("回显应算作对进行中点名的回应")
	}

	// ③ 回显里单独 @某人：照常解析（交付物可以被 @，对方照常被唤醒）。
	msg := h.sayLike(乙, "@丙 帮我看下接口", MsgSay, false, OriginBridge, nil, nil)
	if !hasName(msg.Mentions, "丙") {
		t.Fatal("回显里的逐人 @ 不再解析")
	}

	// ④ 镜像（房主会话里点名）保持完整点名语义——房主裁定。
	h.SayMirror(owner, "@所有人 会话里点名也算数")
	if len(h.allReqs) != 2 {
		t.Fatalf("镜像点名未开窗: %d", len(h.allReqs))
	}
	frames = drainSend(丙)
	if !hasSystemWith(frames, "点名已发起") {
		t.Fatal("镜像点名未播报开始")
	}

	// ⑤ 镜像 @全员：广播语义——展开但不开窗。
	h.SayMirror(owner, "@全员 广播一条")
	if len(h.allReqs) != 2 {
		t.Fatalf("广播词竟开了点名窗: %d", len(h.allReqs))
	}
	frames = drainSend(丙)
	if len(frames) == 0 || !hasName(frames[0].Mentions, "甲") {
		t.Fatal("镜像广播未展开提及")
	}
	if hasSystemWith(frames, "点名已发起") {
		t.Fatal("广播词播报了点名开始")
	}
}

// TestPostNameIsNotAWakeToken pins the shape the「让编排者拆解」静默
// bug exposed（f5d7e3b 写死 @编排者 措辞、v2.2 旗舰上岗后岗位名与成员
// 名分家）：岗位名「编排者」既不是成员名也不是身份段——@它谁也唤不
// 醒，又因没有组后缀连「未命中」提示都不播，消息作为无人点名沉底。
// 按岗寻人的正路是身份段组呼 @排期编排组；逐人 @真名（如 @小牛）也行。
func TestPostNameIsNotAWakeToken(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	小牛, _ := h.Join("小牛", "排期编排", false)
	drainSend(小牛)

	// ① 身份段组呼（生产词令）：命中在岗编排者。
	msg := h.sayLike(owner, "@排期编排组 请拆解 r_01：标题", MsgSay, false, "", nil, nil)
	if !hasName(msg.Mentions, "小牛") {
		t.Fatalf("岗位群呼未唤醒排期编排: %+v", msg.Mentions)
	}
	// ② 逐人 @真名：照常解析。
	msg = h.sayLike(owner, "@小牛 请拆解 r_01：标题", MsgSay, false, "", nil, nil)
	if !hasName(msg.Mentions, "小牛") {
		t.Fatalf("逐人 @ 真名未解析: %+v", msg.Mentions)
	}
	// ③ 岗位名 @：无人唤醒、无提示——本 bug 的静默形态，钉住别再犯。
	msg = h.sayLike(owner, "@编排者 请拆解 r_01：标题", MsgSay, false, "", nil, nil)
	if len(msg.Mentions) != 0 {
		t.Fatalf("岗位名 @ 竟唤醒了成员: %+v", msg.Mentions)
	}
	if hasSystemWith(drainSend(小牛), "未命中成员") {
		t.Fatal("非组词竟播报了岗位群呼未命中提示")
	}
}

// TestBridgedReplyDoesNotGroupWake pins the group-call half of the same
// exemption: a bridged reply quoting "@岗位组" neither wakes the stem's
// members nor fires the unmatched-stem hint; a real say still does both.
func TestBridgedReplyDoesNotGroupWake(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	甲, _ := h.Join("甲", "产品经理", false)
	乙, _ := h.Join("乙", "前端开发", false)
	drainSend(甲)
	drainSend(乙)

	// 真人岗位群呼：命中的岗位被唤醒。
	msg := h.sayLike(owner, "@产品组 冲", MsgSay, false, "", nil, nil)
	if !hasName(msg.Mentions, "甲") {
		t.Fatal("真人岗位群呼未唤醒命中成员")
	}
	// 回显引用同一群呼词：不唤醒。
	msg = h.sayLike(乙, "@产品组 收到", MsgSay, false, OriginBridge, nil, nil)
	if hasName(msg.Mentions, "甲") {
		t.Fatal("回显里的群呼词竟唤醒了岗位成员")
	}
	// 未命中提示：真人发言播报、回显静默。
	h.Say(owner, "@不存在的组 谁在")
	if !hasSystemWith(drainSend(甲), "未命中成员") {
		t.Fatal("真人未命中的群呼词未播报提示")
	}
	h.SayBridged(乙, "@不存在的组 收到")
	if hasSystemWith(drainSend(甲), "未命中成员") {
		t.Fatal("回显里的未命中群呼词竟播报了提示")
	}
}
