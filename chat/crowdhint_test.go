package chat

// 人群词误用提示（房主实录：「全部人都去查看」零唤醒沉底背景车道，静默
// 被读成全房抗命）：房主的 say 带人群词却一个 @ 都没点上时，提示只发给
// 房主本人——真点名/广播/逐人 @/闲聊/成员回显都不出；与岗位群呼未命中
// 提示同族（打错的呼唤绝不做静默 no-op），但它是私人指路，不广播进房。

import "testing"

const crowdNudgeSub = "没有 @ 到任何人"

func TestCrowdWordMissNudgesOwnerOnly(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	甲, _ := h.Join("甲", "产品经理", false)
	drainSend(owner)
	drainSend(甲)

	// ① 房主人群词、无 @：提示只落在房主自己的连接上。
	h.Say(owner, "全部人都去查看 r_01")
	if !hasSystemWith(drainSend(owner), crowdNudgeSub) {
		t.Fatal("人群词误用未提示房主")
	}
	if hasSystemWith(drainSend(甲), crowdNudgeSub) {
		t.Fatal("提示竟广播给了成员")
	}

	// ② 真点名：提示不出，点名播报照常。
	h.Say(owner, "@所有人 去查看 r_01")
	frames := drainSend(owner)
	if hasSystemWith(frames, crowdNudgeSub) {
		t.Fatal("真点名竟出了误用提示")
	}
	if !hasSystemWith(frames, "点名已发起") {
		t.Fatal("真点名未播报开始")
	}
	drainSend(甲)

	// ③ 已逐人 @：有唤醒就不提示（人群词在场也不唠叨）。
	h.Say(owner, "@甲 大家有空也看看")
	if hasSystemWith(drainSend(owner), crowdNudgeSub) {
		t.Fatal("已点名却仍出误用提示")
	}
	drainSend(甲)

	// ④ 无人群词的闲聊：不提示。
	h.Say(owner, "今天进度不错")
	if hasSystemWith(drainSend(owner), crowdNudgeSub) {
		t.Fatal("闲聊竟出了误用提示")
	}
	drainSend(甲)

	// ⑤ 成员回显带人群词：bridge 豁免同族，不提示。
	h.SayBridged(甲, "收到，大家都加油")
	if hasSystemWith(drainSend(甲), crowdNudgeSub) {
		t.Fatal("成员回显竟出了误用提示")
	}

	// ⑥ 镜像线（房主会话里说的）：同属房主发言，照常提示。
	h.SayMirror(owner, "全员都去看下")
	if !hasSystemWith(drainSend(owner), crowdNudgeSub) {
		t.Fatal("房主镜像线的人群词误用未提示")
	}
}
