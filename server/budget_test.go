package server

// budget_test.go — token 预算与熔断（r_26）的行为面：
//   - 窗口锚点：日＝本地今日零点、周＝本周一零点（周日属上一周一）；
//   - 熔断语义：到额只关推进（AutoAdvance 自关＋播报），补货存活、
//     到龄提案回到房主关口——与 max_plans 熔断（r_19）完全同构；
//   - 周窗独立触发（日未过、周已过 → 本周口径播报）；
//   - 旋钮全零＝零开销：不查台账、不熔断（缺省工作室无感）；
//   - 额内照常推进（到龄提案正常代收）；
//   - HTTP 面：GET 反射旋钮＋窗口锚点，POST 成对覆写（缺一 400、
//     负值 400、合法对落盘 budget.json）。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

// apSpend is the budget-spend stub: fixed window numbers plus a call
// count (the 零开销 test asserts the engine never asked).
type apSpend struct {
	mu    sync.Mutex
	day   int64
	week  int64
	calls int
	fail  bool
}

func (s *apSpend) Spend(_, _ int64) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail {
		return 0, 0, os.ErrDeadlineExceeded
	}
	return s.day, s.week, nil
}

func (s *apSpend) called() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestBudgetWindowsAnchors: 窗口口径的纯函数钉子（本地自然日/自然周，
// 周一起算——2026-10-02 是周五，周一为 09-28）。
func TestBudgetWindowsAnchors(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 30, 0, 0, time.Local)
	day, week := budgetWindows(now)
	if want := time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local); !day.Equal(want) {
		t.Fatalf("日窗锚点＝今日零点（got %v want %v）", day, want)
	}
	if want := time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local); !week.Equal(want) {
		t.Fatalf("周窗锚点＝本周一零点（got %v want %v）", week, want)
	}
	// 周一当天：两窗同锚
	mon := time.Date(2026, 9, 28, 8, 0, 0, 0, time.Local)
	d2, w2 := budgetWindows(mon)
	if !d2.Equal(w2) {
		t.Fatalf("周一零点后两窗应同锚（day %v week %v）", d2, w2)
	}
	// 周日深夜仍属上一个周一
	sun := time.Date(2026, 10, 4, 23, 0, 0, 0, time.Local)
	_, w3 := budgetWindows(sun)
	if want := time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local); !w3.Equal(want) {
		t.Fatalf("周日属上一周一（got %v want %v）", w3, want)
	}
	// 人话格式：万一位小数、亿两位小数、万以下原样
	for n, want := range map[int64]string{
		999: "999", 10_000: "1.0 万", 12_345_678: "1234.6 万",
		123_456_789: "1.23 亿",
	} {
		if got := fmtBudgetTokens(n); got != want {
			t.Fatalf("fmtBudgetTokens(%d) = %q want %q", n, got, want)
		}
	}
}

// TestBudgetBreakerTripsAdvanceOnly: 日窗到额——推进自关＋播报、补货
// 存活、到龄提案不再代收（r_19 熔断语义的同构复制，分子换成 token）。
func TestBudgetBreakerTripsAdvanceOnly(t *testing.T) {
	spend := &apSpend{day: 150, week: 400}
	st := startAutoPilotStage(t, AutoPilotConfig{
		AcceptDelay: time.Minute,
		Budget:      func() BudgetPolicy { return BudgetPolicy{DayTokens: 100, WeekTokens: 1000} },
		BudgetSpend: spend.Spend,
	})
	enableBoth(st.staff, "proj-ap")
	st.submitAged(t, "到龄等审", time.Hour)

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("预算到额应熔断自动推进")
	}
	if !st.staff.SettingsOf("proj-ap").AutoStock {
		t.Fatal("熔断只关推进——自动补货必须存活（r_19 同构语义）")
	}
	if st.plans.Pending("", "proj-ap") == nil {
		t.Fatal("熔断后到龄提案不该被代收——回到房主关口")
	}
	if spend.called() == 0 {
		t.Fatal("旋钮在额上应查过台账")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "预算上限") >= 1 },
		"房间未见预算熔断播报")
	apWaitFor(t, func() bool {
		m, ok := st.tap.find(chat.MsgSystem, "预算上限")
		return ok && strings.Contains(m.Text, "今日") && strings.Contains(m.Text, "只关推进")
	}, "播报应言明今日口径与只关推进")
}

// TestBudgetWeekWindowTrips: 日未过、周已过——按本周口径熔断，播报带
// 下周一复位说明。
func TestBudgetWeekWindowTrips(t *testing.T) {
	spend := &apSpend{day: 50, week: 900}
	st := startAutoPilotStage(t, AutoPilotConfig{
		Budget:      func() BudgetPolicy { return BudgetPolicy{DayTokens: 100, WeekTokens: 800} },
		BudgetSpend: spend.Spend,
	})
	enableBoth(st.staff, "proj-ap")

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if st.staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("周窗到额应熔断自动推进")
	}
	apWaitFor(t, func() bool {
		m, ok := st.tap.find(chat.MsgSystem, "预算上限")
		return ok && strings.Contains(m.Text, "本周") && strings.Contains(m.Text, "下周一零点")
	}, "周窗播报应带本周口径与下周一复位")
}

