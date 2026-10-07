package server

// usage_test.go — 耗粮读面的行为钉子：
//   - usageCache：30s 窗内合并台账读（两次 overview 一次子进程）、warm
//     是同一把合并的后台拍（热缓存上的预热白拿、过窗预热填热）、error
//     一并缓存（台账挂时不每拍轰炸）、过窗自愈重读、明细页按 title|limit
//     分 key；
//   - HTTP 面：/usage 一包带回员工表＋趋势面；Accept-Encoding: gzip 时
//     回压缩体（解压即原 JSON），不带的客户端拿明文；缓存命中不重复起
//     台账读。

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/zcode"
)

// countingLedger is the usageCache stub: canned overview plus a call count.
// calls 是原子计数——warm() 的后台 goroutine 写它，测试主协程的断言与
// waitCalls 读它，裸 int 会是数据竞态。
type countingLedger struct {
	calls atomic.Int64
	fail  bool
	ov    *zcode.UsageOverview // 缺省一行员工＋一格趋势；要过 gzip 门槛时由测试加宽
	turns map[string]int       // "title|limit" → calls
}

func (l *countingLedger) overview() (*zcode.UsageOverview, error) {
	l.calls.Add(1)
	if l.fail {
		return nil, io.ErrUnexpectedEOF
	}
	if l.ov == nil {
		l.ov = &zcode.UsageOverview{
			Employees: []zcode.UsageEmployee{{Title: "【Niuma_Studio】小牛-排期编排-Lv.8", Total: 3}},
			Series: &zcode.UsageSeriesReport{
				Hour:   &zcode.UsageSeries{Bucket: "hour", Points: []zcode.UsagePoint{{Total: 7}}},
				Day:    &zcode.UsageSeries{Bucket: "day"},
				Week:   &zcode.UsageSeries{Bucket: "week"},
				Month:  &zcode.UsageSeries{Bucket: "month"},
				Models: []zcode.UsageModelTotal{{Model: "GLM-5.3", Tokens: 7}},
			},
		}
	}
	return l.ov, nil
}

func (l *countingLedger) page(title string, limit int) (*zcode.UsageTurnsReport, error) {
	key := title + "|" + strconv.Itoa(limit)
	if l.turns == nil {
		l.turns = map[string]int{}
	}
	l.turns[key]++
	if l.fail {
		return nil, io.ErrUnexpectedEOF
	}
	return &zcode.UsageTurnsReport{Title: title, Turns: []zcode.UsageTurn{{Total: 1}}}, nil
}

// TestUsageCacheCoalescesReads: 窗内两次 overview 只付一次子进程，过窗
// 重读拿到新值——驾驶舱/面板反复开合不再每拍起 python3。
func TestUsageCacheCoalescesReads(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := &countingLedger{}
	c := &usageCache{ttl: 30 * time.Second, now: func() time.Time { return clock }, read: l.overview}

	ov, err := c.overview()
	if err != nil || len(ov.Employees) != 1 || ov.Employees[0].Total != 3 {
		t.Fatalf("首读: err=%v ov=%+v", err, ov)
	}
	if _, err = c.overview(); err != nil {
		t.Fatalf("窗内二读: %v", err)
	}
	if l.calls.Load() != 1 {
		t.Fatalf("30s 窗内两次 overview 应合并成一读，实付 %d 次", l.calls.Load())
	}
	clock = clock.Add(31 * time.Second)
	ov, err = c.overview()
	if err != nil || len(ov.Employees) != 1 {
		t.Fatalf("过窗重读: err=%v ov=%+v", err, ov)
	}
	if l.calls.Load() != 2 {
		t.Fatalf("过窗应重读，实付 %d 次", l.calls.Load())
	}
}

// TestUsageCacheWarmRidesTheWindow: warm 是 overview 的后台拍——热缓存
// 上的预热不再付子进程（面板页加载钩子反复触发也合并成零读），冷却后
// 的预热把缓存重新填热，紧随的同步读直接命中不再付。
func TestUsageCacheWarmRidesTheWindow(t *testing.T) {
	// clock 走互斥锁：now 闭包在 warm 的后台 goroutine 里被调，主协程
	// 过窗拨针（clock = clock.Add）若裸写就是数据竞态。
	var clkMu sync.Mutex
	clock := time.Unix(1_700_000_000, 0)
	nowFn := func() time.Time {
		clkMu.Lock()
		defer clkMu.Unlock()
		return clock
	}
	l := &countingLedger{}
	c := &usageCache{ttl: 30 * time.Second, now: nowFn, read: l.overview}

	c.warm() // 冷缓存：后台一读
	waitCalls(t, l, 1)
	c.warm() // 热缓存：撞窗合并，不再付
	c.warm()
	time.Sleep(5 * time.Millisecond)
	if l.calls.Load() != 1 {
		t.Fatalf("窗内预热应合并成零读，实付 %d 次", l.calls.Load())
	}
	if ov, err := c.overview(); err != nil || len(ov.Employees) != 1 {
		t.Fatalf("预热后的同步读应直接命中: %v", err)
	}
	if l.calls.Load() != 1 {
		t.Fatalf("预热后的读不应再付子进程，实付 %d 次", l.calls.Load())
	}
	clkMu.Lock()
	clock = clock.Add(31 * time.Second)
	clkMu.Unlock()
	c.warm()
	waitCalls(t, l, 2)
	if _, err := c.overview(); err != nil {
		t.Fatalf("热缓存同步读: %v", err)
	}
	if l.calls.Load() != 2 {
		t.Fatalf("过窗后 warm+读应合并成一次，实付 %d 次", l.calls.Load())
	}
}

