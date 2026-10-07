package dispatch

// trace_test.go — 工作过程水龙头（dispatch/trace.go）的判定表：纯映射
// traceEntriesOf（探针实录的六类事件形状→条目词汇）、pending lane 的
// fold 语义（与 hub 同源）、子智能体归属的异步解析（parentSessionId
// 权威路径 + 唯一 Running·Agent 未决的启发式 + 冷却跳过）。

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// fakeTraceBridge 只答 ListSessions（归属解析用的那张表），其余全烂。
type fakeTraceBridge struct {
	baseBridge
	mu    sync.Mutex
	list  []zcode.SessionInfo
	calls int
}

func (b *fakeTraceBridge) ListSessions(ctx context.Context) ([]zcode.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return b.list, nil
}
func (b *fakeTraceBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	return "s-new", nil
}

func (b *fakeTraceBridge) ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error) {
	return &zcode.ResumeResult{}, nil
}
func (b *fakeTraceBridge) Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error) {
	return &zcode.SubscribeResult{}, nil
}
func (b *fakeTraceBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	return &zcode.SendAck{}, nil
}

func TestTraceEntriesOfMapping(t *testing.T) {
	cases := []struct {
		name string
		ev   zcode.Event
		want func([]chat.TraceEntry) bool
	}{
		{"思考增量", zcode.Event{Type: "model.streaming", Kind: "reasoning_delta",
			Payload: map[string]any{"delta": "想想"}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].Kind == chat.TraceThink && es[0].Text == "想想"
			}},
		{"正文草稿", zcode.Event{Type: "model.streaming", Kind: "text_delta",
			Payload: map[string]any{"delta": "答复"}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].Kind == chat.TraceDraft && es[0].Text == "答复"
			}},
		{"空增量不产条", zcode.Event{Type: "model.streaming", Kind: "text_delta",
			Payload: map[string]any{"delta": ""}},
			func(es []chat.TraceEntry) bool { return len(es) == 0 }},
		{"工具调用全量入参", zcode.Event{Type: "model.streaming", Kind: "tool_call",
			Payload: map[string]any{"toolCallId": "c1", "toolName": "Bash",
				"input": map[string]any{"command": "ls"}}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].Kind == chat.TraceTool && es[0].Call == "c1" &&
					es[0].Tool == "Bash" && es[0].State == "call" && es[0].Input != nil
			}},
		{"工具开始", zcode.Event{Type: "tool.updated", Kind: "started",
			Payload: map[string]any{"toolCallId": "c1", "toolName": "Bash"}},
			func(es []chat.TraceEntry) bool { return len(es) == 1 && es[0].State == "run" }},
		{"工具结果（探针实录形状）", zcode.Event{Type: "tool.updated", Kind: "result",
			Payload: map[string]any{"toolCallId": "c1", "duration": 2287.0,
				"result": map[string]any{"success": true, "content": "probe-ok"}}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].State == "done" && es[0].Output == "probe-ok" &&
					es[0].OK != nil && *es[0].OK && es[0].Ms == 2287
			}},
		{"回合起点", zcode.Event{Type: "turn.started",
			Payload: map[string]any{"turnNumber": 0.0}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].Kind == chat.TraceTurn && es[0].State == "start"
			}},
		{"回合终点（response 全文）", zcode.Event{Type: "turn.completed",
			Payload: map[string]any{"response": "完成了", "duration": 18311.0, "resultType": "success"}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].State == "done" && es[0].Text == "完成了" &&
					es[0].Stop == "success" && es[0].Ms == 18311
			}},
		{"模型请求轮次（probe 实录）", zcode.Event{Type: "session.updated",
			Payload: map[string]any{"messageCount": 9.0, "providerId": "personal", "modelId": "GLM-5.3",
				"toolCount": 11.0, "iteration": 2.0}},
			func(es []chat.TraceEntry) bool {
				return len(es) == 1 && es[0].Kind == chat.TraceModel && es[0].Model == "personal/GLM-5.3" && es[0].Iter == 2
			}},
		{"hook 事件不产条", zcode.Event{Type: "session.updated",
			Payload: map[string]any{"hookEventName": "UserPromptSubmit", "descriptor": map[string]any{}}},
			func(es []chat.TraceEntry) bool { return len(es) == 0 }},
		{"标题更新不产条", zcode.Event{Type: "session.titleUpdated",
			Payload: map[string]any{"title": "x"}},
			func(es []chat.TraceEntry) bool { return len(es) == 0 }},
		{"流恢复锚不产条", zcode.Event{Type: "streamRecovery.updated", Kind: "tool_result",
			Payload: map[string]any{"toolCallId": "c1"}},
			func(es []chat.TraceEntry) bool { return len(es) == 0 }},
	}
	for _, tc := range cases {
		if es := traceEntriesOf(tc.ev); !tc.want(es) {
			t.Errorf("%s：映射不符，得 %+v", tc.name, es)
		}
	}
}

