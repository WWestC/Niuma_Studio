package tasks

// sweep_test.go — 卡死提案出清的兜底钉子：挂过 ProposalStaleAfter 且
// 无法再生效（世界从下面挪走——挂靠项目归档是实录形状）或需确认人
// 全部离场，才出清；新鲜、仍有效、还有人在岗的一律不动。指派类出清
// 连任务一起取消（decline 同语义）。

import (
	"strings"
	"testing"
	"time"
)

// agePending 把任务的挂起提案拨老（同一包内的测试缝）。
func agePending(t *testing.T, e *Engine, id string, ts int64) {
	t.Helper()
	for _, ch := range e.shardSnapshot() {
		ch.mu.Lock()
		for _, task := range ch.tasks {
			if task.ID == id && task.Pending != nil {
				task.Pending.TS = ts
			}
		}
		ch.mu.Unlock()
	}
}

func TestSweepStaleProposals(t *testing.T) {
	e := openEngine(t)
	e.SetProjectKeys(func() []string { return []string{"proj-x", ""} })
	if out := e.Create("proj-x", "房主", "挂靠单", "", "甲"); out.Denied {
		t.Fatal(out.Reason)
	}
	// 丙（平级）对甲名下的单子申请改进度——挂起等甲确认
	if out := e.Update("proj-x", "丙", "t_01", Patch{Progress: "40%"}); out.Denied {
		t.Fatal(out.Reason)
	}
	// 世界从下面挪走：项目归档，该提案从此无法生效
	e.SetProjectKeys(func() []string { return []string{""} })

	now := time.Now()
	aged := now.Add(-25 * time.Hour).Unix()

	// 未满龄：不动（哪怕已经无法生效）
	agePending(t, e, "t_01", now.Unix())
	if out := e.SweepStaleProposals("proj-x", now, nil); len(out) != 0 {
		t.Fatalf("未满龄不该出清（got %d）", len(out))
	}
	// 满龄且无法生效：出清，提案消失
	agePending(t, e, "t_01", aged)
	out := e.SweepStaleProposals("proj-x", now, nil)
	if len(out) != 1 || !strings.Contains(out[0].Text, "无法再生效") {
		t.Fatalf("满龄卡死应出清一条（got %+v）", out)
	}
	if cur, _ := e.GetIn("proj-x", "t_01"); cur.Pending != nil {
		t.Fatal("出清后提案应消失")
	}
}

func TestSweepKeepsLiveNegotiations(t *testing.T) {
	e := openEngine(t)
	if out := e.Create("", "房主", "健谈单", "", "甲"); out.Denied {
		t.Fatal(out.Reason)
	}
	if out := e.Update("", "丙", "t_01", Patch{Progress: "30%"}); out.Denied {
		t.Fatal(out.Reason)
	}
	now := time.Now()
	agePending(t, e, "t_01", now.Add(-25*time.Hour).Unix())

	// 仍有效且需确认人在岗：谈判不是卡死，不动
	if out := e.SweepStaleProposals("", now, func(string) bool { return true }); len(out) != 0 {
		t.Fatalf("仍有效且有人在岗不该出清（got %d）", len(out))
	}
	// 需确认人全部离场：出清
	if out := e.SweepStaleProposals("", now, func(string) bool { return false }); len(out) != 1 {
		t.Fatalf("需确认人全离场应出清（got %d）", len(out))
	}
	if cur, _ := e.GetIn("", "t_01"); cur.Pending != nil {
		t.Fatal("出清后提案应消失")
	}
}

func TestSweepAssignCancelsTask(t *testing.T) {
	e := openEngine(t)
	if out := e.CreatePlanned("丙", Task{Title: "提名单", Assignee: "甲"}); out.Denied {
		t.Fatal(out.Reason)
	}
	now := time.Now()
	agePending(t, e, "t_01", now.Add(-25*time.Hour).Unix())
	out := e.SweepStaleProposals("", now, func(string) bool { return false })
	if len(out) != 1 {
		t.Fatalf("指派提案应出清（got %d）", len(out))
	}
	cur, _ := e.GetIn("", "t_01")
	if cur.Status != StatusCancelled {
		t.Fatalf("指派出清应连任务取消（got %s）", cur.Status)
	}
}
