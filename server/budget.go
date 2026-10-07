package server

// budget.go — token 预算与熔断（r_26）：/usage 有耗粮统计、max_plans
// 有计数帽，但都没有真正的钱面控制。这里补上那块拼图：一组工作室级
// 预算旋钮（日/周 token 上限，budget.json under the v2 root——sendwait
// 同款位形），到额即熔断——只关推进侧、保人力侧，与自动驾驶每日代收
// 上限的「熔断只关推进」（r_19）完全同构：SetAutoAdvance(false)＋房内
// 播报，自动补货、房主手动路、在途任务一概不动。
//
// 真源链（每个旋钮独立走）：budget.json（整份生效）> env
// （NIUMA_BUDGET_DAY_TOKENS / NIUMA_BUDGET_WEEK_TOKENS）> 0（不限，
// 缺省）。0＝不限是明示值：文件在、值为 0 就是关掉这根旋钮（写面
// 允许置 0；env 不因此回填——文件一旦存在即整份做主，sendwait 的
// wholesale 语义）。引擎每拍直读（热更：设置卡松手下一拍即按新值
// 熔断/放开），窗口口径＝本地自然日零点 / 本周一零点（与 apDay 的
// 「日」同钟）。GET /budget 同时回两窗已耗（驾驶舱成本象限与设置卡
// 回显的共同真源——只有一个口径面）。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/autopilot"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// BudgetMaxTokens bounds one knob from above — past a trillion tokens
// the number is no longer a budget but a typo.
const BudgetMaxTokens = autopilot.BudgetMaxTokens

type BudgetPolicy = autopilot.BudgetPolicy // moved: the engine's contract type

// The policy core moved to the autopilot domain (engine-owned);
// these delegators keep the budget face and callers unchanged.

func BudgetPath(root string) string { return autopilot.BudgetPath(root) }

// validBudgetTokens: 0（不限）或 1..BudgetMaxTokens。
func validBudgetTokens(v int64) bool { return autopilot.ValidBudgetTokens(v) }

// LoadBudgetPolicy reads the persisted knobs; ok=false means absent,
// corrupt or out-of-range (the caller walks down to the env leg).
func LoadBudgetPolicy(path string) (BudgetPolicy, bool) { return autopilot.LoadBudgetPolicy(path) }

// SaveBudgetPolicy persists the knobs atomically (tmp + rename — every
// ledger write's discipline). The caller validates.
func SaveBudgetPolicy(path string, pol BudgetPolicy) error {
	return autopilot.SaveBudgetPolicy(path, pol)
}

// spendCache is the ledger read's short-TTL cache：一次 StudioUsageWindows
// 要起一个 python3 子进程扫两份 sqlite，驾驶舱 20s 一拍地撞、熔断拍也
// 同源来读——30s 窗内合并成至多一读（驾驶舱隔拍真读，多消费者合并）。
// 窗口锚点入 key：跨零点/周一自然失效，无需显式作废；error 一并缓存，
// 台账挂时不每拍轰炸子进程（usage_error 照常出网）。POST 只动旋钮不动
// 耗粮，无需失效。
type spendCache struct {
	ttl  time.Duration
	now  func() time.Time                                // 可换的钟（测试）
	read func(dayMS, weekMS int64) (int64, int64, error) // 可换的台账（测试）

	mu            sync.Mutex
	at            time.Time
	dayMS, weekMS int64
	day, week     int64
	err           error
}

func newSpendCache() *spendCache {
	return &spendCache{ttl: 30 * time.Second, now: time.Now, read: zcode.StudioUsageWindows}
}

