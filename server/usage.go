package server

// usage.go — 耗粮统计的只读 HTTP 面（GET /usage 与 /usage/turns）：
// 把 CLI 记的 token 台账按牛马员工摊开。数据全在 zcode.StudioUsage*
// （usage.go 的注释讲了 join 的来龙去脉）；这里只做门卫——GET only、
// limit 钳制、错误带中文原因回 writeJSONErr——和 /kb/* 读面同一纪律。
// 读的是本机 ~/.zcode 的库，10 秒帽在 zcode 层；缺库 = 空表（还没出生
// 过的工作室），不是 5xx。/usage 一次回齐员工表＋四档趋势＋模型对比
// （StudioUsageOverview 一趟扫账），usageCache 把 30s 窗内的撞击合并
// 成至多一读。

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// usageCache is the ledger read's short-TTL cache：一次 StudioUsageOverview
// 要起一个 python3 子进程扫两份 sqlite——耗粮面板每开一次、每点一名员工
// 都是一撞，30s 窗内合并成至多一读（面板页加载时的 warm 也吃这把合并，
// 冷却中的重入不再起子进程）。台账只增不减，30s 的陈旧对「终身累计」
// 无感；error 一并缓存（台账挂时不每点一下轰一个子进程，30s 后自愈重
// 读）。明细页按 title|limit 入 key：条目数以员工数为上界（几十），不
// 设淘汰。overview 与 turns 各持一拍、互不等待，锁内读（并发首撞天然
// 合并成一次子进程——spendCache 同款）。
type usageCache struct {
	ttl  time.Duration
	now  func() time.Time                                               // 可换的钟（测试）
	read func() (*zcode.UsageOverview, error)                           // 可换的台账（测试）
	page func(title string, limit int) (*zcode.UsageTurnsReport, error) // 可换的明细（测试）

	mu   sync.Mutex
	at   time.Time
	ov   *zcode.UsageOverview
	oerr error

	pages map[string]usagePage
}

// usagePage is one cached /usage/turns answer (value + error together).
type usagePage struct {
	at  time.Time
	rep *zcode.UsageTurnsReport
	err error
}

func newUsageCache() *usageCache {
	return &usageCache{
		ttl:   30 * time.Second,
		now:   time.Now,
		read:  zcode.StudioUsageOverview,
		page:  zcode.StudioUsageTurns,
		pages: map[string]usagePage{},
	}
}

// overview resolves the drawer's whole diet through the cache —
// nil-safe (a bare &Server{} built by a test reads the ledger directly,
// byte-identical to the pre-cache behavior).
func (c *usageCache) overview() (*zcode.UsageOverview, error) {
	if c == nil {
		return zcode.StudioUsageOverview()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.now().Sub(c.at) < c.ttl {
		return c.ov, c.oerr
	}
	ov, err := c.read()
	c.at, c.ov, c.oerr = c.now(), ov, err
	return ov, err
}

// warm kicks one overview read in the background — the workbench page
// load's 提前量（handleEntry 挂的钩子）：用户点开抽屉前缓存多半已热，
// 首开即渲染。撞进 30s 窗内的重复预热天然合并，不白起子进程。
func (c *usageCache) warm() {
	if c == nil {
		return
	}
	go func() { _, _ = c.overview() }()
}

// turns resolves one employee's page through the cache (same window).
func (c *usageCache) turns(title string, limit int) (*zcode.UsageTurnsReport, error) {
	if c == nil {
		return zcode.StudioUsageTurns(title, limit)
	}
	key := title + "\x00" + strconv.Itoa(limit)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pages == nil {
		c.pages = map[string]usagePage{} // 字面量构造的缓存（测试）也安全
	}
	if p, ok := c.pages[key]; ok && !p.at.IsZero() && c.now().Sub(p.at) < c.ttl {
		return p.rep, p.err
	}
	rep, err := c.page(title, limit)
	c.pages[key] = usagePage{at: c.now(), rep: rep, err: err}
	return rep, err
}

// handleUsage serves GET /usage: every studio-born employee's lifetime
// consumption (biggest spender first) with the studio roll-up, plus the
// four-bucket trend and per-model ranking — the drawer renders the whole
// face from this one answer.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/usage" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	ov, err := s.usage.overview()
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("耗粮台账读取失败：%s", err.Error()))
		return
	}
	employees := ov.Employees
	var total zcode.UsageEmployee
	total.Title = i18n.S("全工作室")
	for _, e := range employees {
		total.Sessions += e.Sessions
		total.Turns += e.Turns
		total.Input += e.Input
		total.Output += e.Output
		total.Reasoning += e.Reasoning
		total.CacheRead += e.CacheRead
		total.NetInput += e.NetInput
		total.Total += e.Total
		if e.LastActive > total.LastActive {
			total.LastActive = e.LastActive
		}
	}
	writeJSONZip(w, r, map[string]any{
		"employees": employees,
		"count":     len(employees),
		"total":     total,
		"series":    ov.Series,
	})
}

// handleUsageTurns serves GET /usage/turns?employee=<标题>&limit=N: one
// employee's turns, newest first (limit 1..500, default 100), each with
// its per-model split. An unknown employee reads as zero turns.
func (s *Server) handleUsageTurns(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/usage/turns" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	employee := r.URL.Query().Get("employee")
	if employee == "" {
		writeJSONErr(w, http.StatusBadRequest, i18n.S("缺 employee 参数（员工标题，如【Niuma_Studio】小牛-排期编排-Lv.8）"))
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeJSONErr(w, http.StatusBadRequest, "bad limit: want a positive count")
			return
		}
		limit = n
	}
	rep, err := s.usage.turns(employee, limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("耗粮明细读取失败：%s", err.Error()))
		return
	}
	writeJSONZip(w, r, rep)
}
