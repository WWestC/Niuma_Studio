package dispatch

// trace.go — 工作过程的水源（v2.8）：dispatcher 在 session/event 通知
// 流的头顶上再开一个只读水龙头，把可展示的时刻（思考增量 reasoning_
// delta、回复草稿 text_delta、工具调用与结果、模型轮次、回合起止、
// 错误）折成 chat.TraceEntry，经一个 500ms 合流器写进 hub 的环形缓冲
// （chat/trace.go 的 fold 规则与帧纪律见彼处注释）。
//
// v2.9 统一口径：「干活中」标记（member_work）挂在调度侧——Send 前
// 就亮、注入超时弃单也故意保持。事件流只在模型吐出首响应后才有货，
// 两面之间天然存在空窗。故回合的输入侧（deliver/steer 的注入行）与
// 生命周期事件（超时弃单、注入失败重排队、失败终态、会话回收、长回
// 合、权限问答）一并走 emitTrace 双门落环——标记亮起的那一刻，抽屉
// 里就有本回合的注入与注记，空态只属于真正没干过活的成员。
//
// v2.10 收口（先落行、再亮灯）：状态机归一 setStatus 单门（onState
// 的两处 twin 直写并入；翻转返回值即「欠一行开场」信号），两条点火
// 路径——deliverQuoted 的武装与 onState 的外部点火——都经 turnOpen
// 在亮灯前落下回合开场行；座位重挂（seat-lease 顶座）后按调度器真相
// 重烫 working 位。「亮着 chip 的空抽屉」从此结构不可达。
//
// 子智能体归属：成员 spawn 的 Agent/Task 子会话用自己的 sessionId 发
// 事件——先查 session/list 的 parentSessionId（权威），查不到再退到启
// 发式（恰好一名成员 Running 且最新的工具卡是 Agent 族未回填）——归到
// 成员名下、标 Agent+AgentID；两头都落空按 30s 冷却跳过（小助手会话等
// 非本房流量）。归属解析是异步的：解析期间的事件先压 pendingUnknown，
// 落定后一并冲账——解析里的 session/list 绝不能在读循环 goroutine 上
// 同步打（等回包会死锁：回包走的就是这条读循环）。

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// 合流器的节拍与尺寸闸。r_17 起拣选与 lane 折叠产出的是「转录级」
// （term 帽：产物 256K、入参 64K、文本流 8M 病态护栏——chat/term.go
// 的常量），工作过程环吃的「展示级」由 displayGrade 在冲水口派生
// （24K/16K/8K——traceTextMax/traceOutMax/traceInMax 现在是展示帽）。
const (
	traceFlushEvery = 500 * time.Millisecond // 尾巴文本的最长落水延时
	traceFlushChars = 4096                   // 未冲文本到达即冲的尺寸线
	traceTextMax    = chat.TraceTextCap      // 展示级：think/draft 合并块的帽
	traceOutMax     = 16_000                 // 展示级：工具输出/错误文本的帽
	traceInMax      = 8_000                  // 展示级：工具入参 JSON 的帽
	traceRetryAfter = 30 * time.Second       // 归属解析失败的冷却
)

// agentFamily 是 spawn 子智能体的工具名（启发式归属的判据）。
func agentFamily(tool string) bool {
	return tool == "Agent" || tool == "Task"
}

// traceTap is the dispatcher-side coalescer: per-member pending lanes
// awaiting their hub flush, plus the subagent attribution tables. All
// fields live under d.mu.
type traceTap struct {
	pending map[string][]chat.TraceEntry // member → 未冲条目（fold 规则同 hub）

	children       map[string]string            // 子会话 → 成员名（归属已定）
	pendingUnknown map[string][]chat.TraceEntry // 子会话 → 等归属的条目
	resolving      map[string]bool              // 子会话 → 解析中
	skipUntil      map[string]time.Time         // 子会话 → 冷却到（解析两头的落空）
}

func newTraceTap() *traceTap {
	return &traceTap{
		pending:        map[string][]chat.TraceEntry{},
		children:       map[string]string{},
		pendingUnknown: map[string][]chat.TraceEntry{},
		resolving:      map[string]bool{},
		skipUntil:      map[string]time.Time{},
	}
}

