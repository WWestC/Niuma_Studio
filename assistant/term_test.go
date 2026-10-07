package assistant

// term_test.go — r_17 终端转录钩子（Config.Term）的契约：
//   1. 一问一答成对落册：注入原文（input 行，src=房主、带首问的
//      system prompt 全文）＋ 回合终点（turn 行、回答全文），From 一律
//      「小助手」；
//   2. 预热回合不落册（通道探针不是问答）——Warmup 完成后册上无行，
//      真问才落；
//   3. 投递失败落 error 行、不落 input 行（没送出去的话不算输入）。

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
)

type termSink struct {
	mu   sync.Mutex
	rows []chat.TraceEntry
}

func (s *termSink) land(e chat.TraceEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, e)
}

func (s *termSink) snapshot() []chat.TraceEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]chat.TraceEntry(nil), s.rows...)
}

func TestAssistantTermAskAnswerPair(t *testing.T) {
	sink := &termSink{}
	cfg := testConfig()
	cfg.Term = sink.land
	a, driver, obs := newTest(t, cfg)
	driver.onSend = func(content string) {
		go func() {
			obs.OnEvent(turnCompleted("sess-assistant", "第一句完整回答"))
		}()
	}
	if err := a.Ask("这是什么工作室？"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)

	rows := sink.snapshot()
	if len(rows) != 2 {
		t.Fatalf("一问一答应落 2 行，得 %d：%+v", len(rows), rows)
	}
	in, turn := rows[0], rows[1]
	if in.From != "小助手" || in.Kind != chat.TraceInput || in.Src != "房主" {
		t.Fatalf("input 行不符：%+v", in)
	}
	// 注入原文＝会话看到的合成文：首问带 system prompt 与问题本身。
	for _, want := range []string{"使用顾问", "这是什么工作室？"} {
		if !strings.Contains(in.Text, want) {
			t.Fatalf("input 行缺 %q：%q", want, in.Text)
		}
	}
	if turn.Kind != chat.TraceTurn || turn.State != "done" ||
		turn.Text != "第一句完整回答" || turn.Stop != "success" {
		t.Fatalf("turn 行不符：%+v", turn)
	}
}

func TestAssistantTermWarmupStaysSilent(t *testing.T) {
	sink := &termSink{}
	cfg := testConfig()
	cfg.Term = sink.land
	cfg.Warmup = true
	cfg.WarmupPrompt = "只回一个词"
	a, driver, obs := newTest(t, cfg)
	driver.onSend = func(content string) {
		go func() {
			obs.OnEvent(turnCompleted("sess-assistant", "词"))
		}()
	}
	a.Warmup()
	for i := 0; i < 400; i++ {
		if warming, _, _ := a.WarmGate(); !warming {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if warming, _, _ := a.WarmGate(); warming {
		t.Fatal("预热未结束")
	}
	if rows := sink.snapshot(); len(rows) != 0 {
		t.Fatalf("预热回合不该落册：%+v", rows)
	}
	// 预热后的真问照常落册。
	if err := a.Ask("在吗？"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)
	rows := sink.snapshot()
	if len(rows) != 2 || rows[0].Kind != chat.TraceInput || rows[1].Kind != chat.TraceTurn {
		t.Fatalf("真问后应 2 行（input+turn）：%+v", rows)
	}
}

func TestAssistantTermErrorRow(t *testing.T) {
	sink := &termSink{}
	cfg := testConfig()
	cfg.Term = sink.land
	a, driver, _ := newTest(t, cfg)
	driver.failNextSend(errBoom)
	if err := a.Ask("会炸吗？"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	waitStatus(t, a, StatusIdle)

	rows := sink.snapshot()
	if len(rows) != 1 || rows[0].Kind != chat.TraceError || !strings.Contains(rows[0].Text, "炸") {
		t.Fatalf("失败应只落 1 行 error（无 input——没送出去）：%+v", rows)
	}
}

// errBoom — failNextSend 的失败语料。
var errBoom = &boomErr{}

type boomErr struct{}

func (*boomErr) Error() string { return "通道炸了" }
