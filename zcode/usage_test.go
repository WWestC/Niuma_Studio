package zcode

// usage_test.go — the ledger read exercised against temp sqlite files
// through the same python3 seam production rides (skip when python3 is
// absent). The property under test is the JOIN: studio-born sessions
// (the three arms) aggregate under their title, the operator's own
// work never shows up, and the turn page is newest-first with its
// per-model split folded in.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeUsageFixture builds both dbs: the task index (tasks with the
// columns the scope arms read) and the CLI store (session/turn_usage/
// model_usage with the columns the aggregation reads). Returns
// (usageDB, indexDB).
func makeUsageFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	indexDB := filepath.Join(dir, "tasks-index.sqlite")
	usageDB := filepath.Join(dir, "db.sqlite")
	execSQL(t, indexDB, `create table tasks (
  workspace_key text not null, task_id text not null,
  title text, meta_json text not null default '{}',
  deleted integer not null default 0,
  primary key (workspace_key, task_id))`)
	execSQL(t, usageDB,
		"create table session (id text primary key)",
		`create table turn_usage (
  session_id text not null, turn_id text not null,
  status text not null, started_at integer not null,
  duration_ms integer, time_to_first_token_ms integer,
  model_request_count integer not null default 0,
  tool_call_count integer not null default 0,
  input_tokens integer not null default 0,
  output_tokens integer not null default 0,
  reasoning_tokens integer not null default 0,
  cache_read_input_tokens integer not null default 0,
  computed_total_tokens integer not null default 0,
  primary key(session_id, turn_id))`,
		`create table model_usage (
  id text primary key, session_id text not null,
  turn_id text not null, model_id text,
  started_at integer not null default 0,
  computed_total_tokens integer not null default 0)`)
	return usageDB, indexDB
}

// seedUsageTask drops one index row (meta is a bare JSON fragment; "{}"
// is the operator's own default — no arms match).
func seedUsageTask(t *testing.T, indexDB, sid, title, meta string) {
	t.Helper()
	execSQL(t, indexDB, "insert into tasks (workspace_key, task_id, title, meta_json)"+
		" values ('ws', '"+sid+"', '"+title+"', '"+meta+"')")
}

// seedUsageTurn drops one turn_usage row; dur/ttft are nullable.
func seedUsageTurn(t *testing.T, usageDB, sid, turnID string, started int64, in, out, cache, total int64, dur any) {
	t.Helper()
	seedUsageTurnRqs(t, usageDB, sid, turnID, started, in, out, cache, total, dur, 1)
}

// seedUsageTurnRqs is seedUsageTurn with the turn's model_request_count
// (the context gauge divides the summed input by it).
func seedUsageTurnRqs(t *testing.T, usageDB, sid, turnID string, started int64, in, out, cache, total int64, dur any, reqs int64) {
	t.Helper()
	durSQL := "NULL"
	switch d := dur.(type) {
	case int:
		durSQL = itoa(int64(d))
	case int64:
		durSQL = itoa(d)
	case float64:
		durSQL = itoa(int64(d))
	}
	execSQL(t, usageDB, "insert into turn_usage (session_id, turn_id, status,"+
		" started_at, duration_ms, model_request_count, input_tokens,"+
		" output_tokens, reasoning_tokens, cache_read_input_tokens,"+
		" computed_total_tokens)"+
		" values ('"+sid+"', '"+turnID+"', 'completed', "+itoa(started)+", "+durSQL+
		", "+itoa(reqs)+", "+itoa(in)+", "+itoa(out)+", 0, "+itoa(cache)+", "+itoa(total)+")")
}

