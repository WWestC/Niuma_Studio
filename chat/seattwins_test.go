package chat

// SeatDialTwins 是座位门的判据（小苗-2 根治）：门口拒绝的恰是
// joinLocked 会顶成 -2 分身的拨号——活座凭据不匹配/免凭据、宽限
// 幽灵凭据不匹配（带着别房 token 拨此房）。免凭据拨入按名收回幽灵、
// 同 token 顶替活座，这两条成员自己的通路必须放行。

import (
	"testing"
	"time"
)

func TestSeatDialTwinsLiveSeat(t *testing.T) {
	h := NewHub()
	if twins, grace := h.SeatDialTwins("无人", "tokX"); twins || grace {
		t.Fatalf("空闲名字不该撞：%v/%v", twins, grace)
	}
	holder, m := h.JoinWithToken("小苗", "编排", false, "tok-book")
	if m.Name != "小苗" {
		t.Fatalf("落座失败：%+v", m)
	}
	if twins, grace := h.SeatDialTwins("小苗", "tok-book"); twins || grace {
		t.Fatalf("同 token 拨号该顶替（成员自己的一次性写）：%v/%v", twins, grace)
	}
	if twins, grace := h.SeatDialTwins("小苗", "tok-other"); !twins || grace {
		t.Fatalf("活座凭据不匹配该拦：%v/%v", twins, grace)
	}
	if twins, grace := h.SeatDialTwins("小苗", ""); !twins || grace {
		t.Fatalf("免凭据拨活座该拦（joinLocked 只对幽灵按名收回）：%v/%v", twins, grace)
	}
	h.Leave(holder)
}

func TestSeatDialTwinsGhostSeat(t *testing.T) {
	h := NewHub()
	// 快照恢复出的幽灵（小苗-2 事故里被暂停房冻住的那只）：token 是
	// 旧房的 e5d0，成员现行凭证是新房的 8d06。
	h.RestoreSeats([]SeatSnapshot{{Name: "小苗", Role: "编排", Token: "e5d0-old"}})
	if h.HasLiveMember("小苗") {
		t.Fatal("恢复的座位必须是幽灵，不是活座")
	}
	if twins, grace := h.SeatDialTwins("小苗", "8d06-new"); !twins || !grace {
		t.Fatalf("带着他房 token 拨幽灵该拦且点名宽限：%v/%v", twins, grace)
	}
	if twins, grace := h.SeatDialTwins("小苗", "e5d0-old"); twins || grace {
		t.Fatalf("幽灵自己的 token 该放行（收回）：%v/%v", twins, grace)
	}
	if twins, grace := h.SeatDialTwins("小苗", ""); twins || grace {
		t.Fatalf("免凭据拨入该放行（按名收回幽灵——历史通行权）：%v/%v", twins, grace)
	}
}

func TestSeatDialTwinsTokenlessGhost(t *testing.T) {
	h := NewHub()
	h.SetPresenceGrace(time.Hour)
	// 旧版快照/无 token 幽灵：任何拨入都可收回（空 token 短路）。
	h.RestoreSeats([]SeatSnapshot{{Name: "小猿"}})
	if twins, grace := h.SeatDialTwins("小猿", "any-token"); twins || grace {
		t.Fatalf("空 token 幽灵对任何拨入放行：%v/%v", twins, grace)
	}
}