// push lands entries on a member's pending lane with From/Agent tags
// filled, folding locally the same way the hub will (text tail-merge,
// tool in-place update) so a flush batch is small and idempotent. The
// return says whether this batch demands an IMMEDIATE flush (any
// non-text entry — a tool card opened/updated, a turn or model marker,
// an error — or a text lane over the size line); text tails otherwise
// wait for the 500ms tick.
func (t *traceTap) push(name, agentID string, ents []chat.TraceEntry) bool {
	urgent := false
entry:
	for _, e := range ents {
		e.From = name
		if agentID != "" {
			e.Agent = true
			e.AgentID = agentID
		}
		if e.Kind != chat.TraceThink && e.Kind != chat.TraceDraft {
			urgent = true
		}
		lane := t.pending[name]
		if e.Kind == chat.TraceTool && e.Call != "" {
			for i := len(lane) - 1; i >= 0; i-- {
				if lane[i].Kind == chat.TraceTool && lane[i].Call == e.Call {
					e.Seq = lane[i].Seq // 保 lane 内的替换键
					lane[i] = chat.MergeTraceEntry(lane[i], e)
					t.pending[name] = lane
					urgent = true  // 回填是看得见的结果——立刻上环
					continue entry // 已并入旧卡，不再新开条
				}
			}
		}
		// 文本尾并（同流判据与 hub.sameStream 一致，这里按已带 From/Agent 的成品比）。
		if e.Kind == chat.TraceThink || e.Kind == chat.TraceDraft {
			if n := len(lane); n > 0 && lane[n-1].Kind == e.Kind &&
				lane[n-1].From == e.From && lane[n-1].Agent == e.Agent && lane[n-1].AgentID == e.AgentID {
				lane[n-1].Text += e.Text
				if len(lane[n-1].Text) > chat.TermTextCap { // 病态护栏（转录级）
					lane[n-1].Text = clipMiddle(lane[n-1].Text, chat.TermTextCap)
				}
				t.pending[name] = lane
				continue entry
			}
		}
		lane = append(lane, e)
		t.pending[name] = lane
	}
	if !urgent {
		n := 0
		for _, e := range t.pending[name] {
			n += len(e.Text)
		}
		urgent = n >= traceFlushChars
	}
	return urgent
}

// drain takes every member's pending lane as flush batches (in lane
// order). Called under d.mu.
func (t *traceTap) drain() map[string][]chat.TraceEntry {
	out := map[string][]chat.TraceEntry{}
	for name, lane := range t.pending {
		if len(lane) > 0 {
			out[name] = lane
		}
		delete(t.pending, name)
	}
	return out
}

// clipMiddle keeps head+tail around a cap, sewing the middle with an
// ellipsis note (chat.traceTextTrim's local twin — displayGrade uses it
// to bring a transcript-grade flush down to the ring's display cap).
func clipMiddle(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	half := cap / 2
	return s[:half] + i18n.S("\n…（中段省略）…\n") + s[len(s)-half:]
}

// clipOut clips a tool output / error text with a tail note (the
// DISPLAY grade's cap — displayGrade's tool).
func clipOut(s string) string {
	if len(s) <= traceOutMax {
		return s
	}
	return s[:traceOutMax] + i18n.Sf("\n…（输出超长，截断；全文 %d 字）", len(s))
}

// trimInput keeps the tool input as its raw JSON value until it would
// balloon the wire; a monster becomes a size note (display grade).
func trimInput(v any) any {
	b, err := json.Marshal(v)
	if err != nil || len(b) <= traceInMax {
		return v
	}
	return i18n.Sf("（输入 %d 字节，超出展示帽）", len(b))
}

