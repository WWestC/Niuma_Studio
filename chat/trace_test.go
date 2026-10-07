package chat

// trace_test.go — 工作过程环形缓冲的家法：文本尾并（同流合并、异流分
// 条）、工具卡原位回填（保位保序号、零字段继承）、容量帽（单成员与全
// 房）、快照只读、帧广播不进历史。帧纪律（不徽标、不进聊天历史）与
// read/ack 同门，这里钉的是 fold 语义本身。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// traceHub 建一个带在座成员的 hub（帧用既有 recvUntil 家法收——观察者
// 的 send 通道在 Leave 时并不关闭，泵协程等不到 EOF）。
func traceHub(t *testing.T) *Hub {
	t.Helper()
	return NewHub()
}

// traceFrame 收一枚 trace 帧并返回其条目（超时即败）。
func traceFrame(t *testing.T, h *Hub, who string) []TraceEntry {
	t.Helper()
	c, _ := h.Join(who, "", false)
	t.Cleanup(func() { h.Leave(c) })
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceTurn, State: "start"}})
	m := recvUntil(t, c, MsgTrace, 2*time.Second)
	if m.Type != MsgTrace || len(m.Trace) == 0 {
		t.Fatalf("trace 帧形状不对：%+v", m)
	}
	return m.Trace
}

func TestTraceTextTailMerge(t *testing.T) {
	h := traceHub(t)
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceThink, Text: "先"}})
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceThink, Text: "后"}})
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceDraft, Text: "答"}})
	ring := h.TraceSnapshot("小策")["小策"]
	if len(ring) != 2 {
		t.Fatalf("同流尾并后应剩 2 条（think 合一、draft 一条），得 %d", len(ring))
	}
	if ring[0].Text != "先后" {
		t.Fatalf("think 文本应顺序合并为「先后」，得 %q", ring[0].Text)
	}
	if ring[0].Kind != TraceThink || ring[1].Kind != TraceDraft {
		t.Fatalf("kind 错位：%v %v", ring[0].Kind, ring[1].Kind)
	}
	// 不同子智能体：异流分条，绝不互并。
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceThink, Text: "x", Agent: true, AgentID: "s1"}})
	ring = h.TraceSnapshot("小策")["小策"]
	if len(ring) != 3 {
		t.Fatalf("异流分条失败：%d", len(ring))
	}
}

func TestTraceToolInPlaceUpdate(t *testing.T) {
	h := traceHub(t)
	okv := true
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceTool, Call: "c1", Tool: "Bash", State: "call", Input: map[string]any{"command": "ls"}}})
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceDraft, Text: "看看结果"}})
	h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceTool, Call: "c1", State: "done", Output: "a.txt", OK: &okv, Ms: 12}})
	ring := h.TraceSnapshot("小策")["小策"]
	if len(ring) != 2 {
		t.Fatalf("工具卡应原位回填不增条，得 %d 条", len(ring))
	}
	tool := ring[0]
	if tool.Tool != "Bash" { // result 事件不带 toolName——首建时的名字不能丢
		t.Fatalf("回填丢了工具名：%+v", tool)
	}
	if tool.State != "done" || tool.Output != "a.txt" || tool.OK == nil || *tool.OK != true || tool.Ms != 12 {
		t.Fatalf("回填字段不全：%+v", tool)
	}
	if tool.Input == nil { // 零字段继承：input 仍在
		t.Fatalf("回填写没了 input：%+v", tool)
	}
	// 位置在 draft 之前（卡留在调用落位，ZCode 式）。
	if ring[1].Kind != TraceDraft {
		t.Fatalf("draft 应排在工具卡之后：%+v", ring)
	}
}

func TestTraceCaps(t *testing.T) {
	h := traceHub(t)
	// 单成员帽：灌 2× 帽，只留尾部。
	for i := 0; i < TraceCapPerMember*2; i++ {
		h.AppendTrace([]TraceEntry{{From: "小策", Kind: TraceModel, Iter: i}})
	}
	ring := h.TraceSnapshot("小策")["小策"]
	if len(ring) != TraceCapPerMember {
		t.Fatalf("单成员帽失效：%d", len(ring))
	}
	// 全房帽：多成员各灌满，总量被压回帽内。
	for who := 0; who < 8; who++ {
		name := "m" + string(rune('a'+who))
		for i := 0; i < TraceCapPerMember; i++ {
			h.AppendTrace([]TraceEntry{{From: name, Kind: TraceModel, Iter: i}})
		}
	}
	total := 0
	for _, r := range h.TraceSnapshot("") {
		total += len(r)
	}
	if total > TraceCapTotal {
		t.Fatalf("全房帽失效：%d", total)
	}
}

func TestTraceFrameReminderOnly(t *testing.T) {
	h := traceHub(t)
	// 帧要发（在座成员收得到）……
	if ents := traceFrame(t, h, "房主"); len(ents) == 0 || ents[0].Kind != TraceTurn {
		t.Fatalf("trace 帧应带回合条目：%+v", ents)
	}
	// ……但绝不进聊天历史（read/ack 帧的纪律）。
	for _, m := range h.History() {
		if m.Type == MsgTrace {
			t.Fatalf("trace 帧不得进入聊天历史：%+v", m)
		}
	}
}

func TestTraceEntryWireShape(t *testing.T) {
	// 线形钉死：字段名是前端 wire.js typedef 的镜像，改了就是 wire break。
	b, err := json.Marshal(TraceEntry{Seq: 1, TS: 2, From: "小策", Kind: "tool",
		Call: "c", Tool: "Bash", State: "done", Output: "o", Ms: 3, Model: "m", Iter: 4, Stop: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"seq":1`, `"ts":2`, `"from":"小策"`, `"kind":"tool"`, `"call":"c"`, `"tool":"Bash"`, `"state":"done"`, `"output":"o"`, `"ms":3`, `"model":"m"`, `"iter":4`, `"stop":"stop"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("线形缺字段 %s：%s", want, b)
		}
	}
	// omitempty 面：空 agent/输入不上面。
	if strings.Contains(string(b), "agent") || strings.Contains(string(b), "input") {
		t.Fatalf("空字段应省略：%s", b)
	}
}