// TestBudgetKnobsOffZeroOverhead: 旋钮全零（缺省）——不查台账（零开销）、
// 不熔断；推进照跑（到龄提案正常代收）。
func TestBudgetKnobsOffZeroOverhead(t *testing.T) {
	spend := &apSpend{day: 1 << 40, week: 1 << 40}
	st := startAutoPilotStage(t, AutoPilotConfig{
		AcceptDelay: time.Minute,
		Budget:      func() BudgetPolicy { return BudgetPolicy{} },
		BudgetSpend: spend.Spend,
	})
	enableBoth(st.staff, "proj-ap")
	st.submitAged(t, "无预算照跑", time.Hour)

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if spend.called() != 0 {
		t.Fatalf("旋钮全零不该查台账（got %d 次）", spend.called())
	}
	if !st.staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("无预算不该熔断")
	}
	if st.plans.Pending("", "proj-ap") != nil {
		t.Fatal("无预算到龄提案应照常代收")
	}
}

// TestBudgetUnderLimitKeepsAdvance: 额内——推进存活、无熔断播报。
func TestBudgetUnderLimitKeepsAdvance(t *testing.T) {
	spend := &apSpend{day: 10, week: 20}
	st := startAutoPilotStage(t, AutoPilotConfig{
		Budget:      func() BudgetPolicy { return BudgetPolicy{DayTokens: 100, WeekTokens: 1000} },
		BudgetSpend: spend.Spend,
	})
	enableBoth(st.staff, "proj-ap")

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if !st.staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("额内不该熔断")
	}
	if n := st.tap.count(chat.MsgSystem, "预算上限"); n != 0 {
		t.Fatalf("额内不该有熔断播报（got %d）", n)
	}
}

// TestBudgetSpendErrorSkipsBeat: 台账读挂——本拍跳过熔断检查（读面失败
// 不是超支），开关不动；但不再静默：失败链首拍落一条系统行、30 分钟
// 节流、读面恢复后计数归零（新失败链立刻再报）。
func TestBudgetSpendErrorSkipsBeat(t *testing.T) {
	spend := &apSpend{fail: true}
	st := startAutoPilotStage(t, AutoPilotConfig{
		Budget:      func() BudgetPolicy { return BudgetPolicy{DayTokens: 100} },
		BudgetSpend: spend.Spend,
	})
	enableBoth(st.staff, "proj-ap")

	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if !st.staff.SettingsOf("proj-ap").AutoAdvance {
		t.Fatal("台账读挂不该熔断")
	}
	if n := st.tap.count(chat.MsgSystem, "已达预算上限"); n != 0 {
		t.Fatalf("读挂不该播报熔断（got %d）", n)
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "台账读取失败") == 1 },
		"失败链首拍应恰一条降级提醒")
	// 同一失败链第二拍：节流，不重复
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	if n := st.tap.count(chat.MsgSystem, "台账读取失败"); n != 1 {
		t.Fatalf("30 分钟内同链应节流（got %d）", n)
	}
	// 读面恢复：计数归零
	spend.mu.Lock()
	spend.fail = false
	spend.day = 10
	spend.mu.Unlock()
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	// 新失败链：立刻再报
	spend.mu.Lock()
	spend.fail = true
	spend.mu.Unlock()
	if !st.fire() {
		t.Fatal("round 未完成")
	}
	apWaitFor(t, func() bool { return st.tap.count(chat.MsgSystem, "台账读取失败") == 2 },
		"恢复后的新失败链应立刻再报")
}

