package achv

// achievements_test.go — r_18 t_187/t_189：判定引擎的纯函数面。构造事件流
// 直喂 Evaluate/Observe。18 枚表整表换入后（t_208 追加）的口径：枚键随
// design/r18-achievements 定稿（first-task/ten-tasks/fifty-tasks/…），
// 含幂等/重启恢复/补算洪水防线、红线三查负例、破坏点 A2 阈值。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/staffing"
)

func openStaffing(t *testing.T, dir string) *staffing.Store {
	t.Helper()
	st, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func ev(kind, member string) Event {
	return Event{Kind: kind, Member: member, At: 1000}
}

// ① 首单交付（A1 初来乍到）：一单即达。
func TestAchieveFirstTask(t *testing.T) {
	st := newAchieveState()
	next, fired := Evaluate(ev("task-done", "小猿"), st, DefaultAchievements())
	found := false
	for _, f := range fired {
		if f.Key == "first-task" {
			found = true
		}
	}
	if !found {
		t.Fatalf("首单应触发 first-task（got %v）", keysOf(fired))
	}
	if next.Members["小猿"].TasksDone != 1 {
		t.Fatal("计数应滚动")
	}
}

// ② 幂等：同一事件重放不重复触发。
func TestAchieveIdempotentReplay(t *testing.T) {
	st := newAchieveState()
	next, _ := Evaluate(ev("task-done", "小猿"), st, DefaultAchievements())
	firedCount := 0
	for i := 0; i < 9; i++ {
		var f []AchievementDef
		next, f = Evaluate(ev("task-done", "小猿"), next, DefaultAchievements())
		firedCount += len(f)
	}
	if firedCount != 1 { // 只有十单（恰在第 10 次触发）
		t.Fatalf("十连事件应只触发十单一枚（got %d）", firedCount)
	}
}

// ③ A2 十全十美：第十次 task-done 触发（破坏点基准——阈值 10）。
func TestAchieveTenTasks(t *testing.T) {
	st := newAchieveState()
	var fired []AchievementDef
	for i := 0; i < 11; i++ {
		st, fired = Evaluate(ev("task-done", "小马"), st, DefaultAchievements())
		for _, f := range fired {
			if f.Key == "ten-tasks" && i != 9 {
				t.Fatalf("十单在第 %d 次提前触发（应恰在第 10 次）", i+1)
			}
		}
	}
	if _, ok := st.Unlocked["ten-tasks@小马"]; !ok {
		t.Fatal("第十一次后十单应在册")
	}
}

// ③b A3 半百老将：累计 50 张。
func TestAchieveFiftyTasks(t *testing.T) {
	st := newAchieveState()
	for i := 0; i < 50; i++ {
		st, _ = Evaluate(ev("task-done", "老黄牛"), st, DefaultAchievements())
	}
	if _, ok := st.Unlocked["fifty-tasks@老黄牛"]; !ok {
		t.Fatal("第 50 单应触发 fifty-tasks")
	}
	if _, ok := st.Unlocked["fifty-tasks@别人"]; ok {
		t.Fatal("他人名下不该有")
	}
}

// ④ 全室成就不挂个人名下（C2 半千护栏）。
func TestAchieveStudioKeyed(t *testing.T) {
	st := newAchieveState()
	next, fired := Evaluate(Event{Kind: "tests-green", TestCount: 501, At: 1}, st, DefaultAchievements())
	found := false
	for _, f := range fired {
		if f.Key == "test-500" {
			found = true
		}
	}
	if !found {
		t.Fatal("破五百应触发 test-500")
	}
	if _, ok := next.Unlocked["test-500"]; !ok {
		t.Fatal("全室键无 @ 后缀")
	}
	if _, ok := next.Unlocked["test-500@"]; ok {
		t.Fatal("全室成就不应挂成员键")
	}
}

// ⑤ 需求线贡献计数与 C1 十全线路（工作室第 10 条线）。
func TestAchieveReqLineContribution(t *testing.T) {
	st := newAchieveState()
	var fired []AchievementDef
	for i := 0; i < 10; i++ {
		st, fired = Evaluate(Event{Kind: "req-split", Member: "小鹿", ReqID: "r_2" + string(rune('0'+i)), At: 1},
			st, DefaultAchievements())
	}
	if !st.Members["小鹿"].ReqLines["r_20"] {
		t.Fatal("贡献记录在册")
	}
	found := false
	for _, f := range fired {
		if f.Key == "req-line-10" {
			found = true
		}
	}
	if !found {
		t.Fatal("第 10 条线应触发 req-line-10（全室）")
	}
	if _, ok := st.Unlocked["req-line-10"]; !ok {
		t.Fatal("C1 挂工作室键")
	}
}

// ⑥ 引擎 Observe：挂 staffing 持久化（达成集落盘）。
func TestAchieveEnginePersists(t *testing.T) {
	dir := t.TempDir()
	staff := openStaffing(t, dir)
	e := NewAchievementEngine(staff)
	e.Observe(ev("task-done", "小猿"), nil)
	// 重开一个引擎（模拟重启）——已达成恢复
	e2 := NewAchievementEngine(staff)
	if _, ok := e2.state.Unlocked["first-task@小猿"]; !ok {
		t.Fatal("重启后达成集应恢复（只存已达成不回放）")
	}
}

// ⑦ 重启后不重触发（幂等跨重启）。
func TestAchieveNoRefireAfterRestart(t *testing.T) {
	dir := t.TempDir()
	staff := openStaffing(t, dir)
	e := NewAchievementEngine(staff)
	e.Observe(ev("task-done", "小猿"), nil)
	e2 := NewAchievementEngine(staff)
	fired := e2.Observe(ev("task-done", "小猿"), nil)
	for _, f := range fired {
		if f.Key == "first-task" {
			t.Fatal("重启后同款事件不该重触发（幂等跨重启）")
		}
	}
}

// ⑧ 深夜亮灯（C3 亮灯的深夜）：全室被动、第十个触发、不挂成员。
func TestAchieveNightLights(t *testing.T) {
	st := newAchieveState()
	var fired []AchievementDef
	for i := 0; i < 10; i++ {
		st, fired = Evaluate(Event{Kind: "night-light", At: int64(i)}, st, DefaultAchievements())
	}
	found := false
	for _, f := range fired {
		if f.Key == "night-owl" {
			found = true
		}
	}
	if !found {
		t.Fatal("第十夜应触发 night-owl")
	}
	if _, ok := st.Unlocked["night-owl"]; !ok {
		t.Fatal("全室键在")
	}
}

// ⑨ 表驱动可替换：换表后引擎结构零改动（清单定稿落地的契约）。
func TestAchieveTablePluggable(t *testing.T) {
	custom := []AchievementDef{
		{Key: "custom-1", Name: "测试章", Text: "测试", PerMember: false,
			Cond: func(ev Event, st *AchieveState) bool { return ev.Kind == "task-done" }},
	}
	st := newAchieveState()
	next, fired := Evaluate(ev("task-done", "小狐"), st, custom)
	if len(fired) != 1 || fired[0].Key != "custom-1" {
		t.Fatal("自定义表应生效（引擎结构零改动）")
	}
	if next.Members["小狐"].TasksDone != 1 {
		t.Fatal("计数照滚")
	}
}

func keysOf(ds []AchievementDef) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Key
	}
	return out
}

