package dispatch

// mentor_test.go — r_28（t_221）：导师指派代码化判定的三分支钉子＋
// JoinPrompt/WelcomeBackPrompt 模板断言（验收②锚「四步引导」行）。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

func mentorStage(t *testing.T) (*Dispatcher, *tasks.Engine) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	eng := tasks.MustOpenMemory("房主")
	d := Start(hub, store, &recBridge{got: map[string][]string{}}, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
		Tasks:       eng,
	})
	t.Cleanup(d.Stop)
	return d, eng
}

func doneTask(t *testing.T, eng *tasks.Engine, assignee, title string) {
	t.Helper()
	out := eng.Create("", "房主", title, "", assignee)
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if up := eng.Update("", "房主", out.Task.ID, tasks.Patch{Status: tasks.StatusDone}); up.Denied {
		t.Fatal(up.Reason)
	}
}

// ① 同岗唯一在岗：直接指派。
func TestPickMentorSameRoleSole(t *testing.T) {
	d, eng := mentorStage(t)
	_ = eng
	if err := d.attach("老前端", "前端开发", "s-m1"); err != nil {
		t.Fatal(err)
	}
	mentor, hr := d.pickMentor("新前端", "前端开发")
	if hr || mentor != "老前端" {
		t.Fatalf("同岗唯一应指派老前端（got %q hr=%v）", mentor, hr)
	}
	line := d.mentorLine("新前端", mentor, "前端开发")
	if !strings.Contains(line, "@老前端") || !strings.Contains(line, "答疑") || !strings.Contains(line, "不陪跑") {
		t.Fatalf("导师行应含 @导师、答一次疑、不陪跑：%s", line)
	}
}

// ② 同岗多人：选最近完工者（台账最后一次 done）。
func TestPickMentorMostRecentDone(t *testing.T) {
	d, eng := mentorStage(t)
	if err := d.attach("甲前端", "前端开发", "s-m2"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("乙前端", "前端开发", "s-m3"); err != nil {
		t.Fatal(err)
	}
	// 甲更近完工（后 done 且拨 60s 拉开）——名字码位「乙<甲」，时间序
	// 与名字序**相反**：只有真正的「最近完工者」判定会选甲——名字序
	// 兜底或排序被废都会选乙。这是 t_222 验收实录的教训：初版测试
	// 「乙后 done」与名字序同结果，破坏性抽验废掉时间排序照样绿——
	// 两种排序必须有区分度，抽验才是在测真东西。
	base := util.Now()
	util.SetNow(func() int64 { return base + 60 })
	defer func() { util.SetNow(func() int64 { return time.Now().Unix() }) }()
	doneTask(t, eng, "甲前端", "甲的新活") // 甲后 done → 时间序胜者
	util.SetNow(func() int64 { return time.Now().Unix() })
	doneTask(t, eng, "乙前端", "乙的旧活") // 乙先 done（名字序胜者——不该赢）
	mentor, hr := d.pickMentor("新前端", "前端开发")
	if hr || mentor != "甲前端" {
		t.Fatalf("多人应选最近完工者甲前端（got %q hr=%v）", mentor, hr)
	}
	// 无人 done 的场景：多人全无 done——名字码位序稳定（乙 U+4E59 <
	// 甲 U+753B，「乙前端」码位靠前排前——可复现的确定性，不是随机）
	d2, _ := mentorStage(t)
	if err := d2.attach("乙前端", "前端开发", "s-m4"); err != nil {
		t.Fatal(err)
	}
	if err := d2.attach("甲前端", "前端开发", "s-m5"); err != nil {
		t.Fatal(err)
	}
	mentor2, hr2 := d2.pickMentor("新前端", "前端开发")
	if hr2 || mentor2 != "乙前端" {
		t.Fatalf("全无 done 应回落名字码位序（乙<甲，got %q）", mentor2)
	}
}

// ③ 无同岗：HR 兼任（在岗 HR 带名字与行动指引；无 HR 不空挂）。
func TestPickMentorHRFallback(t *testing.T) {
	d, _ := mentorStage(t)
	if err := d.attach("小马", "人事", "s-hr"); err != nil {
		t.Fatal(err)
	}
	mentor, hr := d.pickMentor("新设计师", "美术设计师")
	if !hr || mentor != "" {
		t.Fatalf("无同岗应 HR 兼任（got %q hr=%v）", mentor, hr)
	}
	line := d.mentorLine("新设计师", "", "美术设计师")
	if !strings.Contains(line, "@小马") || !strings.Contains(line, "兼任") || !strings.Contains(line, "主动") {
		t.Fatalf("HR 兼任文案应含 @HR、兼任、主动来问：%s", line)
	}
	// HR 也不在岗：不空挂——指路组呼与验收纪律库
	d2, _ := mentorStage(t)
	line2 := d2.mentorLine("新设计师", "", "美术设计师")
	if !strings.Contains(line2, "导师暂缺") || !strings.Contains(line2, "@排期编排组") {
		t.Fatalf("无 HR 应回落指路（got %s）", line2)
	}
}

// ④ 新人自己在册不误指给自己；空岗位走 HR 兜底。
func TestPickMentorEdge(t *testing.T) {
	d, _ := mentorStage(t)
	if err := d.attach("新前端", "前端开发", "s-m6"); err != nil {
		t.Fatal(err)
	}
	mentor, hr := d.pickMentor("新前端", "前端开发")
	if !hr {
		t.Fatalf("唯一同岗是自己→不该指自己（got %q）", mentor)
	}
	_, hr2 := d.pickMentor("任何人", "")
	if !hr2 {
		t.Fatal("空岗位应 HR 兜底")
	}
}

// ⑤ 模板断言（验收②）：HandoffPrompt 含四步引导行（指路不搬运）；
// WelcomeBackPrompt 是复职短模板（跳过四步）。
func TestOnboardingPrompts(t *testing.T) {
	cfg := agents.Config{Name: "新人", Role: "前端开发", Manual: "roles/developer"}
	join := agents.HandoffPrompt(cfg)
	for _, want := range []string{
		"四步引导",
		"新成员到岗引导",
		"读手册→读台账→读知识库→报到播报",
		"文件族",
		"完成标志",
	} {
		if !strings.Contains(join, want) {
			t.Fatalf("HandoffPrompt 应含 %q（四步指引行）", want)
		}
	}
	if !strings.Contains(join, "第四步：你的任务") {
		t.Fatal("原第三步应顺位为第四步")
	}
	back := agents.WelcomeBackPrompt(cfg)
	if !strings.Contains(back, "欢迎回来") || !strings.Contains(back, "跳过") {
		t.Fatalf("复职模板应是欢迎回来短版：%s", back[:60])
	}
	if strings.Contains(back, "四步引导") {
		t.Fatal("复职模板不该再走四步")
	}
}
