package dispatch

// patrol_test.go — r_16 t_179：PatrolNow 第五项「停滞检查」。util.SetNow
// 时钟缝——建单落 now、拨快 16 分钟（默认阈 15）即停滞；拨 6
// 分钟＋阈 5 同样停滞；pending 不入；既有四项零回退。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// patrolStage 装配带 tasks 引擎与编排者的 dispatcher（startAutoPilotStage
// 没配 Tasks——停滞检查的数据面），恢复全局时钟的清理挂在 Cleanup。
func patrolStage(t *testing.T) (*Dispatcher, *watchBridge, *staffing.Store) {
	t.Helper()
	realNow := util.NowFn()
	t.Cleanup(func() { util.SetNow(realNow) })
	patrolNowBase = realNow()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	engine := tasks.MustOpenMemory("")
	engine.SetProjectKeys(func() []string { return []string{"proj-ap", "default"} })
	wb := &watchBridge{}
	d := Start(hub, store, wb, Config{
		Workspace:    t.TempDir(),
		InboxDir:     t.TempDir(),
		PatrolEvery:  -1,
		DailyAt:      "off",
		MeetingEvery: -1,
		WatchdogTick: make(chan time.Time),
		ProjectKey:   "proj-ap",
		StaffStore:   staff,
		Tasks:        engine,
	})
	t.Cleanup(d.Stop)
	if err := d.attach("小牛", agents.OrchestratorRole, "s-pt-1"); err != nil {
		t.Fatal(err)
	}
	return d, wb, staff
}

var patrolNowBase int64 // patrolStage 设的时钟基线

// mkDoing 建一条 doing 任务挂本项目（UpdatedTS=当前缝时钟）。
func mkDoing(t *testing.T, d *Dispatcher, title string) string {
	t.Helper()
	oc := d.cfg.Tasks.Create("", "小猿", title, "", "小猿")
	if oc.Denied {
		t.Fatal(oc.Reason)
	}
	up := d.cfg.Tasks.Update("", "小猿", oc.Task.ID, tasks.Patch{Project: d.projectKey, Status: tasks.StatusDoing})
	if up.Denied {
		t.Fatal(up.Reason)
	}
	return oc.Task.ID
}

// TestPatrolStall16Min：16 分钟旧单出现（默认阈 15）。
func TestPatrolStall16Min(t *testing.T) {
	d, wb, _ := patrolStage(t)
	id := mkDoing(t, d, "停滞单")
	// 拨快 16 分钟——UpdatedTS 留在 16 分钟前
	util.SetNow(func() int64 { return patrolNowBase + 16*60 })
	d.PatrolNow()
	if !strings.Contains(wb.sentText(), id) || !strings.Contains(wb.sentText(), "停滞") {
		t.Fatalf("16 分钟旧单应入停滞清单: %q", wb.sentText())
	}
}

// TestPatrolStallFreshNotListed：新单（0 分钟）不出现。
func TestPatrolStallFreshNotListed(t *testing.T) {
	d, wb, _ := patrolStage(t)
	id := mkDoing(t, d, "新鲜单")
	d.PatrolNow()
	if strings.Contains(wb.sentText(), id) {
		t.Fatalf("新单不该入停滞清单: %q", wb.sentText())
	}
}

// TestPatrolStallThreshold5：阈 5（设置档）＋6 分钟旧单出现。
func TestPatrolStallThreshold5(t *testing.T) {
	d, wb, staff := patrolStage(t)
	staff.SetPatrolStallMin(d.projectKey, 5)
	id := mkDoing(t, d, "六分钟单")
	util.SetNow(func() int64 { return patrolNowBase + 6*60 })
	d.PatrolNow()
	if !strings.Contains(wb.sentText(), id) {
		t.Fatalf("阈 5 时 6 分钟单应停滞: %q", wb.sentText())
	}
	if !strings.Contains(wb.sentText(), "阈值 5 分钟") {
		t.Fatalf("清单应带当前阈值: %q", wb.sentText())
	}
}

// ── r_14 微迭代（t_181 验收遗留五条款）──────────────────────────────

// TestPatrolStallRowThreeColumns：定稿 §二三列格式——「t_NN｜负责人｜已停
// M 分钟」，标题不再进清单行；负责人不在房名册带（离线）标注。
func TestPatrolStallRowThreeColumns(t *testing.T) {
	d, wb, _ := patrolStage(t)
	id := mkDoing(t, d, "离线负责人单")
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	d.PatrolNow()
	sent := wb.sentText()
	if !strings.Contains(sent, id+"｜小猿（离线）｜已停 20 分钟") {
		t.Fatalf("清单行应为三列＋离线标注: %q", sent)
	}
	if strings.Contains(sent, "离线负责人单") {
		t.Fatalf("标题不进清单行（定稿三列无标题列）: %q", sent)
	}
	if !strings.Contains(sent, "停滞清单（阈值 15 分钟）：") {
		t.Fatalf("清单段标题行应在: %q", sent)
	}
}