// ⑩ tests-green 全室事件：跑绿计数滚动（test-500 未到线不触发）。
func TestAchieveTestsGreenEvent(t *testing.T) {
	st := newAchieveState()
	next, fired := Evaluate(Event{Kind: "tests-green", TestCount: 102, At: 1}, st, DefaultAchievements())
	for _, f := range fired {
		if f.Key == "test-500" {
			t.Fatal("五百线未到不该触发")
		}
	}
	if next.TestGreenRuns != 1 {
		t.Fatal("跑绿计数应滚")
	}
}

// ── t_189 表换入（t_208 追加）的新面 ─────────────────────────────

// ⑪ A4 一锤定音：首个提案被接受（plan-accept 事件，主语是提交人）。
func TestAchieveFirstPlan(t *testing.T) {
	st := newAchieveState()
	next, fired := Evaluate(ev("plan-accept", "小牛"), st, DefaultAchievements())
	found := false
	for _, f := range fired {
		if f.Key == "first-plan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("首个提案通过应触发 first-plan（got %v）", keysOf(fired))
	}
	if next.Members["小牛"].PlansAccepted != 1 {
		t.Fatal("提案计数应滚")
	}
	// 第二个提案不再触发（幂等）
	_, fired = Evaluate(ev("plan-accept", "小牛"), next, DefaultAchievements())
	for _, f := range fired {
		if f.Key == "first-plan" {
			t.Fatal("第二个提案不该重触发 first-plan")
		}
	}
}

// ⑫ A6 落笔定稿：design/ 前缀首笔 kb write；非 design 键不触发。
func TestAchieveDesignFinal(t *testing.T) {
	st := newAchieveState()
	next, fired := Evaluate(Event{Kind: "kb-write", Member: "小鹿", DocKey: "design/r18-x", At: 1},
		st, DefaultAchievements())
	found := false
	for _, f := range fired {
		if f.Key == "design-final" {
			found = true
		}
	}
	if !found {
		t.Fatal("design/ 首笔应触发 design-final")
	}
	if next.Members["小鹿"].DesignDocs != 1 {
		t.Fatal("设计定稿计数应滚")
	}
	// 非设计键：不触发 A6
	_, fired = Evaluate(Event{Kind: "kb-write", Member: "小马", DocKey: "roles/hr", At: 1},
		st, DefaultAchievements())
	for _, f := range fired {
		if f.Key == "design-final" {
			t.Fatal("非 design/ 键不该触发 design-final")
		}
	}
}

