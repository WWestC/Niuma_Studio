package chat

// presence_test.go — r_19 修订（代行不失聪）的在场原语口径：房主席
// 发言盖章、成员发言不盖章、点卡腿（NoteOwnerActivity）盖章、窗内读
// 在场、窗外与无证据读缺席——dispatcher 的提问宽限与引擎的代收让位
// 都吃这一个判。

import (
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/util"
)

func TestOwnerPresenceStamp(t *testing.T) {
	h := NewHub()
	base := util.Now()
	prev := util.NowFn()
	util.SetNow(func() int64 { return base })
	t.Cleanup(func() { util.SetNow(prev) })

	own, _ := h.Join("房主", "", false)
	h.SetOwnerSeat(own)
	mem, _ := h.Join("小牛", "开发", false)

	if h.OwnerActiveWithin(OwnerPresenceWindow) {
		t.Fatal("无证据应读作缺席")
	}
	h.Say(mem, "成员路过")
	if h.OwnerActiveWithin(OwnerPresenceWindow) {
		t.Fatal("成员发言不算房主在场")
	}
	h.Say(own, "我看一下")
	if !h.OwnerActiveWithin(OwnerPresenceWindow) {
		t.Fatal("房主席发言应盖章（含 mirror 同路）")
	}
	util.SetNow(func() int64 { return base + int64(OwnerPresenceWindow/time.Second) + 1 })
	if h.OwnerActiveWithin(OwnerPresenceWindow) {
		t.Fatal("在场窗过期应读作缺席")
	}
	h.NoteOwnerActivity()
	if !h.OwnerActiveWithin(OwnerPresenceWindow) {
		t.Fatal("点卡腿应盖章")
	}
}

// TestPausedRoomFreezesGhostSweeper（房间暂停 × 宽限钟）：暂停的语义是
// 「恢复前一切保持现状」——座位定格。重启后 attach 暂时失败的成员靠
// 快照 ghost 撑着座位，若暂停期宽限钟照走，ghost 到期被清、leave 广播，
// 小人就凭空消失（v2.13 实录：暂停→重启→小人不见了）。冻结到恢复：
// 暂停期每一拍顺延 deadline；恢复后照常倒计时、照常清。
func TestPausedRoomFreezesGhostSweeper(t *testing.T) {
	h := NewHub()
	h.SetPresenceGrace(250 * time.Millisecond)
	paused := true
	h.PausedFn = func() bool { return paused }
	h.RestoreSeats([]SeatSnapshot{{Name: "小牛", Role: "开发", Token: "tok"}})

	has := func() bool {
		for _, m := range h.Members() {
			if m.Name == "小牛" {
				return true
			}
		}
		return false
	}

	// 暂停期：远超宽限窗的等待，ghost 必须还在（宽限钟冻结）
	time.Sleep(700 * time.Millisecond)
	if !has() {
		t.Fatal("暂停期宽限钟照走：ghost 被清，座位凭空消失")
	}

	// 恢复：宽限钟重新倒计时，到点照常清
	paused = false
	deadline := time.Now().Add(2 * time.Second)
	for has() {
		if time.Now().After(deadline) {
			t.Fatal("恢复后 ghost 应在宽限窗内被清")
		}
		time.Sleep(40 * time.Millisecond)
	}
}
