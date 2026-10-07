package zcode

// usage.go — 耗粮台账的读面：牛马员工的 token 消耗从不在工作室自己的
// 存储里——每一轮、每一次模型调用都记在 ZCode CLI 的库（~/.zcode/cli/
// db/db.sqlite）的 turn_usage（每回合一行：输入/输出/思考/缓存读/总计、
// 时长、首 token、模型请求数）与 model_usage（每次模型请求一行，含
// provider/model）里，而 session_id 就是任务索引（~/.zcode/v2/tasks-
// index.sqlite）的 task_id——出生登记（taskindex.go）盖的 dhEmployee 印
// 与【…】…-Lv.N / 【…】小助手-… 标题契约就是「谁是牛马」的判定，与
// purge.go 的三臂同源（桌面端的时间线同步会重写出生行抹掉 dhEmployee，
// 标题臂兜底）。本文件经 python3+sqlite3 只读接缝把两边 join 起来：
//
//   - StudioUsageOverview —— 一趟扫账喂饱整个耗粮抽屉：按员工（标题）
//     聚合的全史消耗（同标题的多条会话——转世重生、跨编制——合并记账）
//     ＋时/天/周/月四档日历分桶趋势＋全史按模型合计，缺库/缺表=空表不
//     报错（还没出生过的工作室本来就没有耗粮）；
//   - StudioUsageTurns —— 一名员工的每轮明细（新→旧，截尾 limit），每轮
//     附 model_usage 折出的按模型分摊（哪个模型几次多少 token）。
//
// 只读面：连接不带 URI ro（与既有接缝一致，busy_timeout=5000 让位于正
// 在写库的 CLI），任何缺席（库不在、表不在、标题查无会话）都折叠成空
// 结果——读统计永远不该把别的脸打红。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UsageEmployee is one studio-born employee's lifetime consumption,
// aggregated over every session the task index files under the title
// (rebirths fold in; the ledger keeps spending history).
type UsageEmployee struct {
	Title      string `json:"title"`
	Sessions   int    `json:"sessions"` // sessions registered under the title (0-turn births included)
	Turns      int    `json:"turns"`
	Input      int64  `json:"input"` // includes the cache-read half
	Output     int64  `json:"output"`
	Reasoning  int64  `json:"reasoning"`
	CacheRead  int64  `json:"cache_read"`
	NetInput   int64  `json:"net_input"`   // input − cache_read (the fresh-context half)
	Total      int64  `json:"total"`       // CLI's computed_total_tokens
	LastActive int64  `json:"last_active"` // ms epoch of the newest turn, 0 = never
}

// UsageModelSplit is one model's share of a turn (model_usage folded).
type UsageModelSplit struct {
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// UsageTurn is one turn of one employee session, newest first.
type UsageTurn struct {
	SessionID     string            `json:"session_id"`
	TurnID        string            `json:"turn_id"`
	Status        string            `json:"status"`
	StartedAt     int64             `json:"started_at"` // ms epoch
	DurationMs    int64             `json:"duration_ms"`
	TTFTMs        int64             `json:"ttft_ms"` // time to first token
	ModelRequests int               `json:"model_requests"`
	ToolCalls     int               `json:"tool_calls"`
	Input         int64             `json:"input"`
	Output        int64             `json:"output"`
	Reasoning     int64             `json:"reasoning"`
	CacheRead     int64             `json:"cache_read"`
	Total         int64             `json:"total"`
	Models        []UsageModelSplit `json:"models,omitempty"`
}

// UsageTurnsReport is StudioUsageTurns' answer: the title's session
// count plus the capped turn rows.
type UsageTurnsReport struct {
	Title    string      `json:"title"`
	Sessions int         `json:"sessions"`
	Turns    []UsageTurn `json:"turns"`
}

// UsageOverview is StudioUsageOverview's answer: the per-employee
// lifetime table and the four-bucket trend riding ONE ledger pass (the
// drawer's whole diet — 员工表与趋势图一口粮，开面板只起一个子进程).
type UsageOverview struct {
	Employees []UsageEmployee    `json:"employees"`
	Series    *UsageSeriesReport `json:"series"`
}

// StudioUsageOverview folds the CLI usage ledger into the studio's
// per-employee lifetime table plus the four calendar-bucketed trend
// lines (hourly 48 / daily 30 / weekly 12 / monthly 12, local-calendar
// anchored and zero-filled) and the all-time per-model ranking — one
// python pass over turn_usage (全史按会话聚合＋近窗分桶同趟) and one
// over model_usage. No CLI store on the machine reads as an empty
// studio: nothing spent, nothing to show.
func StudioUsageOverview() (*UsageOverview, error) {
	usageDB, err := HomePath(filepath.Join("cli", "db", "db.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("zcode: cli db path: %w", err)
	}
	indexDB, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return nil, fmt.Errorf("zcode: task index path: %w", err)
	}
	return usageOverviewAt(usageDB, indexDB)
}

// usageOverviewAt is StudioUsageOverview over explicit dbs (tests).
func usageOverviewAt(usageDB, indexDB string) (*UsageOverview, error) {
	ov := &UsageOverview{Series: &UsageSeriesReport{
		Hour:   &UsageSeries{Bucket: "hour"},
		Day:    &UsageSeries{Bucket: "day"},
		Week:   &UsageSeries{Bucket: "week"},
		Month:  &UsageSeries{Bucket: "month"},
		Models: []UsageModelTotal{},
	}}
	if _, err := os.Stat(usageDB); err != nil {
		ov.Employees = []UsageEmployee{} // no CLI store: no ledger to read
		return ov, nil
	}
	out := usageOverviewJSON(usageDB, indexDB)
	if err := json.Unmarshal(out, ov); err != nil {
		return nil, fmt.Errorf("zcode: usage overview decode: %w", err)
	}
	if ov.Employees == nil {
		ov.Employees = []UsageEmployee{}
	}
	for i := range ov.Employees {
		e := &ov.Employees[i]
		e.NetInput = e.Input - e.CacheRead
		if e.NetInput < 0 {
			e.NetInput = 0
		}
	}
	sort.SliceStable(ov.Employees, func(i, j int) bool { return ov.Employees[i].Total > ov.Employees[j].Total })
	s := ov.Series
	for _, g := range []*UsageSeries{s.Hour, s.Day, s.Week, s.Month} {
		if g != nil && g.Points == nil {
			g.Points = []UsagePoint{}
		}
	}
	if s.Models == nil {
		s.Models = []UsageModelTotal{}
	}
	return ov, nil
}

// StudioUsageTurns lists one employee's turns newest-first (limit
// clamped to 1..500), each carrying its per-model split. An unknown
// title reads as zero turns, never an error.
func StudioUsageTurns(title string, limit int) (*UsageTurnsReport, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("zcode: usage turns need a title")
	}
	limit = clampUsageLimit(limit)
	usageDB, err := HomePath(filepath.Join("cli", "db", "db.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("zcode: cli db path: %w", err)
	}
	indexDB, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return nil, fmt.Errorf("zcode: task index path: %w", err)
	}
	return usageTurnsAt(usageDB, indexDB, title, limit)
}

