package chat

// 截断回执（超长 say 的齐头截断曾是静默修剪——断在半句与断在半词是
// 两种话，房里却读作说话人的完整意思）：超限行的发送者本人收到一条
// 私有系统提示；短行不出、回执不广播进房。与人群词误用提示同族
// （打错/被裁的发言绝不做静默 no-op），私人指路，不进房史。

import (
	"strings"
	"testing"
)

func TestClampReceiptNudgesSpeakerOnly(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	甲, _ := h.Join("甲", "产品经理", false)
	drainSend(owner)
	drainSend(甲)

	// ① 房主超长行：正文恰为 maxTextLen 字，回执只落在房主自己的连接。
	long := strings.Repeat("长", maxTextLen+3)
	msg, _ := h.Say(owner, long)
	if n := len([]rune(msg.Text)); n != maxTextLen {
		t.Fatalf("正文应恰为 maxTextLen（got %d）", n)
	}
	if !hasSystemWith(drainSend(owner), "尾部已被截断送达") {
		t.Fatal("超长行未给发送者截断回执")
	}
	if hasSystemWith(drainSend(甲), "尾部已被截断送达") {
		t.Fatal("截断回执竟广播给了别人")
	}

	// ② 短行：不出回执。
	h.Say(owner, "今天进度不错")
	if hasSystemWith(drainSend(owner), "尾部已被截断送达") {
		t.Fatal("短行竟出了截断回执")
	}
	drainSend(甲)

	// ③ 成员超长行：回执只落成员本人，房主收不到。
	h.Say(甲, long)
	if !hasSystemWith(drainSend(甲), "尾部已被截断送达") {
		t.Fatal("成员超长行未给本人截断回执")
	}
	if hasSystemWith(drainSend(owner), "尾部已被截断送达") {
		t.Fatal("成员的截断回执竟落到了房主")
	}

	// ④ 回显超长（bridge 镜像）：裁掉的长回复同样要让成员知道尾巴
	// 没了——调度器补投/复读治理都依赖成员知道自己被裁过。
	h.SayBridged(甲, long)
	if !hasSystemWith(drainSend(甲), "尾部已被截断送达") {
		t.Fatal("回显超长行未给本人截断回执")
	}
}
