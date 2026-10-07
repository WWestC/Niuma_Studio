package dispatch

// 提案入库的定向唤醒（v2 P4-b 补上的发布腿）：plan_accept 的广播帧
// 不带 @、唤不醒任何人，Dispatcher.PlanLanded 把「发布」真正发出去——
// 每个受派负责人一封【任务发布】（同人多单合一封，待确认的提名附
// 确认动词），编排者一封【提案入库】全量去向图（任务池与自己名下的
// 单都归它的图）；不在调度中的名字（房主/人类/笔误）静默跳过，图上
// 仍看得到去向。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/tasks"
)

func TestPlanLandedWakesAssigneesAndOrchestrator(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	ab := &ackBridge{}
	d := Start(hub, store, ab, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("小牛", agents.OrchestratorRole, "s-pl-orch"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("张三", "工程师", "s-pl-eng"); err != nil {
		t.Fatal(err)
	}

	pending := &tasks.Proposal{Kind: tasks.ProposalAssign, Need: []string{"张三"}}
	landed := []*tasks.Task{
		{ID: "t_01", Title: "直派单", Assignee: "张三"},
		{ID: "t_02", Title: "提名单", Assignee: "张三", Pending: pending},
		{ID: "t_03", Title: "池子单"},                 // 任务池：归编排者的图
		{ID: "t_04", Title: "自领单", Assignee: "小牛"}, // 编排者自己名下
		{ID: "t_05", Title: "外人单", Assignee: "李四"}, // 非调度成员：跳过但上图
	}
	d.PlanLanded("p_01", "拆解标题", "小牛", landed)

	waitFor(t, func() bool { return ab.sendCount() == 2 }, "应恰好注入两封：张三一封、小牛一封")
	var engLine, orchLine string
	for _, s := range ab.allSends() {
		if strings.Contains(s, "【任务发布") {
			engLine = s
		}
		if strings.Contains(s, "【提案入库") {
			orchLine = s
		}
	}
	if engLine == "" || orchLine == "" {
		t.Fatalf("注入缺型：eng=%q orch=%q", engLine, orchLine)
	}
	// 张三：同人多单合一封；直派单已生效且带开工/关单动令，提名单给确认动词
	for _, want := range []string{
		"t_01「直派单」", "已生效",
		"task update t_01 --name 张三 --status doing",
		"task update t_01 --name 张三 --status done",
		"t_02「提名单」", "待你确认", "task confirm t_02 --name 张三",
	} {
		if !strings.Contains(engLine, want) {
			t.Errorf("张三的注入缺 %q：\n%s", want, engLine)
		}
	}
	// 小牛：全量去向图——池子、自己、外人都看得见，提名有标注
	for _, want := range []string{
		"t_01「直派单」→ 张三",
		"t_02「提名单」→ 张三（待其确认接收）",
		"t_03「池子单」→ 任务池",
		"t_04「自领单」→ 你",
		"t_05「外人单」→ 李四",
	} {
		if !strings.Contains(orchLine, want) {
			t.Errorf("小牛的注入缺 %q：\n%s", want, orchLine)
		}
	}
}