// waitCalls spins until the background warm lands (goroutine pacing).
func waitCalls(t *testing.T, l *countingLedger, want int64) {
	t.Helper()
	for i := 0; i < 100 && l.calls.Load() < want; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	if l.calls.Load() != want {
		t.Fatalf("后台预热应恰付 %d 次读，实付 %d 次", want, l.calls.Load())
	}
}

// TestUsageCacheCachesError: 台账挂时错误一并入窗——30s 内重试不再轰
// 子进程，过窗自愈。
func TestUsageCacheCachesError(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := &countingLedger{fail: true}
	c := &usageCache{ttl: 30 * time.Second, now: func() time.Time { return clock }, read: l.overview}

	if _, err := c.overview(); err == nil {
		t.Fatal("挂的台账应回错误")
	}
	if _, err := c.overview(); err == nil {
		t.Fatal("窗内二读仍应回缓存的原错误")
	}
	if l.calls.Load() != 1 {
		t.Fatalf("错误入窗后不应再付子进程，实付 %d 次", l.calls.Load())
	}
	l.fail = false
	clock = clock.Add(31 * time.Second)
	if _, err := c.overview(); err != nil {
		t.Fatalf("过窗自愈重读: %v", err)
	}
	if l.calls.Load() != 2 {
		t.Fatalf("过窗应重读，实付 %d 次", l.calls.Load())
	}
}

// TestUsageCacheTurnsKeysByTitle: 明细页按 title|limit 分 key——同一
// 员工窗内只读一次，换员工/换页宽各自起读。
func TestUsageCacheTurnsKeysByTitle(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := &countingLedger{}
	c := &usageCache{ttl: 30 * time.Second, now: func() time.Time { return clock }, page: l.page}

	if _, err := c.turns("甲", 100); err != nil {
		t.Fatalf("甲首读: %v", err)
	}
	if _, err := c.turns("甲", 100); err != nil {
		t.Fatalf("甲二读: %v", err)
	}
	if _, err := c.turns("乙", 100); err != nil {
		t.Fatalf("乙首读: %v", err)
	}
	if _, err := c.turns("甲", 20); err != nil {
		t.Fatalf("甲窄页: %v", err)
	}
	if l.turns["甲|100"] != 1 || l.turns["乙|100"] != 1 || l.turns["甲|20"] != 1 {
		t.Fatalf("明细缓存分 key 不对: %v", l.turns)
	}
}

// TestUsageHandlerGzipsAndCaches: HTTP 面的压缩与缓存联动——一包带回
// 员工表＋趋势面；带 gzip 协商的客户端拿压缩体（解压即原 JSON），明文
// 客户端逐字节同形，窗内二拍不再付台账读。
func TestUsageHandlerGzipsAndCaches(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := &countingLedger{ov: &zcode.UsageOverview{Employees: []zcode.UsageEmployee{}, Series: &zcode.UsageSeriesReport{}}}
	for i := 0; i < 60; i++ { // 60 行越过 gzipMinSize，逼出压缩路径
		l.ov.Employees = append(l.ov.Employees, zcode.UsageEmployee{
			Title: "【Niuma_Studio】小牛-排期编排-Lv.8（转世第" + strconv.Itoa(i) + "代，标题长一点才好压）",
			Total: int64(i),
		})
	}
	l.ov.Series = &zcode.UsageSeriesReport{
		Hour:   &zcode.UsageSeries{Bucket: "hour", Points: []zcode.UsagePoint{{Total: 7}}},
		Day:    &zcode.UsageSeries{Bucket: "day"},
		Week:   &zcode.UsageSeries{Bucket: "week"},
		Month:  &zcode.UsageSeries{Bucket: "month"},
		Models: []zcode.UsageModelTotal{{Model: "GLM-5.3", Tokens: 7}},
	}
	s := &Server{usage: &usageCache{ttl: 30 * time.Second, now: func() time.Time { return clock }, read: l.overview}}

	gzReq := httptest.NewRequest("GET", "/usage", nil)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	s.handleUsage(rec, gzReq)
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("gzip 协商应回压缩体，Content-Encoding=%q", ce)
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip 体打不开: %v", err)
	}
	var decoded struct {
		Count  int `json:"count"`
		Series struct {
			Hour struct {
				Points []struct {
					Total int64 `json:"total"`
				} `json:"points"`
			} `json:"hour"`
		} `json:"series"`
	}
	if err := json.NewDecoder(gr).Decode(&decoded); err != nil {
		t.Fatalf("解压后应即原 JSON: %v", err)
	}
	if decoded.Count != 60 {
		t.Fatalf("count=%d, want 60", decoded.Count)
	}
	if len(decoded.Series.Hour.Points) != 1 || decoded.Series.Hour.Points[0].Total != 7 {
		t.Fatalf("一包应带齐趋势面: %+v", decoded.Series)
	}

	plainReq := httptest.NewRequest("GET", "/usage", nil)
	rec2 := httptest.NewRecorder()
	s.handleUsage(rec2, plainReq)
	if rec2.Header().Get("Content-Encoding") != "" {
		t.Fatal("无协商的客户端应拿明文")
	}
	if !bytes.Contains(rec2.Body.Bytes(), []byte(`"count":60`)) {
		t.Fatal("明文体应与压缩体同形")
	}
	if l.calls.Load() != 1 {
		t.Fatalf("窗内两拍应合并成一读，实付 %d 次", l.calls.Load())
	}
}