// TestPatrolStallOnlineNoMark：负责人在房名册（有座）时不带离线标注。
func TestPatrolStallOnlineNoMark(t *testing.T) {
	d, wb, _ := patrolStage(t)
	// 小猿坐进房——名册在场即「在线」（宽限幽灵同算，这里测活座）
	d.hub.Join("小猿", "开发", false)
	id := mkDoing(t, d, "在线负责人单")
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	d.PatrolNow()
	if !strings.Contains(wb.sentText(), id+"｜小猿｜已停 20 分钟") {
		t.Fatalf("在房负责人应无（离线）标注: %q", wb.sentText())
	}
}

// TestPatrolStallRecruitSkipped：「待招·」占位单直接跳过——催空座没有
// 意义。走 CreatePlanned 落单（跨人指派走 Create 会变待确认，那是
// pending 豁免的辖区，不是这条的测法）。
func TestPatrolStallRecruitSkipped(t *testing.T) {
	d, wb, _ := patrolStage(t)
	oc := d.cfg.Tasks.CreatePlanned("小牛", tasks.Task{
		Title: "占位单", Assignee: "待招·测试", ProjectKey: d.projectKey,
	})
	if oc.Denied {
		t.Fatal(oc.Reason)
	}
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	d.PatrolNow()
	if strings.Contains(wb.sentText(), oc.Task.ID) {
		t.Fatalf("待招占位单不该入停滞清单: %q", wb.sentText())
	}
}

// TestPatrolStallSecondRoundAgain：同单连续第二轮标（再）——编排者升级
// 处置而非重复催（定稿 §二纪律）。
func TestPatrolStallSecondRoundAgain(t *testing.T) {
	d, wb, _ := patrolStage(t)
	mkDoing(t, d, "连续停滞单")
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	d.PatrolNow()
	if strings.Contains(wb.sentText(), "（再）") {
		t.Fatalf("首轮不该有（再）标记: %q", wb.sentText())
	}
	util.SetNow(func() int64 { return patrolNowBase + 40*60 })
	d.PatrolNow()
	if !strings.Contains(wb.sentText(), "（再）") {
		t.Fatalf("第二轮同单应标（再）: %q", wb.sentText())
	}
}

// TestPatrolStallResumedMarked：续命账目在 15min 记忆窗内 → 清单标
// （已续命）不再裸催（定稿 §三去重规则）；窗外记号退场。
func TestPatrolStallResumedMarked(t *testing.T) {
	d, wb, _ := patrolStage(t)
	id := mkDoing(t, d, "续命单")
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	d.mu.Lock()
	d.resumeMark[id] = util.Now()
	d.mu.Unlock()
	d.PatrolNow()
	if !strings.Contains(wb.sentText(), "（已续命）") {
		t.Fatalf("续命过的单应标（已续命）: %q", wb.sentText())
	}
	// 记号过期（拨到窗外再巡）——裸清单回归。sentText 是累计册，只看
	// 最后一轮的注入段。
	util.SetNow(func() int64 { return patrolNowBase + 20*60 + 16*60 })
	d.PatrolNow()
	all := wb.sentText()
	sent := all[strings.LastIndex(all, "【巡检】"):]
	if strings.Contains(sent, "（已续命）") {
		t.Fatalf("15min 窗外的续命记号应退场: %q", sent)
	}
	if !strings.Contains(sent, id) {
		t.Fatalf("单子仍在清单（只是不再标已续命）: %q", sent)
	}
}

// TestAutoPilotResumeStampsMark：续命注入送达即落账——resumeMark 有该单
// （清单去重的数据面）。
func TestAutoPilotResumeStampsMark(t *testing.T) {
	d, wb, _ := patrolStage(t)
	if err := d.attach("小猿", "开发", "s-stall-1"); err != nil {
		t.Fatal(err)
	}
	id := mkDoing(t, d, "续命落账单")
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	var stalled []*tasks.Task
	for _, t := range d.cfg.Tasks.ListFiltered("", tasks.StatusDoing, d.projectKey, "") {
		if t.ID == id {
			stalled = append(stalled, &t)
		}
	}
	if err := d.AutoPilotResume("小猿", stalled); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	_, marked := d.resumeMark[id]
	d.mu.Unlock()
	if !marked {
		t.Fatalf("续命送达应落 resumeMark: %q", wb.sentText())
	}
}

// TestPatrolStallPendingSkipped：待确认变更（Pending）不入停滞。
func TestPatrolStallPendingSkipped(t *testing.T) {
	d, wb, _ := patrolStage(t)
	// 同 rank 提名 → pending 状态
	oc := d.cfg.Tasks.Create("", "小猿", "待确认单", "", "小猿")
	if oc.Denied {
		t.Fatal(oc.Reason)
	}
	// 该任务已是 pending（提名待确认）——拨 20 分钟后不应出现在停滞清单
	util.SetNow(func() int64 { return patrolNowBase + 20*60 })
	d.PatrolNow()
	if strings.Contains(wb.sentText(), "待确认单") {
		t.Fatalf("pending 不该入停滞清单: %q", wb.sentText())
	}
}

// TestPatrolFourChecksZeroRegress：既有四项检查的注入格式零回退。
func TestPatrolFourChecksZeroRegress(t *testing.T) {
	d, wb, _ := patrolStage(t)
	d.PatrolNow()
	sent := wb.sentText()
	for _, want := range []string{"【巡检】", "逾期未完", "blocked 超阈", "待排期", "提案滞留"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("注入缺既有检查 %q: %q", want, sent)
		}
	}
}
