package sqlstore_test

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/storetest"
)

// ns_test.go — the namespace column isolates through the engines'
// bound SUBJECTS (the per-call namespace the seams carry; "" is the
// single-user historical shape). Two subjects over one file behave as
// two ledgers: same ids, same counters, no cross-talk — and the
// default ("") namespace sees neither.
func TestNamespaceIsolation(t *testing.T) {
	open, _ := storetest.ReqSQLiteBacking(t)
	a := open("alice")
	b := open("bob")

	if _, err := a.Create("alice", "demo", "甲的 r_01", "", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create("bob", "demo", "乙的 r_01", "", "bob"); err != nil {
		t.Fatal(err)
	}
	x, ok := a.GetIn("alice", "demo", "r_01")
	if !ok || x.Title != "甲的 r_01" {
		t.Fatalf("alice 主体应只读自己的行：%+v", x)
	}
	if got := a.List("alice"); len(got) != 1 {
		t.Fatalf("alice 主体应只见自己的列表：%+v", got)
	}
	// 计数器各自走：两边各自的第二条都是 r_02
	if id, _ := a.Create("alice", "demo", "甲的第二条", "", "alice"); id.ID != "r_02" {
		t.Fatalf("alice 计数应独立推进：%s", id.ID)
	}
	if id, _ := b.Create("bob", "demo", "乙的第二条", "", "bob"); id.ID != "r_02" {
		t.Fatalf("bob 计数应独立推进：%s", id.ID)
	}
	// 默认命名空间（单用户态）与两者互不可见，从 r_01 起算
	e := open("")
	if got := e.List(""); len(got) != 0 {
		t.Fatalf("默认命名空间不应看到具名主体的行：%+v", got)
	}
	if id, _ := e.Create("", "demo", "默认的第一条", "", "房主"); id.ID != "r_01" {
		t.Fatalf("默认空间应从 r_01 起算：%s", id.ID)
	}
	// rev 也是按主体隔离的（变更检测不串门）
	if a.Rev("alice") == 0 || b.Rev("bob") == 0 {
		t.Fatal("主体的 rev 应各自计数")
	}
}
