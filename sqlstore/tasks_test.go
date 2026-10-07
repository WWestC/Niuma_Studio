package sqlstore

// tasks_test.go — the tasks side's own guards (implementation-specific,
// not contract-portable): a corrupt shelf document must refuse to load
// (the boot corrupt-reset path's entry condition — silently skipping a
// bad shelf would continue on a quiet partial ledger), the forensic
// export + reset helpers must actually round-trip, and the two
// namespaces must not see each other's shelves or ranks (the ns
// discipline the columns reserve for Phase 3).

import (
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/tasks"
)

func TestTaskShelfCorruptDocRefuses(t *testing.T) {
	d, _ := openDB(t)
	defer d.Close()
	if err := d.Tasks().SaveShelf("", "demo", &tasks.Shelf{Next: 2, Tasks: []*tasks.Task{
		{ID: "t_01", Title: "好架", ProjectKey: "demo", Status: tasks.StatusTodo},
	}}); err != nil {
		t.Fatal(err)
	}
	// 直接落一份坏文档进库（模拟中途损坏/手改）
	if _, err := d.db.Exec(`INSERT INTO tasks_shelves (ns, project, next, doc)
		VALUES (?, ?, ?, ?)`, "", "bad", 2, `{"broken json`); err != nil {
		t.Fatal(err)
	}
	shelves, _, err := d.Tasks().OpenShelves("")
	if err == nil {
		t.Fatalf("坏文档必须拒开（绝不在静默半架上继续）: %+v", shelves)
	}
	// 取证导出＋清表重开（boot corrupt-reset 的两步）：坏字节留档、
	// 好架一并导出（取证是全量快照）、重开后引擎可开。
	backup := filepath.Join(t.TempDir(), "tasks-corrupt.json")
	if err := d.ExportLedgerSnapshot(backup); err != nil {
		t.Fatalf("取证导出: %v", err)
	}
	if err := d.ResetTablesNS([]string{"tasks_shelves", "task_ranks"}, ""); err != nil {
		t.Fatalf("重置: %v", err)
	}
	if _, _, err := d.Tasks().OpenShelves(""); err != nil {
		t.Fatalf("重置后应可开: %v", err)
	}
	e, err := tasks.OpenStore(d.Tasks(), "房主")
	if err != nil {
		t.Fatalf("重置后引擎应开: %v", err)
	}
	if got := e.Count(); got != 0 {
		t.Fatalf("重置后应空台账: %d", got)
	}
}

func TestTaskNamespaceIsolation(t *testing.T) {
	d, _ := openDB(t)
	defer d.Close()
	// 两个主体各自一房一架：引擎按主体绑定（OpenStoreNS），存储按主体落 ns。
	for _, subj := range []string{"", "alice"} {
		if err := d.Tasks().SaveShelf(subj, "demo", &tasks.Shelf{Next: 2, Tasks: []*tasks.Task{
			{ID: "t_01", Title: "自家的", ProjectKey: "demo", Status: tasks.StatusTodo},
		}}); err != nil {
			t.Fatal(err)
		}
		if err := d.Tasks().SaveRanks(subj, map[string]int{"小明": 5}); err != nil {
			t.Fatal(err)
		}
	}
	ea, err := tasks.OpenStoreNS(d.Tasks(), "", "房主")
	if err != nil {
		t.Fatal(err)
	}
	eb, err := tasks.OpenStoreNS(d.Tasks(), "alice", "房主")
	if err != nil {
		t.Fatal(err)
	}
	if got := ea.Count(); got != 1 || eb.Count() != 1 {
		t.Fatalf("两主体各自一张: %d %d", ea.Count(), eb.Count())
	}
	if ea.Rank("小明") != 5 || eb.Rank("小明") != 5 {
		t.Fatal("两主体各自一行等级")
	}
	if ea.Subject() != "" || eb.Subject() != "alice" {
		t.Fatalf("引擎主体绑定: %q %q", ea.Subject(), eb.Subject())
	}
	// 互不串门：默认侧再写不进 alice 侧的架
	if err := d.Tasks().SaveShelf("", "only-default", &tasks.Shelf{Next: 1}); err != nil {
		t.Fatal(err)
	}
	if shelves, _, err := d.Tasks().OpenShelves("alice"); err != nil || len(shelves) != 1 {
		t.Fatalf("alice 侧只见自家的架: %+v %v", shelves, err)
	}
	if _, ok := eb.GetIn("only-default", "t_01"); ok {
		t.Fatal("alice 侧解析不到默认侧的架")
	}
}
