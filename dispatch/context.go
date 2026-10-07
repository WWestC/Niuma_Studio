package dispatch

// context.go — P1 上下文用量仪表（v2.11）：成员会话的活体上下文尺寸
// 读面。数据源是 ZCode CLI 的耗粮台账（zcode/usage.go 的 turn_usage，
// 每回合一行）——采样轮＝最新非零输入轮，其 input_tokens 是该轮全部
// 模型请求的求和（不是单请求上下文），ctx_estimate＝input÷请求数才是
// 活体上下文尺寸的估计；cache_read 是缓存命中的一半，net_input 是净
// 新量。调度器手里就有每个成员的 session_id：直查，不走标题 join。
//
// 这是 P0（周期性会话折叠）的前置传感器——没有尺寸就没有阈值，阈值
// 不该拍脑袋。GET /dispatch/context 供房主工作台与 CLI 读「谁在膨
// 胀」：turns 是会话回合龄，ctx_estimate 是现役上下文估计，total 是
// 该会话全史消耗。

import (
	"log"
	"sort"

	"github.com/WWestC/Niuma_Studio/zcode"
)

// MemberContextGauge is one member's row in the context gauge. 口径：
// last_input 是采样轮（最新非零输入轮）全部模型请求的 input 求和，
// ctx_estimate 才是活体上下文尺寸的估计（÷该轮请求数）——P0 折叠阈值
// 读它，别读 last_input（满载实测：小笔 1.70M÷3≈56.8 万，差 3–7 倍）。
type MemberContextGauge struct {
	Name         string `json:"name"`
	Session      string `json:"session"`
	Status       string `json:"status"`
	Known        bool   `json:"known"` // the ledger answered for this session (false = 无台账/从未跑过回合)
	Turns        int64  `json:"turns"`
	LastInput    int64  `json:"last_input"` // sampled turn's summed input
	LastCache    int64  `json:"last_cache"`
	NetInput     int64  `json:"net_input"`     // last_input − last_cache（净新量）
	LastRequests int64  `json:"last_requests"` // sampled turn's model request count
	CtxEstimate  int64  `json:"ctx_estimate"`  // ≈ live context size: last_input ÷ max(1, requests)
	Total        int64  `json:"total"`         // session lifetime computed_total_tokens
	LastAt       int64  `json:"last_at"`       // ms epoch of the sampled turn
}

// ProjectContextGauges is one project room's member gauges.
type ProjectContextGauges struct {
	Project string               `json:"project"`
	Members []MemberContextGauge `json:"members"`
}

// ContextGauges snapshots every seated member's live context gauge
// across the whole fleet — ONE ledger pass for all sessions. A ledger
// read failure degrades to known=false rows (logged, never an error
// face: 读统计永远不该把别的脸打红).
func (f *Fleet) ContextGauges() []ProjectContextGauges {
	// key 与 dispatcher 成对收集、成对排序——分开排序会把项目标签贴到
	// 别人的成员上（map 迭代序随机，对不对全凭运气；上线实测 book 标
	// 签下出过大厅成员）。
	f.mu.Lock()
	pairs := make([]fleetPair, 0, len(f.byKey))
	for k, d := range f.byKey {
		pairs = append(pairs, fleetPair{key: k, d: d})
	}
	f.mu.Unlock()
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })

	seatsBy := make([][]SeatSession, len(pairs))
	var ids []string
	for i, p := range pairs {
		seatsBy[i] = p.d.SeatSessions()
		for _, s := range seatsBy[i] {
			if s.Session != "" {
				ids = append(ids, s.Session)
			}
		}
	}
	gauges, err := zcode.SessionGauges(ids)
	if err != nil {
		log.Printf("[调度] 上下文仪表读台账失败（本次按无台账降级）：%v", err)
		gauges = map[string]zcode.SessionGauge{}
	}
	out := make([]ProjectContextGauges, 0, len(pairs))
	for i, seats := range seatsBy {
		row := ProjectContextGauges{Project: pairs[i].key, Members: make([]MemberContextGauge, 0, len(seats))}
		for _, s := range seats {
			mg := MemberContextGauge{Name: s.Name, Session: s.Session, Status: s.Status}
			if g, ok := gauges[s.Session]; ok {
				mg.Known = true
				mg.Turns, mg.LastInput, mg.LastCache, mg.NetInput = g.Turns, g.LastInput, g.LastCache, g.NetInput
				mg.LastRequests, mg.CtxEstimate = g.LastRequests, g.CtxEstimate
				mg.Total, mg.LastAt = g.Total, g.LastAt
			}
			row.Members = append(row.Members, mg)
		}
		out = append(out, row)
	}
	return out
}

// fleetPair is ContextGauges' (project key, dispatcher) pair — the unit
// of its sort, so labels never part from their members.
type fleetPair struct {
	key string
	d   *Dispatcher
}