// displayGrade derives the 工作过程 ring's flush batch from a
// transcript-grade one: think/draft text clipped to TraceTextCap, tool
// output/input to the display caps, turn/error text likewise, and the
// lifecycle rows (input/sys/ask/answer) clipped head-tail to the output
// cap — deliver 侧的注入原文是 256K 转录帽，展示面收到 16K。A pure
// copy — the term journal keeps eating the untouched original (one
// classification, two gates; the ring's behavior is byte-identical to
// the pre-r_17 single-grade era).
func displayGrade(batch []chat.TraceEntry) []chat.TraceEntry {
	out := make([]chat.TraceEntry, len(batch))
	for i, e := range batch {
		switch e.Kind {
		case chat.TraceThink, chat.TraceDraft:
			e.Text = clipMiddle(e.Text, traceTextMax)
		case chat.TraceTool:
			e.Output = clipOut(e.Output)
			e.Input = trimInput(e.Input)
		case chat.TraceTurn, chat.TraceError:
			e.Text = clipOut(e.Text)
		case chat.TraceInput, chat.TraceSys, chat.TraceAsk, chat.TraceAnswer:
			e.Text = clipMiddle(e.Text, traceOutMax)
		}
		out[i] = e
	}
	return out
}

// emitTrace is the flush funnel every tap drain goes through (r_17):
// the ring gets the display-grade batch, the terminal journal the
// transcript-grade original — same entries, same fold, two gates. All
// four former d.hub.AppendTrace call sites route here.
func (d *Dispatcher) emitTrace(batch []chat.TraceEntry) {
	if len(batch) == 0 {
		return
	}
	d.hub.AppendTrace(displayGrade(batch))
	d.hub.AppendTerm(batch)
}

// turnOpen lands the turn's opening note the instant a working tell is
// about to light (v2.10 同步收口): a lit 「干活中」 must never front an
// empty drawer. Both ignition paths open the turn here BEFORE the
// flip — the dispatcher's own injection (投递中…, the verbatim input
// row still lands on ack per r_17's delivered-only discipline) and the
// external run (onState's non-injected flip). One funnel, two gates:
// the ring and the term journal both carry the boundary.
func (d *Dispatcher) turnOpen(m *member, note string) {
	d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys, Text: note}})
}

// srcOfText lifts the provenance out of an injected line's own headers
// (the input row's src chip): the 办公室消息/公告 markers carry the
// speaker, the birth prefixes mark 编制, a queued 背景捎带 block keeps
// whatever the inner lines say (first marker wins). "" = no marker —
// the renderer falls back to a bare 注入.
func srcOfText(text string) string {
	head := text
	if len(head) > 400 {
		head = head[:400]
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`【办公室消息｜来自 ([^】]+)】`),
		regexp.MustCompile(`【公告｜来自 ([^】]+)】`),
		regexp.MustCompile(`【公告更新｜([^】]+) 发布】`),
	} {
		if m := re.FindStringSubmatch(head); m != nil {
			return m[1]
		}
	}
	if strings.Contains(head, "【拉回】") || strings.Contains(head, "【工作树】") {
		return i18n.S("编制") // 出生/召回/工作树安置（deliver 里的非房间消息注入）
	}
	if strings.Contains(head, "【当前房间公告") {
		return i18n.S("房间公告") // 出生线随行的当前公告头（noticePrefix）
	}
	return ""
}

// termInputRow is the input row one delivered injection lands (the
// transcript's IN half): the full composed text exactly as the session
// saw it (排队说明/背景捎带/装配头 all in), src lifted from the line's
// own markers. Cap is the transcript's guard cap — a birth prompt with
// capability packs is big but not 256K big.
func termInputRow(name, text string) chat.TraceEntry {
	return chat.TraceEntry{From: name, Kind: chat.TraceInput,
		Src:  srcOfText(text),
		Text: clipMiddle(text, chat.TermOutMax)}
}

