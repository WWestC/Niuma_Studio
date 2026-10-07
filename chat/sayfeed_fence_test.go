package chat

// sayfeed_fence_test.go — pumpSayFeed 的 panic 栅栏钉：一个坏观察者
// 只丢自己那一条，泵与后来的观察者照常吃线。护栏之前，注释声称
// "an observer panic must not kill the pump" 但没有 recover 兑现——
// 本测试把承诺变成行为。

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestSayObserverPanicIsFenced(t *testing.T) {
	h := NewHub()
	c, _ := h.Join("房主", "boss", false)

	var healthy int32
	// 坏观察者排在前：它的 panic 不许拦住后面的健康观察者
	h.OnSay(func(Message) { panic("观察者的 bug") })
	h.OnSay(func(Message) { atomic.AddInt32(&healthy, 1) })

	h.Report(c, "第一条：坏观察者在此 panicked")
	h.Report(c, "第二条：泵必须还活着")

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&healthy) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&healthy); got < 2 {
		t.Fatalf("坏观察者的 panic 应被栅栏吃掉、健康观察者两条全收：got %d/2", got)
	}
}
