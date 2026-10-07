package merge_test

// merge_test.go — 合并提案单槽的钉子：Submit 盖章（m_NN）/顶替、Take
// 的一次性 id 门、持久化往返。plan.Store 同款形状的姊妹用例。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/storetest"
)

func TestStoreSubmitStampAndSupersede(t *testing.T) {
	s := merge.OpenMemory()
	stored, sup := s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-a", Into: "main"}, 100)
	if stored.ID != "m_01" || stored.SubmittedBy != "编排者" || stored.SubmittedTS != 100 {
		t.Fatalf("盖章不合预期：%+v", stored)
	}
	if sup != nil {
		t.Fatal("首件无被顶替者")
	}
	stored2, sup2 := s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-b", Into: "main"}, 200)
	if stored2.ID != "m_02" || sup2 == nil || sup2.ID != "m_01" {
		t.Fatalf("第二件应顶替 m_01：%+v sup=%+v", stored2, sup2)
	}
	// 项目间互不干扰
	if _, sup3 := s.Submit("", "proj-y", "编排者", merge.Merge{Branch: "feat-c", Into: "main"}, 300); sup3 != nil {
		t.Fatalf("proj-y 的首件不应有被顶替者：%+v", sup3)
	}
	if ids := s.PendingIDs(""); len(ids) != 2 {
		t.Fatalf("两个项目各持一件：%v", ids)
	}
}

func TestStoreTakeIDGate(t *testing.T) {
	s := merge.OpenMemory()
	s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-a", Into: "main"}, 100)
	if _, err := s.Take("", "proj-x", "m_99"); err == nil {
		t.Fatal("陈旧 id 不应消费到新件")
	}
	m, err := s.Take("", "proj-x", "m_01")
	if err != nil || m.Branch != "feat-a" {
		t.Fatalf("对口 id 应取到：%v %+v", err, m)
	}
	if s.Pending("", "proj-x") != nil {
		t.Fatal("Take 后槽应空")
	}
	if _, err := s.Take("", "proj-x", "m_01"); err == nil {
		t.Fatal("二次 Take 应报无待审件")
	}
}

func TestStorePersistenceRoundTrip(t *testing.T) {
	open, reopenB := storetest.MergeSQLiteBacking(t)
	s := open("")
	s.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-a", Into: "main",
		Commits: 3, Files: 5, Additions: 120, Deletions: 30, Tasks: []string{"t_1", "r_2"}}, 100)
	s2 := reopenB("")(t)
	m := s2.Pending("", "proj-x")
	if m == nil || m.ID != "m_01" || m.Commits != 3 || len(m.Tasks) != 2 {
		t.Fatalf("重开后待审件应原样：%+v", m)
	}
	stored, _ := s2.Submit("", "proj-x", "编排者", merge.Merge{Branch: "feat-b", Into: "main"}, 200)
	if stored.ID != "m_02" {
		t.Fatalf("计数应续走：m_02，得 %s", stored.ID)
	}
}
