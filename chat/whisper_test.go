package chat

// whisper_test.go — 私信（manual v0.10 M4 占位落地）的 hub 面契约：
// 回执只进房主自己的座位（成员/观察者收不到）、不进 say 喂线（调度器
// 的 @ 路由与深夜亮灯喂线都不该看见它）、夹取与空载荷拒收、房主座位
// 缺席如实 false。

import (
	"strings"
	"testing"
	"time"
)

func drainOne(t *testing.T, c *Client) Message {
	t.Helper()
	select {
	case m := <-c.Receive():
		return m
	case <-time.After(500 * time.Millisecond):
		t.Fatal("等帧超时")
		return Message{}
	}
}

func TestWhisperAsOwnerReceiptOnly(t *testing.T) {
	h := NewHub()
	seat, _ := h.Join("房主", "boss", false)
	h.SetOwnerSeat(seat)
	li, _ := h.Join("小猿", "dev", false)
	// 排干 join 族帧（welcome/join 广播），频道清零后再断言 whisper
	for {
		select {
		case <-seat.Receive():
			continue
		case <-li.Receive():
			continue
		default:
		}
		break
	}

	fed := 0
	h.OnSay(func(Message) { fed++ })

	msg, ok := h.WhisperAsOwner("小猿", "悄悄话")
	if !ok {
		t.Fatal("房主座位在场，私信应成功")
	}
	if msg.Origin != OriginWhisper || msg.To != "小猿" || msg.From != "房主" || msg.Text != "悄悄话" {
		t.Fatalf("whisper 帧字段不符: %+v", msg)
	}
	// 房主座位收到回执
	got := drainOne(t, seat)
	if got.Origin != OriginWhisper || got.To != "小猿" {
		t.Fatalf("房主应收到 whisper 回执: %+v", got)
	}
	// 成员与喂线都看不见
	select {
	case m := <-li.Receive():
		t.Fatalf("成员不应收到 whisper 帧: %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
	if fed != 0 {
		t.Fatalf("whisper 不进 say 喂线（fed=%d）", fed)
	}
}

func TestWhisperAsOwnerRefusals(t *testing.T) {
	h := NewHub()
	// 房主座位缺席 → false
	if _, ok := h.WhisperAsOwner("小猿", "话"); ok {
		t.Fatal("无房主座位应拒收")
	}
	seat, _ := h.Join("房主", "boss", false)
	h.SetOwnerSeat(seat)
	// 空载荷 → false（空格文本与 say 同语义——夹取钳不管空白，不另立规）
	if _, ok := h.WhisperAsOwner("", "话"); ok {
		t.Fatal("空目标应拒收")
	}
	if _, ok := h.WhisperAsOwner("小猿", ""); ok {
		t.Fatal("空文本应拒收")
	}
	// 超长夹取到 maxTextLen（与 say 同钳）
	long := strings.Repeat("长", maxTextLen+500)
	msg, ok := h.WhisperAsOwner("小猿", long)
	if !ok {
		t.Fatal("超长私信应夹取而非拒收")
	}
	if n := len([]rune(msg.Text)); n != maxTextLen {
		t.Fatalf("夹取后长度应恰为 maxTextLen（got %d）", n)
	}
}