// usageTurnsAt is StudioUsageTurns over explicit dbs (tests).
func usageTurnsAt(usageDB, indexDB, title string, limit int) (*UsageTurnsReport, error) {
	if _, err := os.Stat(usageDB); err != nil {
		return &UsageTurnsReport{Title: title, Turns: []UsageTurn{}}, nil
	}
	out := usageTurnsJSON(usageDB, indexDB, title, clampUsageLimit(limit))
	var rep UsageTurnsReport
	if err := json.Unmarshal(out, &rep); err != nil {
		return nil, fmt.Errorf("zcode: usage turns decode: %w", err)
	}
	if rep.Turns == nil {
		rep.Turns = []UsageTurn{}
	}
	return &rep, nil
}

// StudioUsageWindows sums the studio's token spend inside two anchored
// windows in ONE ledger pass — the budget breaker's numerator (r_26：
// 日窗＝本地今日零点起、周窗＝本周一零点起，锚点由调用方算好传入，
// ms epoch 闭区间下界). The employee scope rides the same purge 三臂;
// a missing store reads as zero spend (a studio with no ledger cannot
// be over budget — the breaker stays open, never errors the loop).
func StudioUsageWindows(dayStartMS, weekStartMS int64) (int64, int64, error) {
	usageDB, err := HomePath(filepath.Join("cli", "db", "db.sqlite"))
	if err != nil {
		return 0, 0, fmt.Errorf("zcode: cli db path: %w", err)
	}
	indexDB, err := HomePathV2("tasks-index.sqlite")
	if err != nil {
		return 0, 0, fmt.Errorf("zcode: task index path: %w", err)
	}
	return usageWindowsAt(usageDB, indexDB, dayStartMS, weekStartMS)
}