func TestTraceTapPendingFold(t *testing.T) {
	tap := newTraceTap()
	// 思考尾巴：不紧急（等钟）。
	if tap.push("小策", "", []chat.TraceEntry{{Kind: chat.TraceThink, Text: "一"}}) {
		t.Fatalf("纯文本尾巴不该立即冲")
	}
	if tap.push("小策", "", []chat.TraceEntry{{Kind: chat.TraceThink, Text: "二"}}) {
		t.Fatalf("同流续写不该立即冲")
	}
	okv := true
	// 工具调用：开卡即紧急。
	if !tap.push("小策", "", []chat.TraceEntry{
		{Kind: chat.TraceTool, Call: "c1", Tool: "Read", State: "call"},
	}) {
		t.Fatalf("工具开卡应立即冲")
	}
	// 回填：同样紧急（结果是看得见的时刻）。
	if !tap.push("小策", "", []chat.TraceEntry{
		{Kind: chat.TraceTool, Call: "c1", State: "done", Output: "内容", OK: &okv},
	}) {
		t.Fatalf("工具回填应立即冲")
	}
	lane := tap.pending["小策"]
	if len(lane) != 2 {
		t.Fatalf("lane fold 后应 2 条（思考并一、工具卡开+回填并一），得 %d", len(lane))
	}
	if lane[1].Tool != "Read" || lane[1].State != "done" || lane[1].Output != "内容" || lane[1].OK == nil {
		t.Fatalf("工具卡 lane 内原位回填失败：%+v", lane[1])
	}
	if lane[0].Text != "一二" {
		t.Fatalf("思考尾并失败：%q", lane[0].Text)
	}
}

// traceDispatcher 搭一台只带 trace 面的调度器（不发 Start 的整套，
// 也就没有钟要停——Cleanup 直接收工）。
func traceDispatcher(t *testing.T, bridge Bridge, members map[string]*member) *Dispatcher {
	t.Helper()
	return &Dispatcher{
		hub:     chat.NewHub(),
		bridge:  bridge,
		members: members,
	}
}

func TestTraceEventMemberFlow(t *testing.T) {
	b := &fakeTraceBridge{}
	d := traceDispatcher(t, b, map[string]*member{
		"小策": {name: "小策", sessionID: "sess-a", status: StatusRunning},
	})
	d.traceEvent(zcode.Event{SessionID: "sess-a", Type: "model.streaming", Kind: "reasoning_delta",
		Payload: map[string]any{"delta": "思考中"}})
	// 文本尾巴不立即冲（等钟），但环里要看得见得等 tick——直接验证
	// pending 语义，再手动 tick 冲水。
	d.mu.Lock()
	lane := d.trace.pending["小策"]
	d.mu.Unlock()
	if len(lane) != 1 || lane[0].Text != "思考中" {
		t.Fatalf("pending 未落：%+v", lane)
	}
	d.traceFlushTick()
	ring := d.hub.TraceSnapshot("小策")["小策"]
	if len(ring) != 1 || ring[0].Kind != chat.TraceThink {
		t.Fatalf("tick 冲水失败：%+v", ring)
	}
	// 非文本条目立即冲：工具调用不进 pending 尾巴。
	d.traceEvent(zcode.Event{SessionID: "sess-a", Type: "model.streaming", Kind: "tool_call",
		Payload: map[string]any{"toolCallId": "c9", "toolName": "Bash", "input": map[string]any{"command": "pwd"}}})
	ring = d.hub.TraceSnapshot("小策")["小策"]
	if len(ring) != 2 || ring[1].Kind != chat.TraceTool || ring[1].Tool != "Bash" {
		t.Fatalf("工具卡未立即冲：%+v", ring)
	}
}

