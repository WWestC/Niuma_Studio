package dispatch

// daily_test.go — r_30（t_133）：日报注入尾部统计行的钉子。统计与台账
// 同源现算（done/在途/open 对照断言）＋自由叙事不被统计模板化（注入仍
// 含原指令文本）＋无 store 时的降级（零值行照发）。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

func dailyStage(t *testing.T) (*Dispatcher, *tasks.Engine, requirements.Store, *recBridge) {
	t.Helper()
	dir := t.TempDir()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{"demo", chat.LobbyKey} })
	reqs := requirements.OpenMemory()
	rb := &recBridge{got: map[string][]string{}}
	d := Start(chat.NewHub(), store, rb, Config{
		Workspace:   dir,
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
		Reqs:        reqs,
		Tasks:       eng,
		ProjectKey:  "demo",
	})
	t.Cleanup(d.Stop)
	return d, eng, reqs, rb
}

// ① 统计尾行与台账一致（当日 done/在途/open 对照）。
func TestDailyStatsTailMatchesLedger(t *testing.T) {
	d, eng, reqs, rb := dailyStage(t)
	// 两张 done（今日）＋一张 doing＋一张 todo；两条 open 需求
	for _, mk := range []struct {
		title, assignee, status string
	}{{"完甲", "小牛", tasks.StatusDone}, {"完乙", "小牛", tasks.StatusDone},
		{"在办", "小牛", tasks.StatusDoing}, {"待办", "小牛", tasks.StatusTodo}} {
		out := eng.CreatePlanned("房主", tasks.Task{Title: mk.title, Assignee: mk.assignee, ProjectKey: "demo"})
		if out.Denied {
			t.Fatal(out.Reason)
		}
		if mk.status != tasks.StatusTodo {
			if up := eng.Update("demo", "房主", out.Task.ID, tasks.Patch{Status: mk.status}); up.Denied {
				t.Fatal(up.Reason)
			}
		}
	}
	for _, title := range []string{"需求甲", "需求乙"} {
		if _, err := reqs.Create("", "demo", title, "", "房主"); err != nil {
			t.Fatal(err)
		}
	}
	// 编排者在座（日报钟的收件人）
	if err := d.attach("小牛", "排期编排", "s-daily"); err != nil {
		t.Fatal(err)
	}
	d.dailyFire(0)
	got := rb.all("s-daily")
	for _, want := range []string{
		"统计尾行", "当日 done 2", "在途 2", "open 2",
		"汇总今日任务动静", // 自由叙事指令原样在（统计只附尾行不改写正文）
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("日报注入应含 %q:\n%s", want, got)
		}
	}
}

// ② 跨日 done 不计入当日（自然日口径）——旧 done 的 UpdatedTS 在零点前。
func TestDailyStatsDayBoundary(t *testing.T) {
	d, eng, _, rb := dailyStage(t)
	out := eng.CreatePlanned("房主", tasks.Task{Title: "昨天的活", Assignee: "小牛", ProjectKey: "demo"})
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if up := eng.Update("demo", "房主", out.Task.ID, tasks.Patch{Status: tasks.StatusDone}); up.Denied {
		t.Fatal(up.Reason)
	}
	// 跨日口径：注入时刻取「后天」（今天+48h）——today 的 done 落在
	// 后天的零点前，不计入「当日」（自然日口径与驾驶舱 dayStart 同钟）
	if err := d.attach("小牛", "排期编排", "s-daily2"); err != nil {
		t.Fatal(err)
	}
	d.dailyFire(time.Now().Add(48 * time.Hour).Unix())
	got := rb.all("s-daily2")
	if !strings.Contains(got, "当日 done 0") {
		t.Fatalf("后天时钟下的注入不该计入今日 done:\n%s", got)
	}
}

// ③ 无编排者：不注入（既有语义零回退）。
func TestDailyNoOrchestratorNoop(t *testing.T) {
	d, _, _, rb := dailyStage(t)
	d.dailyFire(0)
	if got := rb.all("s-daily"); got != "" {
		t.Fatalf("无编排者不该注入：%s", got)
	}
}
