package tasks

import (
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/projects"
)

// ts builds a Unix-second stamp from a local wall clock (test fixtures
// read as what the gantt ruler will label them).
func ts(y int, mo time.Month, d, h int) int64 {
	return time.Date(y, mo, d, h, 0, 0, 0, time.Local).Unix()
}

// LaneOrder pins the 呈现序 contract: lanes rise by their earliest bar
// start (name breaks ties) — the top of the gantt is where the schedule
// begins, not where the alphabet begins. Alphabetical order once stranded
// the only visible bars in bottom lanes under a wall of empty top lanes.
func TestLaneOrder(t *testing.T) {
	p := projects.Project{Key: "ord", Name: "排", Status: projects.StatusActive}
	now := time.Unix(ts(2026, 10, 1, 0), 0)
	list := []Task{
		{ID: "t_01", Title: "丙晚开工", Assignee: "丙", StartTS: ts(2026, 10, 20, 0), Status: StatusTodo},
		{ID: "t_02", Title: "甲最早", Assignee: "甲", StartTS: ts(2026, 10, 1, 0), Status: StatusTodo},
		{ID: "t_03", Title: "乙更晚", Assignee: "乙", StartTS: ts(2026, 10, 5, 0), Status: StatusTodo},
		{ID: "t_04", Title: "甲同刻", Assignee: "甲", StartTS: ts(2026, 10, 1, 0), Status: StatusTodo},
	}
	got := ScheduleView(p, nil, list, now).Lanes
	want := []string{"甲", "乙", "丙"}
	if len(got) != len(want) {
		t.Fatalf("lanes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].Assignee != want[i] {
			t.Fatalf("lane %d = %q, want %q (full order %v)", i, got[i].Assignee, want[i], got)
		}
	}
}

// ActualSpanAndEfficiency pins the AI-pace ledger: the log's 状态 A→B
// hops ARE the real clock (first →进行中 = hands on, last →已完成 =
// shipped), and the view's roll-up compares Σ计划 vs Σ实际 only over
// finished leaves that carry both. A same-second ship counts as one
// second — 提效 stays finite; a task without recorded hops never joins
// the roll-up.
func TestActualSpanAndEfficiency(t *testing.T) {
	p := projects.Project{Key: "eff", Name: "提效", Status: projects.StatusActive}
	now := time.Unix(ts(2026, 10, 1, 0), 0)
	base := ts(2026, 10, 1, 0)
	list := []Task{
		{ // 计划 2 天，实际 7 秒 —— the AI pace the card exists for
			ID: "t_01", Title: "快刀", Assignee: "甲", StartTS: base, EndTS: base + 2*86400,
			Status: StatusDone,
			Log: []LogEntry{
				{TS: base - 3600, By: "甲", Note: "创建任务，指派给 甲"},
				{TS: base + 100, By: "甲", Note: "状态 待处理→进行中"},
				{TS: base + 107, By: "甲", Note: "状态 进行中→已完成"},
			},
		},
		{ // same-second ship: spent clamps to 1s, still counted
			ID: "t_02", Title: "秒完", Assignee: "甲", StartTS: base, EndTS: base + 86400,
			Status: StatusDone,
			Log: []LogEntry{
				{TS: base + 10, By: "甲", Note: "状态 待处理→进行中 · 进度 50%"},
				{TS: base + 10, By: "甲", Note: "状态 进行中→已完成"},
			},
		},
		{ // finished but NO recorded hops: excluded from the roll-up
			ID: "t_03", Title: "老账", Assignee: "乙", StartTS: base, EndTS: base + 86400,
			Status: StatusDone,
		},
		{ // still running: carries actual_start, never joins Σ
			ID: "t_04", Title: "在办", Assignee: "乙", StartTS: base, EndTS: base + 86400,
			Status: StatusDoing,
			Log:    []LogEntry{{TS: base + 50, By: "乙", Note: "状态 待处理→进行中"}},
		},
	}
	view := ScheduleView(p, nil, list, now)
	byID := map[string]Bar{}
	for _, b := range view.Bars {
		byID[b.TaskID] = b
	}
	if got := byID["t_01"]; got.ActualStart != base+100 || got.ActualEnd != base+107 {
		t.Fatalf("t_01 actual = [%d,%d], want [%d,%d]", got.ActualStart, got.ActualEnd, base+100, base+107)
	}
	if got := byID["t_03"]; got.ActualStart != 0 || got.ActualEnd != 0 {
		t.Fatalf("t_03 (no hops) actual = [%d,%d], want [0,0]", got.ActualStart, got.ActualEnd)
	}
	if got := byID["t_04"]; got.ActualStart != base+50 || got.ActualEnd != 0 {
		t.Fatalf("t_04 (running) actual = [%d,%d], want [%d,0]", got.ActualStart, got.ActualEnd, base+50)
	}
	// roll-up: t_01 (172800 vs 7) + t_02 (86400 vs 1); t_03/t_04 excluded
	if view.ActualDone != 2 {
		t.Fatalf("ActualDone = %d, want 2", view.ActualDone)
	}
	if view.ActualPlanned != 172800+86400 {
		t.Fatalf("ActualPlanned = %d, want %d", view.ActualPlanned, 172800+86400)
	}
	if view.ActualSpent != 8 {
		t.Fatalf("ActualSpent = %d, want 8", view.ActualSpent)
	}
}

