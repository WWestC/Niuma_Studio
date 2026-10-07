package storetest

// meeting.go — the meeting ledger's contract suite: the occupancy
// gate, the m_NN high-water across restart, the capped finished
// history, project wipe. The live meeting is memory-only BY DESIGN —
// only the finished history rides the backing.

import (
	"fmt"
	"testing"

	"github.com/WWestC/Niuma_Studio/meeting"
)

// MeetingHarness wires one backing into the meeting suite.
type MeetingHarness struct {
	Name string
	// OpenBacking: open yields one engine bound to one subject, reopen
	// reconstructs over the same durable backing (subject "" rides the
	// single-user binding; the engine's face carries no subject).
	OpenBacking func(t *testing.T) (open func(subject string) *meeting.Engine, reopen func(subject string) func(*testing.T) *meeting.Engine)
}

// TestMeetingStore is the meeting domain's contract suite.
func TestMeetingStore(t *testing.T, h MeetingHarness) {
	if h.OpenBacking == nil {
		t.Fatalf("%s: harness 无 OpenBacking", h.Name)
	}
	mk := func(t *testing.T) (*meeting.Engine, func(*testing.T) *meeting.Engine) {
		open, reopen := h.OpenBacking(t)
		return open(""), reopen("")
	}

	t.Run("occupancy gate and history", func(t *testing.T) {
		s, reopen := mk(t)
		m1, err := s.Begin("demo", "r_01", "标题", "编排者", []string{"产品"})
		if err != nil || m1.ID != "m_01" || m1.Status != meeting.StatusLive {
			t.Fatalf("首会应成立并盖章 m_01（%s: %+v %v）", h.Name, m1, err)
		}
		if _, err := s.Begin("demo", "r_02", "标题", "编排者", nil); err == nil {
			t.Fatalf("在会时第二场应拒（%s）", h.Name)
		}
		s.Line("demo", "产品", "意见一句")
		done, ok := s.End("demo")
		if !ok || done.Status != meeting.StatusDone || len(done.Transcript) != 1 {
			t.Fatalf("散会应入史（%s: %+v）", h.Name, done)
		}
		s2 := reopen(t)
		if got := s2.History("demo", 0); len(got) != 1 || got[0].ID != "m_01" {
			t.Fatalf("重开后历史应在（%s: %+v）", h.Name, got)
		}
		// 高水位：删过的号不复生（m_01 已耗，下一场 m_02 起）
		if m2, err := s2.Begin("demo", "r_02", "标题", "编排者", nil); err != nil || m2.ID != "m_02" {
			t.Fatalf("计数应续走 m_02（%s: %+v %v）", h.Name, m2, err)
		}
	})

	t.Run("shelf isolation and drop", func(t *testing.T) {
		s, _ := mk(t)
		if _, err := s.Begin("book", "r_01", "标题", "编排者", nil); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.End("book"); !ok {
			t.Fatal("book 散会应入史")
		}
		if _, err := s.Begin("demo", "r_01", "标题", "编排者", nil); err != nil {
			t.Fatal(err)
		}
		s.End("demo")
		if n := s.DropProject("demo"); n != 1 {
			t.Fatalf("Drop 应报一场入史（%s: %d）", h.Name, n)
		}
		if got := s.History("demo", 0); len(got) != 0 {
			t.Fatalf("清架后历史应空（%s）", h.Name)
		}
		if got := s.History("book", 0); len(got) != 1 {
			t.Fatalf("邻架不随删（%s）", h.Name)
		}
		// 清架后 m_01 重排
		if m, err := s.Begin("demo", "r_09", "标题", "编排者", nil); err != nil || m.ID != "m_01" {
			t.Fatalf("清架后应从 m_01 重排（%s: %+v %v）", h.Name, m, err)
		}
	})

	t.Run("reviewed at", func(t *testing.T) {
		s, reopen := mk(t)
		for i := 0; i < 2; i++ {
			if _, err := s.Begin("demo", fmt.Sprintf("r_%02d", i+1), "标题", "编排者", nil); err != nil {
				t.Fatal(err)
			}
			s.End("demo")
		}
		s2 := reopen(t)
		at := s2.ReviewedAt("demo")
		if len(at) != 2 || at["r_01"] == 0 || at["r_02"] == 0 {
			t.Fatalf("复查时间应按需求记（%s: %+v）", h.Name, at)
		}
	})
}