// traceEntriesOf is the pure event→entries mapping — the protocol's
// displayable slice, classified on the envelope type (probe-verified
// vocabulary: model.streaming/tool.updated/turn.started/turn.completed/
// session.updated with iteration). Everything else (titles, hooks,
// stream-recovery anchors, batch rollups, network spans) yields nil.
// r_17: the entries come out TRANSCRIPT-grade (raw text deltas, tool
// outputs at chat.TermClipOut, inputs at chat.TermTrimInput) — the
// display-grade caps the 工作过程 ring eats are derived per flush by
// displayGrade, one classification two gates.
func traceEntriesOf(ev zcode.Event) []chat.TraceEntry {
	p := ev.Payload
	switch ev.Type {
	case "model.streaming":
		switch ev.Kind {
		case "reasoning_delta":
			if d, _ := p["delta"].(string); d != "" {
				return []chat.TraceEntry{{Kind: chat.TraceThink, Text: d}}
			}
		case "text_delta":
			if d, _ := p["delta"].(string); d != "" {
				return []chat.TraceEntry{{Kind: chat.TraceDraft, Text: d}}
			}
		case "tool_input_start":
			call, _ := p["toolCallId"].(string)
			tool, _ := p["toolName"].(string)
			if call != "" {
				return []chat.TraceEntry{{Kind: chat.TraceTool, Call: call, Tool: tool, State: "call"}}
			}
		case "tool_call":
			call, _ := p["toolCallId"].(string)
			tool, _ := p["toolName"].(string)
			if call != "" {
				return []chat.TraceEntry{{Kind: chat.TraceTool, Call: call, Tool: tool,
					State: "call", Input: chat.TermTrimInput(p["input"])}}
			}
		}
		return nil
	case "tool.updated":
		call, _ := p["toolCallId"].(string)
		if call == "" {
			return nil
		}
		switch ev.Kind {
		case "started":
			return []chat.TraceEntry{{Kind: chat.TraceTool, Call: call, State: "run"}}
		case "result":
			e := chat.TraceEntry{Kind: chat.TraceTool, Call: call, State: "done"}
			if res, ok := p["result"].(map[string]any); ok {
				if c, _ := res["content"].(string); c != "" {
					e.Output = chat.TermClipOut(c)
				}
				if okv, ok := res["success"].(bool); ok {
					v := okv
					e.OK = &v
				}
			}
			if ms, ok := numFloat(p["duration"]); ok {
				e.Ms = int64(ms)
			}
			return []chat.TraceEntry{e}
		case "error":
			e := chat.TraceEntry{Kind: chat.TraceTool, Call: call, State: "error"}
			for _, k := range []string{"error", "message", "errorMessage", "code"} {
				if s, _ := p[k].(string); s != "" {
					e.Output = chat.TermClipOut(s)
					break
				}
			}
			if e.Output == "" {
				e.Output = i18n.S("工具执行失败（无详情）")
			}
			return []chat.TraceEntry{e}
		}
		return nil
	case "turn.started":
		return []chat.TraceEntry{{Kind: chat.TraceTurn, State: "start"}}
	case "turn.completed":
		e := chat.TraceEntry{Kind: chat.TraceTurn, State: "done"}
		if r, ok := p["response"].(string); ok {
			e.Text = chat.TermClipOut(r)
		}
		if rt, _ := p["resultType"].(string); rt != "" {
			e.Stop = rt
		}
		if ms, ok := numFloat(p["duration"]); ok {
			e.Ms = int64(ms)
		}
		return []chat.TraceEntry{e}
	case "model.error", "turn.error":
		e := chat.TraceEntry{Kind: chat.TraceError}
		for _, k := range []string{"message", "error", "reason"} {
			if s, _ := p[k].(string); s != "" {
				e.Text = chat.TermClipOut(s)
				break
			}
		}
		if e.Text == "" {
			e.Text = ev.Type
		}
		return []chat.TraceEntry{e}
	case "session.updated":
		// model_request 的投影形（probe：{messageCount, providerId,
		// modelId, toolCount, iteration}）——回合内第 N 轮模型请求。
		if it, ok := numFloat(p["iteration"]); ok {
			e := chat.TraceEntry{Kind: chat.TraceModel, Iter: int(it)}
			prov, _ := p["providerId"].(string)
			model, _ := p["modelId"].(string)
			if model != "" {
				e.Model = model
				if prov != "" {
					e.Model = prov + "/" + model
				}
			}
			return []chat.TraceEntry{e}
		}
		return nil
	default:
		return nil
	}
}