// seedUsageModel drops one model_usage request row (started_at rides
// along — the series buckets models by it, no turn join).
func seedUsageModel(t *testing.T, usageDB, sid, turnID, model string, tokens int64, started int64) {
	t.Helper()
	execSQL(t, usageDB, "insert into model_usage (id, session_id, turn_id, model_id,"+
		" started_at, computed_total_tokens) values ('"+sid+"-"+turnID+"-"+model+
		"', '"+sid+"', '"+turnID+"', '"+model+"', "+itoa(started)+", "+itoa(tokens)+")")
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestUsageSummaryJoinsStudioBornUnderTitles: the three arms fold every
// studio session under its title (rebirth sessions merge, zero-turn
// births still count), the operator's own rows stay out, net_input is
// input minus cache-read, and the biggest spender heads the table.
func TestUsageSummaryJoinsStudioBornUnderTitles(t *testing.T) {
	usageDB, indexDB := makeUsageFixture(t)
	// 小牛：两条会话（一条带 dhEmployee 印，一条只有标题契约——时间线
	// 重写抹印后的形态）＋一条零轮新会话；小助手一条；操作者自己的
	// 「重新编译」和无 Lv 的【个人】会话必须缺席。
	seedUsageTask(t, indexDB, "sess_niu1", "【Niuma_Studio】小牛-排期编排-Lv.8", `{"dhEmployee":true}`)
	seedUsageTask(t, indexDB, "sess_niu2", "【Niuma_Studio】小牛-排期编排-Lv.8", `{}`)
	seedUsageTask(t, indexDB, "sess_niu3", "【Niuma_Studio】小牛-排期编排-Lv.8", `{}`)
	seedUsageTask(t, indexDB, "sess_zhu", "【Niuma_Studio】小助手-使用顾问", `{"dhEmployee":true}`)
	seedUsageTask(t, indexDB, "sess_own1", "重新编译", `{}`)
	seedUsageTask(t, indexDB, "sess_own2", "【个人】写作", `{"dhEmployee":false}`)
	seedUsageTurn(t, usageDB, "sess_niu1", "turn_a", 1000, 500, 50, 400, 550, 8000)
	seedUsageTurn(t, usageDB, "sess_niu2", "turn_b", 2000, 700, 30, 650, 730, nil)
	seedUsageTurn(t, usageDB, "sess_zhu", "turn_c", 3000, 100, 10, 80, 110, 500)
	seedUsageTurn(t, usageDB, "sess_own1", "turn_x", 4000, 999, 99, 900, 1098, 100)

	ov, err := usageOverviewAt(usageDB, indexDB)
	if err != nil {
		t.Fatalf("usageOverviewAt: %v", err)
	}
	rows := ov.Employees
	if len(rows) != 2 {
		t.Fatalf("want 2 studio employees (小牛/小助手), got %d: %+v", len(rows), rows)
	}
	niu, zhu := rows[0], rows[1]
	if niu.Title != "【Niuma_Studio】小牛-排期编排-Lv.8" || zhu.Title != "【Niuma_Studio】小助手-使用顾问" {
		t.Fatalf("biggest spender first + assistant second, got %q / %q", niu.Title, zhu.Title)
	}
	if niu.Sessions != 3 || niu.Turns != 2 {
		t.Fatalf("小牛 sessions=3 (zero-turn birth counts) turns=2, got %d/%d", niu.Sessions, niu.Turns)
	}
	if niu.Input != 1200 || niu.Output != 80 || niu.CacheRead != 1050 || niu.Total != 1280 {
		t.Fatalf("小牛 sums in/out/cache/total = %d/%d/%d/%d, want 1200/80/1050/1280",
			niu.Input, niu.Output, niu.CacheRead, niu.Total)
	}
	if niu.NetInput != 150 {
		t.Fatalf("net_input = input−cache_read = 150, got %d", niu.NetInput)
	}
	if niu.LastActive != 2000 {
		t.Fatalf("last_active = newest turn 2000, got %d", niu.LastActive)
	}
	if zhu.Turns != 1 || zhu.Total != 110 {
		t.Fatalf("小助手 turns/total = %d/%d, want 1/110", zhu.Turns, zhu.Total)
	}
}

// TestUsageSummaryAbsentStoresReadEmpty: no CLI store (or no index) is
// an empty studio, not an error.
func TestUsageSummaryAbsentStoresReadEmpty(t *testing.T) {
	dir := t.TempDir()
	usageDB, indexDB := filepath.Join(dir, "none.sqlite"), filepath.Join(dir, "none-index.sqlite")
	ov, err := usageOverviewAt(usageDB, indexDB)
	if err != nil || len(ov.Employees) != 0 {
		t.Fatalf("missing stores = empty nil-error, got %+v / %v", ov.Employees, err)
	}
	if ov.Series == nil || ov.Series.Hour == nil || len(ov.Series.Hour.Points) != 0 || len(ov.Series.Models) != 0 {
		t.Fatalf("missing stores series = empty grids, got %+v", ov.Series)
	}
	rep, err := usageTurnsAt(usageDB, indexDB, "【Niuma_Studio】小牛-排期编排-Lv.8", 100)
	if err != nil || rep == nil || len(rep.Turns) != 0 {
		t.Fatalf("missing stores turns = empty nil-error, got %+v / %v", rep, err)
	}
}

// TestUsageTurnsNewestFirstCappedWithModelSplits: the page is newest-
// first, capped at limit, nulls fold to zero, and each turn carries its
// per-model split sorted by tokens — an unknown title reads empty.
func TestUsageTurnsNewestFirstCappedWithModelSplits(t *testing.T) {
	usageDB, indexDB := makeUsageFixture(t)
	seedUsageTask(t, indexDB, "sess_niu1", "【Niuma_Studio】小牛-排期编排-Lv.8", `{"dhEmployee":true}`)
	seedUsageTask(t, indexDB, "sess_niu2", "【Niuma_Studio】小牛-排期编排-Lv.8", `{"dhEmployee":true}`)
	for i, at := range []int64{1000, 2000, 3000, 4000, 5000} {
		seedUsageTurn(t, usageDB, "sess_niu1", "turn_"+itoa(int64(i)), at,
			int64(100+i), 10, 90, int64(110+i), 7000+i)
	}
	// turn_4 的双模型分摊（大头的排后仍按 token 降序）＋ turn_2 单模型。
	seedUsageModel(t, usageDB, "sess_niu1", "turn_4", "GLM-5.3", 90, 5000)
	seedUsageModel(t, usageDB, "sess_niu1", "turn_4", "GLM-5.3-Flash", 20, 5000)
	seedUsageModel(t, usageDB, "sess_niu1", "turn_2", "GLM-5.3", 105, 3000)

	rep, err := usageTurnsAt(usageDB, indexDB, "【Niuma_Studio】小牛-排期编排-Lv.8", 3)
	if err != nil {
		t.Fatalf("usageTurnsAt: %v", err)
	}
	if rep.Sessions != 2 || len(rep.Turns) != 3 {
		t.Fatalf("sessions=2 turns capped at 3, got %d/%d", rep.Sessions, len(rep.Turns))
	}
	if rep.Turns[0].TurnID != "turn_4" || rep.Turns[2].TurnID != "turn_2" {
		t.Fatalf("newest first: want turn_4..turn_2, got %s..%s", rep.Turns[0].TurnID, rep.Turns[2].TurnID)
	}
	first := rep.Turns[0]
	if first.StartedAt != 5000 || first.DurationMs != 7004 || first.TTFTMs != 0 {
		t.Fatalf("turn_4 fields started/dur/ttft = %d/%d/%d (null ttft folds 0)",
			first.StartedAt, first.DurationMs, first.TTFTMs)
	}
	if first.Input != 104 || first.Total != 114 {
		t.Fatalf("turn_4 in/total = %d/%d, want 104/114", first.Input, first.Total)
	}
	if len(first.Models) != 2 || first.Models[0].Model != "GLM-5.3" || first.Models[0].Tokens != 90 {
		t.Fatalf("turn_4 model split sorted by tokens, got %+v", first.Models)
	}
	if third := rep.Turns[2]; len(third.Models) != 1 || third.Models[0].Model != "GLM-5.3" {
		t.Fatalf("turn_2 single model split, got %+v", third.Models)
	}

	unknown, err := usageTurnsAt(usageDB, indexDB, "【Niuma_Studio】查无此人-岗-Lv.1", 100)
	if err != nil || len(unknown.Turns) != 0 {
		t.Fatalf("unknown title = empty nil-error, got %+v / %v", unknown, err)
	}
}

// TestStudioUsageLimitClamp: the page size pins to 1..500 whatever the
// caller passes (0/negative → the 100 default, oversized → 500).
func TestStudioUsageLimitClamp(t *testing.T) {
	for in, want := range map[int]int{0: 100, -5: 100, 1: 1, 300: 300, 99999: 500} {
		if got := clampUsageLimit(in); got != want {
			t.Fatalf("clampUsageLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestUsageSummaryRealHomeSmoke is the one touch of the real ~/.zcode
// (read-only): whatever exists there must decode without error — a
// schema drift on the CLI side should fail HERE loudly, not silently
// in the panel.
func TestUsageSummaryRealHomeSmoke(t *testing.T) {
	if _, err := os.Stat(mustHome(t, filepath.Join(".zcode", "cli", "db", "db.sqlite"))); err != nil {
		t.Skip("no real CLI store on this machine")
	}
	ov, err := StudioUsageOverview()
	if err != nil {
		t.Fatalf("StudioUsageOverview against the real home: %v", err)
	}
	for _, r := range ov.Employees {
		if r.NetInput < 0 || r.Total < 0 {
			t.Fatalf("negative ledger fields on %q: %+v", r.Title, r)
		}
	}
	if ov.Series == nil || ov.Series.Hour == nil || ov.Series.Day == nil || ov.Series.Week == nil || ov.Series.Month == nil {
		t.Fatal("real home 应四档齐备")
	}
}

// TestUsageSeriesFillsFourGridsWithModels: 四档日历网格各就各位——小时
// 48 桶 / 天 30 / 周 12 / 月 12（旧→新、空桶补零、本地日历锚定），轮走
// turn_usage、模型走 model_usage 自带的 started_at（不用 join），今天
// 的桶收当轮的双模型分摊（大头在前），昨天的一轮落昨日桶；操作者自己
// 的轮和模型请求再大也不进任何档；全史按模型合计单列一榜降序。
func TestUsageSeriesFillsFourGridsWithModels(t *testing.T) {
	usageDB, indexDB := makeUsageFixture(t)
	seedUsageTask(t, indexDB, "sess_niu", "【Niuma_Studio】小牛-排期编排-Lv.8", `{"dhEmployee":true}`)
	seedUsageTask(t, indexDB, "sess_own", "重新编译", `{}`)
	now := time.Now()
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	monday := day0.AddDate(0, 0, -(int(now.Weekday())+6)%7)
	hour0 := now.Truncate(time.Hour)
	month0 := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	yday := day0.AddDate(0, 0, -1).Add(time.Hour) // 昨天此时（落昨日桶）
	// 今天两轮（total 100+60）＋昨天一轮（40）；操作者今天的大轮不算。
	seedUsageTurn(t, usageDB, "sess_niu", "turn_now", now.UnixMilli(), 500, 50, 400, 100, nil)
	seedUsageTurn(t, usageDB, "sess_niu", "turn_now2", now.UnixMilli(), 100, 10, 90, 60, nil)
	seedUsageTurn(t, usageDB, "sess_niu", "turn_yday", yday.UnixMilli(), 300, 30, 270, 40, nil)
	seedUsageTurn(t, usageDB, "sess_own", "turn_own", now.UnixMilli(), 999, 99, 900, 999, nil)
	// 今天的桶挂双模型（70/30）＋昨天一个（40）；操作者的不算。
	seedUsageModel(t, usageDB, "sess_niu", "turn_now", "GLM-5.3", 70, now.UnixMilli())
	seedUsageModel(t, usageDB, "sess_niu", "turn_now", "GLM-5.3-Flash", 30, now.UnixMilli())
	seedUsageModel(t, usageDB, "sess_niu", "turn_yday", "GLM-5.3", 40, yday.UnixMilli())
	seedUsageModel(t, usageDB, "sess_own", "turn_own", "GLM-5.3", 999, now.UnixMilli())

	ov, err := usageOverviewAt(usageDB, indexDB)
	if err != nil {
		t.Fatalf("usageOverviewAt: %v", err)
	}
	rep := ov.Series
	for lv, want := range map[string]int{"hour": 48, "day": 30, "week": 12, "month": 12} {
		s := map[string]*UsageSeries{"hour": rep.Hour, "day": rep.Day, "week": rep.Week, "month": rep.Month}[lv]
		if s == nil || s.Bucket != lv || len(s.Points) != want {
			t.Fatalf("%s 档应 %d 桶，got %+v", lv, want, s)
		}
	}
	// 小时档：最新桶＝当前整点收今天两轮（in 600 = 500+100、cache 490），
	// 双模型降序；最老一桶是空桶补零。
	last := rep.Hour.Points[47]
	if last.T != hour0.UnixMilli() || last.Turns != 2 || last.Total != 160 {
		t.Fatalf("小时档最新桶应整点锚定收两轮 160，got T=%d turns=%d total=%d", last.T, last.Turns, last.Total)
	}
	if last.Input != 600 || last.Output != 60 || last.Cache != 490 {
		t.Fatalf("小时档最新桶 in/out/cache = %d/%d/%d, want 600/60/490", last.Input, last.Output, last.Cache)
	}
	if len(last.Models) != 2 || last.Models[0].Model != "GLM-5.3" || last.Models[0].Tokens != 70 ||
		last.Models[1].Model != "GLM-5.3-Flash" || last.Models[1].Tokens != 30 {
		t.Fatalf("小时档最新桶模型分摊降序 70/30，got %+v", last.Models)
	}
	oldest := rep.Hour.Points[0]
	if oldest.Total != 0 || oldest.T != hour0.Add(-47*time.Hour).UnixMilli() {
		t.Fatalf("小时档最老桶应空补零锚定 47h 前，got %+v", oldest)
	}
	// 天档：今天 160、昨天 40 各落各桶（本地日历锚定）。
	if d := rep.Day.Points[29]; d.T != day0.UnixMilli() || d.Total != 160 {
		t.Fatalf("天档最新桶应今日零点收 160，got T=%d total=%d", d.T, d.Total)
	}
	if d := rep.Day.Points[28]; d.T != day0.AddDate(0, 0, -1).UnixMilli() || d.Total != 40 {
		t.Fatalf("天档昨日桶应昨日零点收 40，got T=%d total=%d", d.T, d.Total)
	}
	// 周档最新桶锚定本周一零点（昨天的轮可能跨周，只钉锚点）；月档最新
	// 桶锚定本月一号。
	if w := rep.Week.Points[11]; w.T != monday.UnixMilli() || w.Total < 160 {
		t.Fatalf("周档最新桶应周一零点锚定，got T=%d total=%d", w.T, w.Total)
	}
	if m := rep.Month.Points[11]; m.T != month0.UnixMilli() || m.Total < 160 {
		t.Fatalf("月档最新桶应本月一号锚定，got T=%d total=%d", m.T, m.Total)
	}
	// 四档的总账都只收工作室的 200（100+60+40），operator 的 999 不进。
	for lv, s := range map[string]*UsageSeries{"hour": rep.Hour, "day": rep.Day, "week": rep.Week, "month": rep.Month} {
		var sum int64
		for _, p := range s.Points {
			sum += p.Total
		}
		if sum != 200 {
			t.Fatalf("%s 档全格合计应 200（operator 不进账），got %d", lv, sum)
		}
	}
	// 全史模型榜：大头在前、operator 的请求不进榜。
	if len(rep.Models) != 2 {
		t.Fatalf("全史模型榜应 2 个模型（operator 剔除），got %+v", rep.Models)
	}
	if rep.Models[0].Model != "GLM-5.3" || rep.Models[0].Tokens != 110 || rep.Models[0].Requests != 2 {
		t.Fatalf("全史模型榜首位应 GLM-5.3 ×2 = 110，got %+v", rep.Models[0])
	}
	if rep.Models[1].Model != "GLM-5.3-Flash" || rep.Models[1].Tokens != 30 {
		t.Fatalf("全史模型榜次位应 GLM-5.3-Flash = 30，got %+v", rep.Models[1])
	}
}

// TestUsageSeriesMissingDBReadsEmpty: no CLI store — four empty lines,
// never an error (a studio with no ledger has no trend to draw).
func TestUsageSeriesMissingDBReadsEmpty(t *testing.T) {
	dir := t.TempDir()
	ov, err := usageOverviewAt(filepath.Join(dir, "无.sqlite"), filepath.Join(dir, "i.sqlite"))
	if err != nil || ov == nil {
		t.Fatalf("缺库＝空面不报错（got %+v / %v）", ov, err)
	}
	rep := ov.Series
	if rep.Hour == nil || len(rep.Hour.Points) != 0 || rep.Day == nil || rep.Week == nil || rep.Month == nil {
		t.Fatalf("缺库应四档各空表：%+v", rep)
	}
	if len(rep.Models) != 0 {
		t.Fatalf("缺库模型榜应空：%+v", rep.Models)
	}
}

// TestUsageSeriesRealHomeSmoke is the second touch of the real ~/.zcode
// (read-only): the four grids must decode at full length — schema drift
// on the model_usage side should fail HERE loudly.
func TestUsageSeriesRealHomeSmoke(t *testing.T) {
	if _, err := os.Stat(mustHome(t, filepath.Join(".zcode", "cli", "db", "db.sqlite"))); err != nil {
		t.Skip("no real CLI store on this machine")
	}
	ov, err := StudioUsageOverview()
	if err != nil {
		t.Fatalf("StudioUsageOverview against the real home: %v", err)
	}
	rep := ov.Series
	if rep.Hour == nil || rep.Day == nil || rep.Week == nil || rep.Month == nil {
		t.Fatal("real home 应四档齐备")
	}
	if len(rep.Hour.Points) != 48 || len(rep.Day.Points) != 30 || len(rep.Week.Points) != 12 || len(rep.Month.Points) != 12 {
		t.Fatalf("real home 网格长度走样：%d/%d/%d/%d", len(rep.Hour.Points), len(rep.Day.Points), len(rep.Week.Points), len(rep.Month.Points))
	}
	for _, m := range rep.Models {
		if m.Tokens < 0 || m.Requests < 0 {
			t.Fatalf("negative model totals on %q: %+v", m.Model, m)
		}
	}
}

// mustHome resolves a home-relative path for the smoke test's Stat.
func mustHome(t *testing.T, rel string) string {
	t.Helper()
	p, err := HomePath(rel)
	if err != nil {
		t.Skip("no home dir")
	}
	return p
}

// TestUsageWindowsSumsAnchoredSpend: the two anchored window sums — turns
// inside each anchor fold under the studio scope arms, the operator's
// own spend never counts, and a missing usage db reads as zero (the
// breaker stays open on a studio with no ledger).
func TestUsageWindowsSumsAnchoredSpend(t *testing.T) {
	usageDB, indexDB := makeUsageFixture(t)
	seedUsageTask(t, indexDB, "sess_niu", "【Niuma_Studio】小牛-排期编排-Lv.8", `{"dhEmployee":true}`)
	seedUsageTask(t, indexDB, "sess_own", "重新编译", `{}`)
	// 锚点：周＝1000、日＝2000。轮次：2500 进两窗、1500 只进周窗、
	// 500 两窗都不进；操作者 2500 的轮子再大也不算工作室的账。
	//（seedUsageTurn 位序：started, in, out, cache, total, dur）
	seedUsageTurn(t, usageDB, "sess_niu", "turn_now", 2500, 500, 50, 400, 100, nil)
	seedUsageTurn(t, usageDB, "sess_niu", "turn_week", 1500, 700, 30, 650, 20, nil)
	seedUsageTurn(t, usageDB, "sess_niu", "turn_old", 500, 900, 90, 800, 7, nil)
	seedUsageTurn(t, usageDB, "sess_own", "turn_own", 2500, 999, 99, 900, 999, nil)

	day, week, err := usageWindowsAt(usageDB, indexDB, 2000, 1000)
	if err != nil {
		t.Fatalf("usageWindowsAt: %v", err)
	}
	if day != 100 {
		t.Fatalf("日窗只收 started_at≥日锚的轮（got %d want 100）", day)
	}
	if week != 120 {
		t.Fatalf("周窗收 started_at≥周锚的轮（got %d want 120）", week)
	}

	// 恰在锚点上的轮算进窗（闭区间下界）
	day, week, err = usageWindowsAt(usageDB, indexDB, 1500, 1500)
	if err != nil {
		t.Fatalf("usageWindowsAt(同锚): %v", err)
	}
	if day != 120 || week != 120 {
		t.Fatalf("锚点上的轮应入窗（got day %d week %d want 120/120）", day, week)
	}
}

// TestUsageWindowsMissingDBReadsZero: no CLI store on the machine — zero
// spend, zero error (a studio without a ledger cannot be over budget).
func TestUsageWindowsMissingDBReadsZero(t *testing.T) {
	dir := t.TempDir()
	day, week, err := usageWindowsAt(filepath.Join(dir, "none.sqlite"),
		filepath.Join(dir, "index.sqlite"), 0, 0)
	if err != nil || day != 0 || week != 0 {
		t.Fatalf("缺库＝零耗粮不报错（got %d/%d/%v）", day, week, err)
	}
}

// TestSessionGaugesReadsNewestTurnContext — P1 上下文用量仪表的底层数：
// 采样轮＝最新非零输入轮（零输入空轮不掩真样），input_tokens 是一轮内
// 全部模型请求的求和——ctx_estimate＝input÷请求数才是活体上下文尺寸
// 的估计；turns/total 为回合龄与全史消耗；未知会话不出现、空入参与缺
// 库都不报错（读统计不打红别的脸）。
func TestSessionGaugesReadsNewestTurnContext(t *testing.T) {
	usageDB, _ := makeUsageFixture(t)
	seedUsageTurnRqs(t, usageDB, "s-1", "t1", 1000, 5000, 200, 4000, 6000, nil, 2)
	seedUsageTurnRqs(t, usageDB, "s-1", "t2", 2000, 12000, 300, 9000, 15000, nil, 3)
	seedUsageTurnRqs(t, usageDB, "s-2", "t1", 3000, 700, 100, 700, 1400, nil, 1)
	// s-3：真实轮之后跟一条零输入空轮（中止回合）——采样必须仍是真实轮
	seedUsageTurnRqs(t, usageDB, "s-3", "t1", 5000, 500, 50, 400, 900, nil, 1)
	seedUsageTurnRqs(t, usageDB, "s-3", "t2", 6000, 0, 0, 0, 0, nil, 0)

	g, err := sessionGaugesAt(usageDB, []string{"s-1", "s-2", "s-3", "s-ghost", "", "s-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g["s-ghost"]; ok {
		t.Fatal("未知会话不该出现在仪表里")
	}
	if len(g) != 3 {
		t.Fatalf("应恰有 3 个会话，实 %d：%v", len(g), g)
	}
	a := g["s-1"]
	if a.Turns != 2 || a.LastInput != 12000 || a.LastCache != 9000 || a.NetInput != 3000 {
		t.Fatalf("s-1 仪表走样：%+v", a)
	}
	if a.LastRequests != 3 || a.CtxEstimate != 4000 {
		t.Fatalf("s-1 上下文估计应 12000÷3=4000：%+v", a)
	}
	if a.Total != 21000 || a.LastAt != 2000 {
		t.Fatalf("s-1 累计/时刻走样：%+v", a)
	}
	b := g["s-2"]
	if b.Turns != 1 || b.LastInput != 700 || b.NetInput != 0 || b.CtxEstimate != 700 {
		t.Fatalf("s-2 仪表走样：%+v", b)
	}
	c := g["s-3"]
	if c.Turns != 2 || c.LastInput != 500 || c.LastAt != 5000 {
		t.Fatalf("零空轮不掩真样：%+v", c)
	}

	// 空入参：不起子进程，直接空表
	if g, err := sessionGaugesAt(usageDB, nil); err != nil || len(g) != 0 {
		t.Fatalf("空入参应空表不报错：%v %v", g, err)
	}
	// 缺库：空表不报错
	if g, err := sessionGaugesAt(filepath.Join(t.TempDir(), "无.sqlite"), []string{"s-1"}); err != nil || len(g) != 0 {
		t.Fatalf("缺库应空表不报错：%v %v", g, err)
	}
}
