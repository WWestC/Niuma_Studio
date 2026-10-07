package chat

// 中断接续（v2.5）：告别快照抓到有人正在干活被退场打断——席位快照
// 必须原样带走在飞状态，重启那棒才能点名提示用户「让 TA 继续」；
// 播报帧带 event=interrupted 标记与结构化名单（前端渲染一键点名卡，
// CLI/旧客户端只读 Text 也成句）。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSeatsCarryWorkingTell(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	甲, _ := h.Join("小牛", "编排者", false)
	drainSend(甲)

	// 在飞：快照必须带上（含起点时刻）；下线翻转后必须带走。
	h.SetWorkState("小牛", true, 12345)
	snap := seatByName(h.Seats(), "小牛")
	if snap == nil || !snap.Working || snap.WorkingSince != 12345 {
		t.Fatalf("在飞席位的快照缺在飞状态: %+v", snap)
	}
	b, err := json.Marshal(h.Seats())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"working":true`) || !strings.Contains(string(b), `"working_since":12345`) {
		t.Fatalf("快照 JSON 缺在飞键: %s", b)
	}

	h.SetWorkState("小牛", false, 0)
	if snap = seatByName(h.Seats(), "小牛"); snap == nil || snap.Working {
		t.Fatalf("下线后快照仍在飞: %+v", snap)
	}
	if b, err = json.Marshal(h.Seats()); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(b), `"working"`) {
		t.Fatalf("idle 席位不该落 working 键: %s", b)
	}
	_ = owner
}

func TestSystemInterruptedFrameShape(t *testing.T) {
	h := NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwner("房主")
	甲, _ := h.Join("小牛", "编排者", false)
	drainSend(甲)

	seats := []InterruptedSeat{{Name: "小牛", Since: 12345}, {Name: "小马", Project: "proj-x", Room: "官网改版"}}
	h.SystemInterrupted(seats, "上次退出时 小牛（Niuma_Studio）、小马（项目「官网改版」） 正在干活……点名即可让他们接着干")

	// 在场者收到的帧：event 标记＋结构化名单＋可读正文＋序号。
	var frame *Message
	for _, m := range drainSend(甲) {
		if m.Type == MsgSystem && m.Event == EventInterrupted {
			f := m
			frame = &f
		}
	}
	if frame == nil {
		t.Fatal("成员连接未收到 interrupted 播报帧")
	}
	if len(frame.Interrupted) != 2 || frame.Interrupted[1].Project != "proj-x" || frame.Interrupted[1].Room != "官网改版" {
		t.Fatalf("名单载荷不完整: %+v", frame.Interrupted)
	}
	if frame.Text == "" || frame.Seq == 0 {
		t.Fatalf("播报帧缺正文或序号: %+v", frame)
	}
	// 落历史：晚开屏的用户回放历史也要看到同一条接续提示。
	found := false
	for _, m := range h.History() {
		if m.Type == MsgSystem && m.Event == EventInterrupted {
			found = true
		}
	}
	if !found {
		t.Fatal("interrupted 播报未落历史")
	}
	_ = owner
}

func seatByName(seats []SeatSnapshot, name string) *SeatSnapshot {
	for i := range seats {
		if seats[i].Name == name {
			return &seats[i]
		}
	}
	return nil
}
