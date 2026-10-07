package chat

// trace.go — 工作过程（牛马的 ZCode 会话思考/工具/回合可展示流，v2.8）。
//
// dispatcher 从 session/event 通知里拣出可展示的时刻（思考增量、回复
// 草稿、工具调用与结果、模型请求轮次、回合起止、错误），折成 TraceEntry
// 写进 hub 的有界环形缓冲，同时广播一枚 reminder-only 的 "trace" 帧——
// 不进聊天历史、不徽标、不唤醒（read/ack 帧的纪律）：前端只在「工作过
// 程」抽屉开着时消费，冷窗水合走 GET /p/{key}/trace。
//
// 条目语义：think/draft 是可增量拼接的文本流（相邻同源条目在缓冲里尾
// 并）；tool 条目按 toolCallId 原位更新（调用落位、结果回填——ZCode 式
// 工具卡）；turn 是回合分隔线；model/error 是瞬时标记。子智能体会话的
// 条目带 Agent+AgentID，前端缩进渲染。

import (
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// 环形缓冲的帽：单成员条数与全房合计条数（内存与快照体积的闸）。
const (
	TraceCapPerMember = 240
	TraceCapTotal     = 1600
	// TraceTextCap 是 think/draft 合并块的字符帽：超长思考保头尾、去
	// 中段（省略号缝合）——展示价值在头尾，中段是重复推演。
	TraceTextCap = 24_000
)

// TraceEntry and its kind vocabulary now live in the wire
// package (the protocol contract); chat re-exports them from
// wire.go. The ring, fold and flush logic below stays here.

// traceTextMerge 把增量并进已有文本，超帽保头尾。纯函数（测试面）。
func traceTextMerge(prev, delta string) string {
	if prev == "" {
		if len(delta) > TraceTextCap {
			return traceTextTrim(delta)
		}
		return delta
	}
	out := prev + delta
	if len(out) > TraceTextCap {
		return traceTextTrim(out)
	}
	return out
}

// traceTextTrim 头尾各留半帽，中段以省略注记缝合。
func traceTextTrim(s string) string {
	half := TraceTextCap / 2
	return s[:half] + i18n.Sf("\n…（中段省略，共 %s 字）…\n", itoa(len(s))) + s[len(s)-half:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// sameStream 报告两条文本条目是否同一条流（同成员、同 kind、同子会话）
// ——尾并的判据。
func sameStream(a, b TraceEntry) bool {
	return a.From == b.From && a.Kind == b.Kind && a.Agent == b.Agent && a.AgentID == b.AgentID
}

// AppendTrace lands one flush-batch of trace entries in the hub's ring
// and broadcasts it as a reminder-only "trace" frame. Fold rules:
//   - think/draft merge into the member's ring TAIL when it is the same
//     stream (the frame still carries the merged entry so live clients
//     can append client-side the same way);
//   - a tool entry whose Call already sits in the ring UPDATES it in
//     place (position = where the call was made; result fills in);
//   - everything else appends.
//
// Seq/TS are hub-stamped here (the tap never invents them). Safe from
// any goroutine; must not be called with h.mu held.
func (h *Hub) AppendTrace(entries []TraceEntry) {
	if len(entries) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.trace == nil {
		h.trace = map[string][]TraceEntry{}
	}
	out := make([]TraceEntry, 0, len(entries))
	for _, e := range entries {
		if e.From == "" {
			continue
		}
		e.TS = util.Now()
		ring := h.trace[e.From]
		// tool 回填：同 call 的卡原位更新（保位保序号；非零字段覆盖，
		// 零字段继承——result 事件不带 toolName，卡面上的名字不能丢）。
		if e.Kind == TraceTool && e.Call != "" {
			if i := traceFindCall(ring, e.Call); i >= 0 {
				e.Seq = ring[i].Seq // 序号保位——客户端按 seq/call 皆可替换
				e.TS = ring[i].TS
				ring[i] = MergeTraceEntry(ring[i], e)
				h.trace[e.From] = ring
				out = append(out, ring[i])
				continue
			}
		}
		// 文本尾并：同流的 think/draft 并进末条（帧带合并后的全文）。
		if (e.Kind == TraceThink || e.Kind == TraceDraft) && len(ring) > 0 {
			tail := &ring[len(ring)-1]
			if sameStream(*tail, e) && tail.Kind != TraceTool {
				tail.Text = traceTextMerge(tail.Text, e.Text)
				h.trace[e.From] = ring
				out = append(out, *tail)
				continue
			}
		}
		h.traceSeq++
		e.Seq = h.traceSeq
		ring = append(ring, e)
		if len(ring) > TraceCapPerMember {
			ring = ring[len(ring)-TraceCapPerMember:]
		}
		h.trace[e.From] = ring
		out = append(out, e)
	}
	h.traceTrimLocked()
	if len(out) == 0 {
		return
	}
	h.broadcastLocked(Message{Type: MsgTrace, From: out[0].From, Trace: out})
}

// traceFindCall 找 ring 里该 toolCallId 的条目下标（-1 = 无）。
func traceFindCall(ring []TraceEntry, call string) int {
	for i := len(ring) - 1; i >= 0; i-- {
		if ring[i].Kind == TraceTool && ring[i].Call == call {
			return i
		}
	}
	return -1
}

// MergeTraceEntry 原位回填的合并语义：fresh 的非零字段覆盖 old，零字段
// 继承 old（result/error 事件只带 call 与产物，Tool/Agent 归属在首建时
// 已定，不能被回填写没）。dispatcher 的 pending lane 复用同一语义。
func MergeTraceEntry(old, fresh TraceEntry) TraceEntry {
	out := old
	if fresh.Tool != "" {
		out.Tool = fresh.Tool
	}
	if fresh.State != "" {
		out.State = fresh.State
	}
	if fresh.Input != nil {
		out.Input = fresh.Input
	}
	if fresh.Output != "" {
		out.Output = fresh.Output
	}
	if fresh.OK != nil {
		out.OK = fresh.OK
	}
	if fresh.Ms != 0 {
		out.Ms = fresh.Ms
	}
	if fresh.Agent {
		out.Agent = true
	}
	if fresh.AgentID != "" {
		out.AgentID = fresh.AgentID
	}
	if fresh.From != "" {
		out.From = fresh.From
	}
	return out
}

// traceTrimLocked keeps the whole-room trace total under TraceCapTotal
// by dropping the head of the fattest member buffers (the oldest lines
// go first — a buffer exists to be scrolled, not hoarded).
func (h *Hub) traceTrimLocked() {
	total := 0
	for _, ring := range h.trace {
		total += len(ring)
	}
	for total > TraceCapTotal {
		fat := ""
		fatLen := 0
		for name, ring := range h.trace {
			if len(ring) > fatLen {
				fat, fatLen = name, len(ring)
			}
		}
		if fat == "" || fatLen == 0 {
			return
		}
		drop := fatLen / 4
		if drop < 1 {
			drop = 1
		}
		ring := h.trace[fat]
		ring = ring[drop:]
		if len(ring) == 0 {
			delete(h.trace, fat)
		} else {
			h.trace[fat] = ring
		}
		total -= drop
	}
}

// TraceSnapshot returns one member's trace ring (oldest first, copied)
// — GET /p/{key}/trace's per-member payload. name "" returns every
// member's ring keyed by name.
func (h *Hub) TraceSnapshot(name string) map[string][]TraceEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string][]TraceEntry{}
	for who, ring := range h.trace {
		if name != "" && who != name {
			continue
		}
		cp := make([]TraceEntry, len(ring))
		copy(cp, ring)
		out[who] = cp
	}
	return out
}