// numFloat lifts a JSON number in either wire shape.
func numFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// traceEvent is the tap's head: classify, attribute, coalesce, and
// maybe flush. Runs on the bridge read-loop goroutine (and the raw
// bridge path) — cheap map work only; the one RPC (subagent归属) is
// spawned async and its answer re-enters through traceResolved.
func (d *Dispatcher) traceEvent(ev zcode.Event) {
	ents := traceEntriesOf(ev)
	if len(ents) == 0 {
		return
	}
	d.mu.Lock()
	if d.trace == nil {
		d.trace = newTraceTap()
	}
	if name := d.memberBySessionLocked(ev.SessionID); name != "" {
		urgent := d.trace.push(name, "", ents)
		var batch []chat.TraceEntry
		if urgent {
			batch = d.trace.pending[name]
			delete(d.trace.pending, name)
		}
		d.mu.Unlock()
		if batch != nil {
			d.emitTrace(batch)
		}
		return
	}
	// 未知会话：子智能体（或别家的流量——小助手会话、他房成员）。
	t := d.trace
	if owner, ok := t.children[ev.SessionID]; ok {
		urgent := t.push(owner, ev.SessionID, ents)
		var batch []chat.TraceEntry
		if urgent {
			batch = t.pending[owner]
			delete(t.pending, owner)
		}
		d.mu.Unlock()
		if batch != nil {
			d.emitTrace(batch)
		}
		return
	}
	if until, ok := t.skipUntil[ev.SessionID]; ok && time.Now().Before(until) {
		d.mu.Unlock()
		return
	}
	t.pendingUnknown[ev.SessionID] = append(t.pendingUnknown[ev.SessionID], ents...)
	if t.resolving[ev.SessionID] {
		d.mu.Unlock()
		return
	}
	t.resolving[ev.SessionID] = true
	d.mu.Unlock()
	go util.Guard("dispatch: trace resolve", func() { d.traceResolve(ev.SessionID) })
}

// traceResolve settles one unknown session's owner off the read loop:
// session/list's parentSessionId first (the authoritative map), then
// the single-running-Agent-caller heuristic; both miss → cooldown skip.
// The registry leg runs in a defer-unlocked closure: a panic mid-leg
// must release d.mu instead of wedging the whole dispatcher (the
// spawn site's Guard does the catching).
func (d *Dispatcher) traceResolve(child string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	parent := ""
	if list, err := d.bridge.ListSessions(ctx); err == nil {
		for _, s := range list {
			if s.SessionID == child && s.ParentSessionID != "" {
				parent = s.ParentSessionID
				break
			}
		}
	}
	owner := ""
	if parent != "" {
		d.mu.Lock()
		owner = d.memberBySessionLocked(parent)
		d.mu.Unlock()
	}
	if owner == "" {
		owner = d.traceAgentCaller()
	}
	var batch []chat.TraceEntry
	func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.trace == nil {
			d.trace = newTraceTap()
		}
		ents := d.trace.pendingUnknown[child]
		delete(d.trace.pendingUnknown, child)
		delete(d.trace.resolving, child)
		if owner != "" {
			d.trace.children[child] = owner
			d.trace.push(owner, child, ents)
			// 归属落定是难得的边界——整条 lane 无条件冲水（首见子智能体
			// 的第一批不该等钟）。
			batch = d.trace.pending[owner]
			delete(d.trace.pending, owner)
		} else if len(ents) > 0 {
			d.trace.skipUntil[child] = time.Now().Add(traceRetryAfter)
		}
	}()
	if batch != nil {
		d.emitTrace(batch)
	}
}