func TestTraceSubagentAttribution(t *testing.T) {
	b := &fakeTraceBridge{list: []zcode.SessionInfo{
		{SessionID: "child-1", ParentSessionID: "sess-a", SessionKind: "subagent"},
	}}
	d := traceDispatcher(t, b, map[string]*member{
		"小策": {name: "小策", sessionID: "sess-a", status: StatusRunning},
	})
	d.traceEvent(zcode.Event{SessionID: "child-1", Type: "model.streaming", Kind: "reasoning_delta",
		Payload: map[string]any{"delta": "子智能体思考"}})
	// 归属解析是异步的——等到环里出现即可（上限 2s）。
	deadline := time.Now().Add(2 * time.Second)
	for {
		ring := d.hub.TraceSnapshot("小策")["小策"]
		if len(ring) == 1 {
			if ring[0].Agent != true || ring[0].AgentID != "child-1" || ring[0].Text != "子智能体思考" {
				t.Fatalf("子智能体条目标注不对：%+v", ring[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("归属解析 2s 内未落环：%+v", ring)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 定居后再来事件：不再查表、直落成员名下（文本尾巴等钟——tick 代行）。
	b.mu.Lock()
	calls := b.calls
	b.mu.Unlock()
	d.traceEvent(zcode.Event{SessionID: "child-1", Type: "model.streaming", Kind: "text_delta",
		Payload: map[string]any{"delta": "子回复"}})
	d.traceFlushTick()
	b.mu.Lock()
	calls2 := b.calls
	b.mu.Unlock()
	if calls2 != calls {
		t.Fatalf("已定居的子会话不应再查 ListSessions：%d → %d", calls, calls2)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		ring := d.hub.TraceSnapshot("小策")["小策"]
		if len(ring) == 2 && ring[1].Agent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("定居后的子会话事件 2s 内未落环：%+v", ring)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTraceUnknownSessionSkipped(t *testing.T) {
	// 小助手会话（无父、无 Running·Agent）：冷却跳过，不落环。
	b := &fakeTraceBridge{list: []zcode.SessionInfo{
		{SessionID: "assistant-1", SessionKind: "assistant"},
	}}
	d := traceDispatcher(t, b, map[string]*member{
		"小策": {name: "小策", sessionID: "sess-a", status: StatusIdle},
	})
	d.traceEvent(zcode.Event{SessionID: "assistant-1", Type: "model.streaming", Kind: "reasoning_delta",
		Payload: map[string]any{"delta": "顾问思考"}})
	time.Sleep(150 * time.Millisecond)
	if ring := d.hub.TraceSnapshot(""); len(ring) != 0 {
		t.Fatalf("无主会话的条目不得落环：%+v", ring)
	}
	d.mu.Lock()
	until := d.trace.skipUntil["assistant-1"]
	pending := len(d.trace.pendingUnknown)
	d.mu.Unlock()
	if until.IsZero() || pending != 0 {
		t.Fatalf("应记冷却且清空 pending：until=%v pending=%d", until, pending)
	}
}

func TestTraceHeuristicSingleAgentCaller(t *testing.T) {
	// 无 parentSessionId 的子会话：唯一 Running 且最新工具卡是未决
	// Agent 调用的成员领走。
	b := &fakeTraceBridge{list: nil}
	d := traceDispatcher(t, b, map[string]*member{
		"美术": {name: "美术", sessionID: "sess-art", status: StatusRunning},
		"闲人": {name: "闲人", sessionID: "sess-x", status: StatusIdle},
	})
	d.hub.AppendTrace([]chat.TraceEntry{{From: "美术", Kind: chat.TraceTool,
		Call: "ag1", Tool: "Agent", State: "call"}})
	d.traceEvent(zcode.Event{SessionID: "child-2", Type: "model.streaming", Kind: "reasoning_delta",
		Payload: map[string]any{"delta": "看图"}})
	deadline := time.Now().Add(2 * time.Second)
	for {
		ring := d.hub.TraceSnapshot("美术")["美术"]
		if len(ring) == 2 {
			if !ring[1].Agent || ring[1].AgentID != "child-2" {
				t.Fatalf("启发式归属标注不对：%+v", ring[1])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("启发式归属 2s 内未落环：%+v", ring)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLifecycleRowsRideBothGates — v2.9 统一口径的判定表：回合的
// 输入侧与生命周期事件（input/sys/ask/answer）必须双门落账——工作
// 过程环与终端转录同批都有。「干活中」亮起的路径上（注入成功即亮、
// 超时弃单保持亮、等权限停摆），抽屉里得有本回合的注入与注记，
// 空态只属于真正没干过活的成员。
func TestLifecycleRowsRideBothGates(t *testing.T) {
	b := &fakeTraceBridge{}
	d := traceDispatcher(t, b, map[string]*member{
		"小策": {name: "小策", sessionID: "sess-a", status: StatusRunning},
	})
	snap := func() (ring, term []chat.TraceEntry) {
		ring = d.hub.TraceSnapshot("小策")["小策"]
		term = d.hub.TermSnapshot("小策", 50)["小策"]
		return
	}
	kindOf := func(rows []chat.TraceEntry) []string {
		out := make([]string, len(rows))
		for i, e := range rows {
			out[i] = e.Kind
		}
		return out
	}

	// 注入成功的 input 行：src 从行内标记提源，两门同见。
	d.emitTrace([]chat.TraceEntry{termInputRow("小策", "【办公室消息｜来自 房主】把登录页做了")})
	ring, term := snap()
	if !reflect.DeepEqual(kindOf(ring), []string{"input"}) || ring[0].Src != "房主" {
		t.Fatalf("trace 环缺 input 行：%+v", ring)
	}
	if !reflect.DeepEqual(kindOf(term), []string{"input"}) {
		t.Fatalf("term 册缺 input 行：%+v", term)
	}

	// 权限问答：等权限的回合输出流停摆，问与答是抽屉里唯一的动静。
	d.termAskNote("小策", "请求权限：Bash")
	d.termAnswer("小策", "放行（策略 allow）", true)
	ring, _ = snap()
	if !reflect.DeepEqual(kindOf(ring), []string{"input", "ask", "answer"}) {
		t.Fatalf("ask/answer 未落环：%+v", kindOf(ring))
	}
	if ok := ring[2].OK; ok == nil || !*ok {
		t.Fatalf("answer 的 OK 未带：%+v", ring[2])
	}

	// 展示帽：20K 字注入（len 按字节，汉字 3 字节＝60K）——环侧收
	// 16K 保头尾，term 侧保转录帽原文。
	d.emitTrace([]chat.TraceEntry{termInputRow("小策", strings.Repeat("长", 20_000))})
	ring, term = snap()
	if n := len(ring[3].Text); n > traceOutMax+64 {
		t.Fatalf("环侧 input 未收展示帽：%d 字节", n)
	}
	if n := len(term[3].Text); n != 60_000 {
		t.Fatalf("term 侧 input 应保原文：%d 字节", n)
	}
}