// usageWindowsAt is StudioUsageWindows over explicit dbs (tests).
func usageWindowsAt(usageDB, indexDB string, dayStartMS, weekStartMS int64) (int64, int64, error) {
	if _, err := os.Stat(usageDB); err != nil {
		return 0, 0, nil // no CLI store: nothing was ever spent
	}
	out := usageWindowsJSON(usageDB, indexDB, dayStartMS, weekStartMS)
	var rep struct {
		Day  int64 `json:"day"`
		Week int64 `json:"week"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		return 0, 0, fmt.Errorf("zcode: usage windows decode: %w", err)
	}
	return rep.Day, rep.Week, nil
}

// UsageModelBar is one model's share of a time bucket (model_usage
// folded — it carries its own started_at, no join needed).
type UsageModelBar struct {
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// UsagePoint is one filled time bucket of studio-wide spend (all zero
// when nothing burned in it — the grid is wall-clock, not sparse).
type UsagePoint struct {
	T      int64           `json:"t"` // bucket start, ms epoch (local-calendar anchored)
	Turns  int             `json:"turns"`
	Input  int64           `json:"input"`
	Output int64           `json:"output"`
	Cache  int64           `json:"cache"` // cache-read half of the input
	Total  int64           `json:"total"` // turn_usage.computed_total_tokens summed
	Models []UsageModelBar `json:"models,omitempty"`
}

// UsageSeries is one granularity's filled line, oldest → newest.
type UsageSeries struct {
	Bucket string       `json:"bucket"` // hour | day | week | month
	Points []UsagePoint `json:"points"`
}

// UsageModelTotal is one model's all-time share of studio spend — the
// comparison bars' diet (ranked by tokens, capped at 24).
type UsageModelTotal struct {
	Model    string `json:"model"`
	Requests int    `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// UsageSeriesReport is the trend half of an overview: the four bucketed
// lines plus the all-time per-model ranking.
type UsageSeriesReport struct {
	Hour   *UsageSeries      `json:"hour"`
	Day    *UsageSeries      `json:"day"`
	Week   *UsageSeries      `json:"week"`
	Month  *UsageSeries      `json:"month"`
	Models []UsageModelTotal `json:"models"`
}

// clampUsageLimit pins the turn-page size (server passes its parsed
// query, tests pass raw).
func clampUsageLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

// SessionGauge is one session's live context gauge (v2.11 P1 上下文用
// 量仪表). 口径（满载实测校准）：turn_usage.input_tokens 是一轮内全
// 部模型请求的求和，不是单请求上下文——LastInput 原样保留（烧掉的面
// 积），CtxEstimate 才是活体上下文尺寸的估计（LastInput÷该轮模型请求
// 数；小笔实录 1,704,178/3≈56.8 万，多轮除得非常一致）。选样跳过零
// 输入空轮（中止回合记 0 行，会把真上下文掩成 0）。Turns/Total 给会
// 话的回合龄与全史消耗——P0 折叠阈值读的就是 CtxEstimate 与 Turns。
type SessionGauge struct {
	Turns        int64 `json:"turns"`
	LastInput    int64 `json:"last_input"`    // newest sampled turn's summed input
	LastCache    int64 `json:"last_cache"`    // that turn's cache-read share
	NetInput     int64 `json:"net_input"`     // last_input − last_cache（净新量）
	LastRequests int64 `json:"last_requests"` // that turn's model request count
	CtxEstimate  int64 `json:"ctx_estimate"`  // ≈ live context size: last_input ÷ max(1, requests)
	Total        int64 `json:"total"`
	LastAt       int64 `json:"last_at"` // ms epoch of the sampled turn, 0 = never
}

// SessionGauges reads the live context gauge for each named session in
// ONE ledger pass — keyed by session_id directly (the dispatcher holds
// every member's id; no title join, no index db). Unknown ids simply
// don't appear; a missing store reads as an empty map, never an error
// (读统计永远不该把别的脸打红).
func SessionGauges(sessionIDs []string) (map[string]SessionGauge, error) {
	usageDB, err := HomePath(filepath.Join("cli", "db", "db.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("zcode: cli db path: %w", err)
	}
	return sessionGaugesAt(usageDB, sessionIDs)
}

// sessionGaugesAt is SessionGauges over an explicit db (tests).
func sessionGaugesAt(usageDB string, sessionIDs []string) (map[string]SessionGauge, error) {
	ids := make([]string, 0, len(sessionIDs))
	seen := map[string]bool{}
	for _, id := range sessionIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return map[string]SessionGauge{}, nil
	}
	if _, err := os.Stat(usageDB); err != nil {
		return map[string]SessionGauge{}, nil // no CLI store: no ledger to read
	}
	out := sessionGaugesJSON(usageDB, ids)
	var raw map[string]struct {
		Turns        int64 `json:"turns"`
		LastInput    int64 `json:"last_input"`
		LastCache    int64 `json:"last_cache"`
		LastRequests int64 `json:"last_requests"`
		Total        int64 `json:"total"`
		LastAt       int64 `json:"last_at"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("zcode: session gauges decode: %w", err)
	}
	gauges := make(map[string]SessionGauge, len(raw))
	for id, g := range raw {
		sg := SessionGauge{
			Turns: g.Turns, LastInput: g.LastInput, LastCache: g.LastCache,
			LastRequests: g.LastRequests, Total: g.Total, LastAt: g.LastAt,
		}
		sg.NetInput = sg.LastInput - sg.LastCache
		if sg.NetInput < 0 {
			sg.NetInput = 0
		}
		if n := sg.LastRequests; n > 0 {
			sg.CtxEstimate = sg.LastInput / n
		} else if sg.LastInput > 0 {
			sg.CtxEstimate = sg.LastInput // 单请求轮本身即上下文
		}
		gauges[id] = sg
	}
	return gauges, nil
}