// traceAgentCaller is the heuristic fallback: EXACTLY ONE member is
// Running and their latest Agent-family tool card has not resolved —
// the spawned child is almost surely theirs (the art-designer shape:
// parent parks while the subagent works). Ambiguity (zero or several
// candidates) refuses to guess. Lock order: member names collected
// under d.mu, rings read OUTSIDE it (never hub.mu under d.mu).
func (d *Dispatcher) traceAgentCaller() string {
	d.mu.Lock()
	running := make([]string, 0, 2)
	for _, m := range d.members {
		if m.status == StatusRunning {
			running = append(running, m.name)
		}
	}
	// pending lane 的快照也在 d.mu 内取：lastAgentCall 在锁外读（hub 环
	// 决不能在 d.mu 下读），而 traceEvent 同拍还在 push——裸读活 map 是
	// concurrent map read/write，直接 fatal。条目全是值字段，拷贝即脱钩。
	lanes := map[string][]chat.TraceEntry{}
	if d.trace != nil {
		for _, name := range running {
			lanes[name] = append([]chat.TraceEntry(nil), d.trace.pending[name]...)
		}
	}
	d.mu.Unlock()
	if len(running) == 0 {
		return ""
	}
	candidate, count := "", 0
	for _, name := range running {
		if ts, live := lastAgentCall(name, lanes[name], d.hub); live {
			count++
			candidate = name
			_ = ts
		}
	}
	if count != 1 {
		return ""
	}
	return candidate
}

// lastAgentCall reports the member's latest Agent-family tool card:
// the TS and whether it is still unresolved (call|run). The pending
// lane arrives as a d.mu-time snapshot (never the live map — the tap
// keeps mutating under d.mu); the hub ring is still read outside d.mu
// by design (never hub.mu under it).
func lastAgentCall(name string, lane []chat.TraceEntry, hub *chat.Hub) (int64, bool) {
	for i := len(lane) - 1; i >= 0; i-- {
		e := lane[i]
		if e.Kind == chat.TraceTool && agentFamily(e.Tool) {
			return e.TS, e.State == "call" || e.State == "run"
		}
	}
	ring := hub.TraceSnapshot(name)[name]
	for i := len(ring) - 1; i >= 0; i-- {
		e := ring[i]
		if e.Kind == chat.TraceTool && agentFamily(e.Tool) {
			return e.TS, e.State == "call" || e.State == "run"
		}
	}
	return 0, false
}

// traceFlushTick is the 500ms heartbeat: text tails that never tripped
// the urgent line land on the ring here.
func (d *Dispatcher) traceFlushTick() {
	d.mu.Lock()
	if d.trace == nil {
		d.mu.Unlock()
		return
	}
	batches := d.trace.drain()
	d.mu.Unlock()
	for _, batch := range batches {
		d.emitTrace(batch)
	}
}

// termAskNote lands one ask row (the transcript's reverse-request half):
// the prompt verbatim, tool name along for the permission shape. 双门
// （v2.9 统一口径）：等权限的回合 working 亮着而输出流停摆——问与
// 答必须出现在工作过程里，否则抽屉就是空的。
func (d *Dispatcher) termAskNote(name, prompt string) {
	text := strings.TrimSpace(prompt)
	if text == "" {
		text = i18n.S("（见问题卡）")
	}
	d.emitTrace([]chat.TraceEntry{{From: name, Kind: chat.TraceAsk,
		Text: clipMiddle(text, 4096)}})
}

// termAnswer lands one answer row — the verdict that closed the ask
// (owner click, policy, autopilot or window timeout).
func (d *Dispatcher) termAnswer(name, text string, ok bool) {
	d.emitTrace([]chat.TraceEntry{{From: name, Kind: chat.TraceAnswer,
		OK: &ok, Text: clipMiddle(text, 4096)}})
}

// askAnswerDigest compacts an owner's answers into one digestible line
// ("问→答" pairs joined; the full card text lives in the chat history).
func askAnswerDigest(answers map[string]string) string {
	if len(answers) == 0 {
		return i18n.S("（空）")
	}
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" → "+answers[k])
	}
	return strings.Join(parts, "；")
}

// memberBySessionLocked maps a session id to this dispatcher's member
// name ("" = not ours). d.mu held.
func (d *Dispatcher) memberBySessionLocked(sessionID string) string {
	for _, m := range d.members {
		if m.sessionID == sessionID {
			return m.name
		}
	}
	return ""
}