// TestActualSpanStructuredStamps: 结构化工作钟（doing_ts/done_ts）逐字段
// 优先于日志考古——新账不再以中文措辞为协议；旧账（无戳）照旧扫日志，
// 混账（一侧有戳）逐字段取优。
func TestActualSpanStructuredStamps(t *testing.T) {
	base := ts(2026, 10, 1, 0)
	// 新账：无日志也有真实钟
	if s, e := actualSpan(Task{DoingTS: base + 5, DoneTS: base + 9}); s != base+5 || e != base+9 {
		t.Fatalf("structured stamps = [%d,%d], want [+5,+9]", s-base, e-base)
	}
	// 混账：开始有戳优先，结束无戳由日志补
	if s, e := actualSpan(Task{DoingTS: base + 5,
		Log: []LogEntry{{TS: base + 20, Note: "状态 进行中→已完成"}}}); s != base+5 || e != base+20 {
		t.Fatalf("mixed = [%d,%d], want [+5,+20]", s-base, e-base)
	}
}

// TestApplyPatchStampsWorkClock: 状态翻转当场打结构化戳——首个进行中
// 定桩（后续保持不动戳），完成落终戳。
func TestApplyPatchStampsWorkClock(t *testing.T) {
	task := &Task{ID: "t_01", Status: StatusTodo}
	applyPatch(task, "甲", Patch{Status: StatusDoing}, schedPatch{}, 1000)
	if task.DoingTS != 1000 {
		t.Fatalf("DoingTS = %d, want 1000", task.DoingTS)
	}
	applyPatch(task, "甲", Patch{Status: StatusDoing}, schedPatch{}, 1050) // 状态保持：首戳不动
	if task.DoingTS != 1000 {
		t.Fatalf("first doing hop must win (got %d)", task.DoingTS)
	}
	applyPatch(task, "甲", Patch{Status: StatusDone}, schedPatch{}, 1100)
	if task.DoneTS != 1100 {
		t.Fatalf("DoneTS = %d, want 1100", task.DoneTS)
	}
}

// TestWorkClockRidesCopies（试用 B 抓出的漏拷回归钉）：applyPatch 落的
// 结构化工作钟必须随每个读面副本走——copyTask 漏掉 DoingTS/DoneTS 时
// Get/List/Outcome 全回 0，甘特实际跨度只能退回刮日志。
func TestWorkClockRidesCopies(t *testing.T) {
	e, err := OpenMemory("房主")
	if err != nil {
		t.Fatal(err)
	}
	out := e.Create("", "房主", "带钟任务", "", "")
	if out.Denied {
		t.Fatalf("建单被拒：%s", out.Reason)
	}
	id := out.Task.ID
	for _, st := range []string{StatusDoing, StatusDone} {
		if u := e.Update("", "房主", id, Patch{Status: st}); u.Denied {
			t.Fatalf("流转到 %s 被拒：%s", st, u.Reason)
		}
	}
	got, ok := e.GetIn("", id)
	if !ok {
		t.Fatal("任务应可读")
	}
	if got.DoingTS == 0 || got.DoneTS == 0 {
		t.Fatalf("工作钟应随副本走（DoingTS=%d DoneTS=%d）", got.DoingTS, got.DoneTS)
	}
	for _, l := range e.ListFiltered("", "", "default", "") {
		if l.ID == id && (l.DoingTS == 0 || l.DoneTS == 0) {
			t.Fatalf("List 副本也应带钟（DoingTS=%d DoneTS=%d）", l.DoingTS, l.DoneTS)
		}
	}
}