// TestBudgetEndpointFace: GET/POST /budget——GET 反射旋钮＋窗口锚点；
// POST 成对覆写（缺一 400、负值 400、合法对落盘并回读得到）。
func TestBudgetEndpointFace(t *testing.T) {
	lobby := chat.NewHub()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	root := t.TempDir()
	registry := chat.NewRegistry(projStore, root, "", "")
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
		},
		Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	base := addrHTTP(s)

	// 空库缺省：双零（不限）、锚点在今日/本周一零点
	resp, err := http.Get(base + "/budget")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		DayTokens   int64 `json:"day_tokens"`
		WeekTokens  int64 `json:"week_tokens"`
		DayStart    int64 `json:"day_start"`
		WeekStart   int64 `json:"week_start"`
		BreakerLive bool  `json:"breaker_live"`
		UsageError  bool  `json:"usage_error"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.DayTokens != 0 || got.WeekTokens != 0 {
		t.Fatalf("缺省双零（got %+v）", got)
	}
	if got.BreakerLive {
		t.Fatal("无引擎 breaker_live 应 false")
	}
	if got.DayStart <= 0 || got.WeekStart <= 0 || got.DayStart < got.WeekStart {
		t.Fatalf("窗口锚点不对（day %d week %d）", got.DayStart, got.WeekStart)
	}

	// 缺一个字段 → 400（预算是成对策略）
	for _, body := range []string{
		`{"day_tokens": 1000}`,
		`{"week_tokens": 1000}`,
		`{}`,
	} {
		r, _ := http.Post(base+"/budget", "application/json", strings.NewReader(body))
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("缺字段应 400（%s → got %d）", body, r.StatusCode)
		}
		r.Body.Close()
	}
	// 负值 → 400
	r, _ := http.Post(base+"/budget", "application/json",
		strings.NewReader(`{"day_tokens": -5, "week_tokens": 0}`))
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("负值应 400（got %d）", r.StatusCode)
	}
	r.Body.Close()

	// 合法对 → 落盘 + 回读得到（week=0 是明示的不限，不是没写）
	r, _ = http.Post(base+"/budget", "application/json",
		strings.NewReader(`{"day_tokens": 5000000, "week_tokens": 0}`))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("合法对应 200（got %d）", r.StatusCode)
	}
	r.Body.Close()
	if pol, ok := LoadBudgetPolicy(BudgetPath(root)); !ok ||
		pol.DayTokens != 5_000_000 || pol.WeekTokens != 0 {
		t.Fatalf("budget.json 落盘不对（%+v ok=%v）", pol, ok)
	}
	resp, err = http.Get(base + "/budget")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.DayTokens != 5_000_000 || got.WeekTokens != 0 {
		t.Fatalf("回读不一致（day %d week %d）", got.DayTokens, got.WeekTokens)
	}
	// 台账读面挂了也只降级不 5xx（usage_error 位）——由 GET 的 200 兜底，
	// 这里至少断言结构不缺锚点字段。
	if got.DayStart <= 0 {
		t.Fatal("回读缺窗口锚点")
	}
}

// TestSpendCacheTTL: 读数缓存——TTL 内同窗只打一次台账；过期重读；
// 换窗锚点（跨零点/周一）立即重读；error 一并缓存（台账挂时不每拍
// 轰炸子进程）。
func TestSpendCacheTTL(t *testing.T) {
	calls := 0
	read := func(_, _ int64) (int64, int64, error) {
		calls++
		return int64(calls), int64(calls * 100), nil
	}
	tick := time.Unix(0, 0)
	c := &spendCache{ttl: 30 * time.Second,
		now:  func() time.Time { return tick },
		read: read}

	day, week, err := c.get(1000, 500)
	if err != nil || day != 1 || week != 100 {
		t.Fatalf("首读应过桩（got %d/%d err %v）", day, week, err)
	}
	// TTL 内同窗：回缓存值，不碰台账
	tick = tick.Add(20 * time.Second)
	if day, week, _ = c.get(1000, 500); day != 1 || week != 100 || calls != 1 {
		t.Fatalf("TTL 内应回缓存（got %d/%d calls %d）", day, week, calls)
	}
	// 过期（35s > 30s）：重读
	tick = tick.Add(15 * time.Second)
	if day, _, _ = c.get(1000, 500); day != 2 || calls != 2 {
		t.Fatalf("过期应重读（got %d calls %d）", day, calls)
	}
	// 换窗锚点：立即重读（跨零点/周一的口径切换）
	if day, week, _ = c.get(2000, 500); day != 3 || week != 300 || calls != 3 {
		t.Fatalf("换窗应重读（got %d/%d calls %d）", day, week, calls)
	}

	// error 一并缓存：第二拍不再打台账
	fails := 0
	ce := &spendCache{ttl: 30 * time.Second,
		now: func() time.Time { return tick },
		read: func(_, _ int64) (int64, int64, error) {
			fails++
			return 0, 0, os.ErrDeadlineExceeded
		}}
	if _, _, err := ce.get(1, 1); err == nil {
		t.Fatal("应透传台账错误")
	}
	if _, _, err := ce.get(1, 1); err == nil || fails != 1 {
		t.Fatalf("TTL 内错误应缓存（fails %d err %v）", fails, err)
	}
}

// TestBudgetPolicyChain: 真源链——文件在则整份做主（值 0 是明示的不限，
// 不被 env 回填）；无文件走 env；都没有＝双零。
func TestBudgetPolicyChain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NIUMA_BUDGET_DAY_TOKENS", "7")
	t.Setenv("NIUMA_BUDGET_WEEK_TOKENS", "9")
	if pol := ResolveBudgetPolicy(dir); pol.DayTokens != 7 || pol.WeekTokens != 9 {
		t.Fatalf("无文件应走 env（got %+v）", pol)
	}
	if err := SaveBudgetPolicy(BudgetPath(dir), BudgetPolicy{DayTokens: 5, WeekTokens: 0}); err != nil {
		t.Fatalf("save: %v", err)
	}
	pol := ResolveBudgetPolicy(dir)
	if pol.DayTokens != 5 || pol.WeekTokens != 0 {
		t.Fatalf("文件在则整份做主（got %+v）", pol)
	}
	// 坏文件（负值）＝不存在，回 env
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(`{"day_tokens":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if pol := ResolveBudgetPolicy(dir); pol.DayTokens != 7 {
		t.Fatalf("坏文件应回 env（got %+v）", pol)
	}
}