// get resolves the two windows' spend through the cache — nil-safe (a
// bare &Server{} built by a test reads the ledger directly, byte-
// identical to the pre-cache behavior).
func (c *spendCache) get(dayMS, weekMS int64) (int64, int64, error) {
	if c == nil {
		return zcode.StudioUsageWindows(dayMS, weekMS)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dayMS == dayMS && c.weekMS == weekMS && c.now().Sub(c.at) < c.ttl {
		return c.day, c.week, c.err
	}
	day, week, err := c.read(dayMS, weekMS)
	c.at, c.dayMS, c.weekMS, c.day, c.week, c.err = c.now(), dayMS, weekMS, day, week, err
	return day, week, err
}

// handleBudget serves GET/POST /budget — the settings card·智能 预算
// 旋钮的读写面 and the cockpit cost quadrant's data source. GET folds
// the live spend in (a ledger read failure degrades to usage_error,
// never a 5xx — the knobs still echo); POST overwrites BOTH knobs in
// one shot (a budget is one policy, not two independent sliders racing).
func (s *Server) handleBudget(w http.ResponseWriter, r *http.Request) {
	if s.opts.Registry == nil || s.opts.Registry.Root() == "" {
		http.NotFound(w, r)
		return
	}
	root := s.opts.Registry.Root()
	switch r.Method {
	case http.MethodGet:
		pol := ResolveBudgetPolicy(root)
		day, week := budgetWindows(time.Unix(util.Now(), 0))
		out := map[string]any{
			"day_tokens":   pol.DayTokens,
			"week_tokens":  pol.WeekTokens,
			"day_start":    day.Unix(),
			"week_start":   week.Unix(),
			"max_tokens":   BudgetMaxTokens,
			"breaker_live": s.autopilot != nil,
			"tripped_day":  pol.DayTokens > 0,
			"tripped_week": pol.WeekTokens > 0,
			"usage_error":  false,
			"day_used":     int64(0),
			"week_used":    int64(0),
		}
		if spent, spentW, err := s.spend.get(day.UnixMilli(), week.UnixMilli()); err != nil {
			out["usage_error"] = true // 台账读挂：旋钮照回，耗粮标不可用
		} else {
			out["day_used"] = spent
			out["week_used"] = spentW
			out["tripped_day"] = pol.DayTokens > 0 && spent >= pol.DayTokens
			out["tripped_week"] = pol.WeekTokens > 0 && spentW >= pol.WeekTokens
		}
		writeJSON(w, out)
	case http.MethodPost:
		var body struct {
			DayTokens  *int64 `json:"day_tokens"`
			WeekTokens *int64 `json:"week_tokens"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("载荷缺失"))
		}
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.DayTokens == nil || body.WeekTokens == nil {
			writeJSONErr(w, http.StatusBadRequest,
				i18n.S("预算是成对策略：day_tokens 与 week_tokens 都要带（0=不限）"))
			return
		}
		if !validBudgetTokens(*body.DayTokens) || !validBudgetTokens(*body.WeekTokens) {
			writeJSONErr(w, http.StatusBadRequest,
				i18n.S("预算值需为 0（不限）或 1–1e12 token（到额熔断只关推进，与每日代收上限同语义）"))
			return
		}
		pol := BudgetPolicy{DayTokens: *body.DayTokens, WeekTokens: *body.WeekTokens}
		if err := SaveBudgetPolicy(BudgetPath(root), pol); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("预算写入失败: %s", err.Error()))
			return
		}
		writeJSON(w, map[string]any{"day_tokens": pol.DayTokens, "week_tokens": pol.WeekTokens})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// The policy core's env/windows/format/resolve legs moved to the
// autopilot domain too — engine-owned; these delegators keep the
// shell's callers and tests unchanged.
func budgetPolicyFromEnv() BudgetPolicy { return autopilot.BudgetPolicyFromEnv() }

func ResolveBudgetPolicy(root string) BudgetPolicy { return autopilot.ResolveBudgetPolicy(root) }

func budgetWindows(now time.Time) (dayStart, weekStart time.Time) {
	return autopilot.BudgetWindows(now)
}

func fmtBudgetTokens(n int64) string { return autopilot.FmtBudgetTokens(n) }
