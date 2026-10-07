package sqlstore

import (
	"context"
	"testing"

	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// tx_test.go — the acceptance core: 跨 store 事务有测试证明。JSON 时
// 代「三处写中途死」必然留半截账（三个文件各自原子、彼此无关）；
// 单库后三写是一个 BEGIN/COMMIT，中途死＝整体回滚。The promotion
// moved the proof one level up: the seams are dumb document stores, so
// the interesting atomicity is (a) seam writes in one Tx and (b) the
// ENGINES' staged accept (server acceptPlanCore's shape) — staging,
// writing through the tx, and adopting memory only after commit.

func TestCrossStoreSeamCommit(t *testing.T) {
	d, _ := openDB(t)
	defer d.Close()
	err := d.Tx(func(tx *TxStores) error {
		if err := tx.Plan().SaveSlot("", "demo", &plan.Slot{Next: 4}); err != nil {
			return err
		}
		if err := tx.Tasks().SaveShelf("", "demo", &tasks.Shelf{Next: 2, Tasks: []*tasks.Task{
			{ID: "t_01", Title: "三写之任务", ProjectKey: "demo", Status: tasks.StatusTodo},
		}}); err != nil {
			return err
		}
		return tx.Requirements().SaveShelf("", "demo", &requirements.Shelf{Next: 2,
			Reqs: []requirements.Req{{ID: "r_01", ProjectKey: "demo", Title: "三写之需求", Status: requirements.StatusSplit}}})
	})
	if err != nil {
		t.Fatalf("三写事务应成立: %v", err)
	}
	// 提交过的三写在重启（新句柄）后仍在——WAL 的持久性承诺
	d2, err := Open(d.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if s, err := requirements.OpenStore(d2.ReqShelves(), ""); err != nil || len(s.List("")) != 1 {
		t.Fatalf("重启后需求架应仍有一条: %+v %v", s.List(""), err)
	}
	if p, err := plan.OpenStore(d2.PlanSlots(), ""); err != nil || p.Pending("", "demo") != nil {
		t.Fatal("重启后提案槽应为空槽（计数在）")
	}
}

// TestCrossStoreSeamRollback — 第三写被数据库拒绝（NOT NULL 撞墙）：
// 前两处全部回滚，一处不留。
func TestCrossStoreSeamRollback(t *testing.T) {
	d, _ := openDB(t)
	defer d.Close()
	err := d.Tx(func(tx *TxStores) error {
		if err := tx.Plan().SaveSlot("", "demo", &plan.Slot{Next: 2}); err != nil {
			return err
		}
		if err := tx.Tasks().SaveShelf("", "demo", &tasks.Shelf{Next: 1}); err != nil {
			return err
		}
		// NOT NULL 违例：notice 行的 doc 不给值
		_, err := tx.q.ExecContext(context.Background(),
			`INSERT INTO notice_rows (ns, room, doc) VALUES ('', 'demo', NULL)`)
		return err
	})
	if err == nil {
		t.Fatal("第三写应被数据库拒绝")
	}
	plans, perr := plan.OpenStore(d.PlanSlots(), "")
	if perr != nil {
		t.Fatal(perr)
	}
	if p := plans.Pending("", "demo"); p != nil {
		t.Fatalf("回滚后第一写不应可见: %+v", p)
	}
	if slots, err := d.PlanSlots().OpenSlots(""); err != nil || len(slots) != 0 {
		t.Fatalf("回滚后槽表应空: %+v %v", slots, err)
	}
}

// TestStagedAcceptAtomicity — the accept path's shape: stage all three
// legs (plan pop + task batch + req flip), write them in ONE tx, adopt
// memory after commit. A failed tx aborts everything — the slot
// stands, no task exists, the requirement is untouched; a committed
// tx leaves all three landed and durable.
func TestStagedAcceptAtomicity(t *testing.T) {
	d, _ := openDB(t)
	defer d.Close()
	reqs, err := requirements.OpenStore(d.ReqShelves(), "")
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plan.OpenStore(d.PlanSlots(), "")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := tasks.OpenStoreNS(d.Tasks(), "", "房主")
	if err != nil {
		t.Fatal(err)
	}
	eng.SetProjectKeys(func() []string { return []string{"demo"} })
	if _, err := reqs.Create("", "demo", "待拆解的需求", "", "编排者"); err != nil {
		t.Fatal(err)
	}
	stored, _ := plans.Submit("", "demo", "编排者", plan.Plan{Title: "提案",
		Req: "r_01", Tasks: []plan.PlanTask{{Title: "活一"}, {Title: "活二"}}}, 100)
	if stored == nil {
		t.Fatal("提案应已入槽")
	}

	runAccept := func(fail bool) {
		if _, err := plans.AcceptBegin("", "demo", stored.ID); err != nil {
			t.Fatal(err)
		}
		eng.AcceptBegin()
		if err := reqs.AcceptBegin("", "demo", "r_01"); err != nil {
			t.Fatal(err)
		}
		reqStaged := true
		werr := d.Tx(func(tx *TxStores) error {
			if err := plans.AcceptWrite(tx.Plan()); err != nil {
				return err
			}
			if err := eng.AcceptCreatePlanned("房主", tasks.Task{Title: "活", ProjectKey: "demo"}); err.Denied {
				t.Fatalf("入库被拒: %s", err.Reason)
			}
			if err := eng.AcceptWrite(tx.Tasks()); err != nil {
				return err
			}
			if reqStaged {
				if err := reqs.AcceptWrite(tx.Requirements()); err != nil {
					return err
				}
			}
			if fail {
				_, err := tx.q.ExecContext(context.Background(),
					`INSERT INTO notice_rows (ns, room, doc) VALUES ('', 'demo', NULL)`)
				return err
			}
			return nil
		})
		if werr != nil {
			plans.AcceptAbort()
			eng.AcceptAbort()
			reqs.AcceptAbort()
			return
		}
		plans.AcceptCommit()
		eng.AcceptCommit()
		reqs.AcceptCommit()
	}

	// 第一轮：事务失败 → 全批弃置（槽在、任务零、需求 open）
	runAccept(true)
	if p := plans.Pending("", "demo"); p == nil || p.ID != stored.ID {
		t.Fatalf("失败后提案应仍在槽内: %+v", p)
	}
	if got := eng.Count(); got != 0 {
		t.Fatalf("失败后不应有任务: %d", got)
	}
	if r, ok := reqs.GetIn("", "demo", "r_01"); !ok || r.Status != requirements.StatusOpen {
		t.Fatalf("失败后需求应仍 open: %+v", r)
	}
	// 第二轮：事务成立 → 三腿全落地且跨重启
	runAccept(false)
	if p := plans.Pending("", "demo"); p != nil {
		t.Fatalf("成立后槽应空: %+v", p)
	}
	if got := eng.Count(); got != 1 {
		t.Fatalf("成立后应有一张任务: %d", got)
	}
	if r, ok := reqs.GetIn("", "demo", "r_01"); !ok || r.Status != requirements.StatusSplit {
		t.Fatalf("成立后需求应 split: %+v", r)
	}
	d2, err := Open(d.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	reqs2, _ := requirements.OpenStore(d2.ReqShelves(), "")
	if r, ok := reqs2.GetIn("", "demo", "r_01"); !ok || r.Status != requirements.StatusSplit {
		t.Fatalf("重启后需求应仍 split: %+v", r)
	}
}