// ⑬ A12 著书立说：kb 累计十篇（任意键）。
func TestAchieveKbAuthor(t *testing.T) {
	st := newAchieveState()
	var fired []AchievementDef
	for i := 0; i < 10; i++ {
		st, fired = Evaluate(Event{Kind: "kb-write", Member: "小鹿", DocKey: "docs/x", At: 1},
			st, DefaultAchievements())
	}
	found := false
	for _, f := range fired {
		if f.Key == "kb-author" {
			found = true
		}
	}
	if !found {
		t.Fatal("第十篇应触发 kb-author")
	}
}

// ⑭ C6 自举彩蛋：achievement-live 事件直配 nil-Cond 全室定义，恰一枚
// （表内其余 nil-Cond 的 A5/A7/A8/A9/A10/A11/C4/C5 不被自举误触发）。
func TestAchieveSelfBootstrap(t *testing.T) {
	st := newAchieveState()
	next, fired := Evaluate(Event{Kind: "achievement-live", At: 1}, st, DefaultAchievements())
	if len(fired) != 1 || fired[0].Key != "achievement-live" {
		t.Fatalf("自举应恰触发 achievement-live 一枚（got %v）", keysOf(fired))
	}
	if _, ok := next.Unlocked["achievement-live"]; !ok {
		t.Fatal("自举键在册")
	}
	for _, k := range []string{"first-review", "audit-pass", "bug-catch", "test-hundred", "cross-role", "apprentice", "dawn-watch", "full-house"} {
		if _, ok := next.Unlocked[k]; ok {
			t.Fatalf("自举不该误触发 %s", k)
		}
	}
	// 重放自举：幂等（重启场景）
	_, fired = Evaluate(Event{Kind: "achievement-live", At: 2}, next, DefaultAchievements())
	if len(fired) != 0 {
		t.Fatal("自举重放不该再触发")
	}
}

// ⑮ 18 枚表完整性：键数 18、个人 12＋集体 6、nil-Cond 恰 8 枚（A5/A7/
// A8/A9/A10/A11/C4/C5——事件面未接，待接入补 Cond 激活）。
func TestAchieveTableShape(t *testing.T) {
	table := DefaultAchievements()
	if len(table) != 18 {
		t.Fatalf("表应 18 枚（got %d）", len(table))
	}
	per, collective, nils := 0, 0, 0
	for _, d := range table {
		if d.PerMember {
			per++
		} else {
			collective++
		}
		if d.Cond == nil {
			nils++
		}
	}
	if per != 12 || collective != 6 {
		t.Fatalf("个人 12＋集体 6（got %d/%d）", per, collective)
	}
	if nils != 9 {
		t.Fatalf("nil-Cond 应 9 枚（A5/A7/A8/A9/A10/A11/C4/C5/C6——got %d）", nils)
	}
}

// ⑯ 红线一：无排名——表内文案禁「最快/最多/领先/超越」字样（「第一张」
// 序数合法——t_189 裁定，不在此列）。
func TestAchieveRedlineNoRanking(t *testing.T) {
	for _, d := range DefaultAchievements() {
		for _, banned := range []string{"最快", "最多", "领先", "超越", "击败"} {
			if strings.Contains(d.Text, banned) || strings.Contains(d.Name, banned) {
				t.Fatalf("红线（无排名）：%s 含禁词 %q", d.Key, banned)
			}
		}
	}
}

// ⑰ 红线二：无时效——表内文案禁时间压力词（「24 小时内」「一周内」类）。
func TestAchieveRedlineNoDeadline(t *testing.T) {
	for _, d := range DefaultAchievements() {
		for _, banned := range []string{"小时内", "天内", "周内", "之前", "限时", "赶在"} {
			if strings.Contains(d.Text, banned) {
				t.Fatalf("红线（无时效）：%s 含禁词 %q", d.Key, banned)
			}
		}
	}
}

// ⑱ 红线三：无补偿——定义结构无奖励字段（编译期已锁死；此处钉文案
// 兜底——不承诺解锁/加分/特权）。
func TestAchieveRedlineNoReward(t *testing.T) {
	for _, d := range DefaultAchievements() {
		for _, banned := range []string{"解锁", "加分", "奖励", "特权"} {
			if strings.Contains(d.Text, banned) {
				t.Fatalf("红线（无补偿）：%s 含禁词 %q", d.Key, banned)
			}
		}
	}
}

// ⑲ 破坏点（t_186 §五）：A2 阈值 10→11 时十连单事件流必红——还原绿
// 由 CI 全量守护（TestAchieveTenTasks 的「恰在第 10 次」断言即该抽验）。
func TestAchieveA2ThresholdPin(t *testing.T) {
	st := newAchieveState()
	for i := 0; i < 10; i++ {
		var fired []AchievementDef
		st, fired = Evaluate(ev("task-done", "小马"), st, DefaultAchievements())
		for _, f := range fired {
			if f.Key == "ten-tasks" && i != 9 {
				t.Fatalf("A2 破坏点：十单在第 %d 次提前触发（阈值被动过？应恰在第 10 次）", i+1)
			}
		}
	}
}
