package storetest

// notice.go — the notice domain's contract suite: the expect_rev
// conditional-write family, the REVERSE failure contract (the store
// is the source of truth — a write that fails DENIES the publish),
// and the persistence round-trip.

import (
	"errors"
	"testing"

	"github.com/WWestC/Niuma_Studio/notice"
)

// NoticeHarness wires one backing into the notice suite.
type NoticeHarness struct {
	Name string
	// OpenBacking: open yields one engine bound to one subject, reopen
	// reconstructs over the same durable backing.
	OpenBacking func(t *testing.T) (open func(subject string) *notice.Engine, reopen func(subject string) func(*testing.T) *notice.Engine)
}

// TestNoticeStore is the notice domain's contract suite.
func TestNoticeStore(t *testing.T, h NoticeHarness) {
	if h.OpenBacking == nil {
		t.Fatalf("%s: harness 无 OpenBacking", h.Name)
	}
	mk := func(t *testing.T) (*notice.Engine, func(*testing.T) *notice.Engine) {
		open, reopen := h.OpenBacking(t)
		return open(""), reopen("")
	}

	t.Run("expect rev contract", func(t *testing.T) {
		s, _ := mk(t)
		if _, err := s.SetExpect("demo", "第一条", "房主", 5); err == nil {
			t.Fatalf("期望 5 而房内无公告应拒（%s）", h.Name)
		}
		n, err := s.SetExpect("demo", "第一条", "房主", 0)
		if err != nil || n.Rev != 1 {
			t.Fatalf("首 发 expect 0 应成立 rev=1（%s: %+v %v）", h.Name, n, err)
		}
		if _, err := s.SetExpect("demo", "改版", "房主", 0); err == nil {
			t.Fatalf("已存在时 expect 0 应拒（%s）", h.Name)
		}
		var conflict *notice.RevConflictError
		n2, err := s.SetExpect("demo", "改版", "房主", 9)
		if !errors.As(err, &conflict) || n2.Rev != 0 {
			t.Fatalf("错版应拒且报当前版（%s: %+v %v）", h.Name, n2, err)
		}
		if n2, err := s.SetExpect("demo", "改版", "房主", 1); err != nil || n2.Rev != 2 {
			t.Fatalf("对版更新应成立 rev=2（%s: %+v %v）", h.Name, n2, err)
		}
	})

	t.Run("clear and persistence", func(t *testing.T) {
		s, reopen := mk(t)
		if _, err := s.SetExpect("demo", "留存的", "房主", -1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetExpect("book", "别房的", "房主", -1); err != nil {
			t.Fatal(err)
		}
		if err := s.Clear("demo", "房主", 9); err == nil {
			t.Fatalf("错版清空应拒（%s）", h.Name)
		}
		if err := s.Clear("demo", "房主", 1); err != nil {
			t.Fatalf("对版清空应成立（%s: %v）", h.Name, err)
		}
		if err := s.Clear("demo", "房主", 0); err != nil {
			t.Fatalf("清空缺席公告＝no-op 成功（%s: %v）", h.Name, err)
		}
		s2 := reopen(t)
		if _, ok := s2.Get("demo"); ok {
			t.Fatalf("已清空的公告不应复活（%s）", h.Name)
		}
		if n, ok := s2.Get("book"); !ok || n.Content != "别房的" {
			t.Fatalf("邻房公告应留存（%s: %+v）", h.Name, n)
		}
		if n, ok := s2.Get("demo"); ok && n.Rev <= 0 {
			t.Fatalf("缺席读应为零值（%s）", h.Name)
		}
	})

	t.Run("content gates", func(t *testing.T) {
		s, _ := mk(t)
		if _, err := s.SetExpect("demo", "   ", "房主", -1); err == nil {
			t.Fatalf("空白公告应拒（%s）", h.Name)
		}
		long := make([]rune, notice.MaxContentRunes+1)
		for i := range long {
			long[i] = '字'
		}
		if _, err := s.SetExpect("demo", string(long), "房主", -1); err == nil {
			t.Fatalf("超长公告应拒（%s）", h.Name)
		}
	})
}
