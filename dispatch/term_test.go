package dispatch

// term_test.go — r_17 的双闸派生与转录行词汇：
//   1. displayGrade：转录级批次派生成展示级（think 24K 保头尾、工具
//      产物 16K/入参 8K、turn/error 产物帽），且不动原件（term 册吃
//      全量——one classification, two gates 的「原件不动」半边）；
//   2. srcOfText：注入行头里的来处提源（办公室消息/公告/公告更新/
//      拉回·工作树=编制/当前房间公告），无标记为空；
//   3. askAnswerDigest：应答摘要有序紧凑；
//   4. traceEntriesOf 出转录级（原样 delta、大帽产物）。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

func TestDisplayGradeClipsAndPreservesOriginal(t *testing.T) {
	long := strings.Repeat("字", 30_000) // 90KB rune text — over both caps
	batch := []chat.TraceEntry{
		{From: "小猿", Kind: chat.TraceThink, Text: long},
		{From: "小猿", Kind: chat.TraceTool, Call: "c1", Tool: "Bash",
			Output: strings.Repeat("o", 20_000), Input: map[string]any{"k": strings.Repeat("v", 10_000)}},
		{From: "小猿", Kind: chat.TraceTurn, State: "done", Text: strings.Repeat("r", 20_000)},
		{From: "小猿", Kind: chat.TraceInput, Text: "注入原文不动帽"},
	}
	disp := displayGrade(batch)

	if len(disp[0].Text) > chat.TraceTextCap+64 || !strings.Contains(disp[0].Text, "中段省略") {
		t.Fatalf("think 应裁到展示帽并保头尾：len=%d", len(disp[0].Text))
	}
	if len(disp[1].Output) > traceOutMax+64 || !strings.Contains(disp[1].Output, "输出超长") {
		t.Fatalf("工具产物应裁到展示帽：len=%d", len(disp[1].Output))
	}
	if s, ok := disp[1].Input.(string); !ok || !strings.Contains(s, "超出展示帽") {
		t.Fatalf("工具入参应换尺寸注记：%v", disp[1].Input)
	}
	if len(disp[2].Text) > traceOutMax+64 {
		t.Fatalf("turn 全文应裁到产物帽：len=%d", len(disp[2].Text))
	}
	// 原件不动：term 册继续吃全量。
	if len(batch[0].Text) != len(long) || len(batch[1].Output) != 20_000 {
		t.Fatal("displayGrade 不得改写转录级原件")
	}
	if batch[3].Text != "注入原文不动帽" || disp[3].Text != "注入原文不动帽" {
		t.Fatal("input 行两侧同文（本就不裁到 4K 以下）")
	}
}

func TestSrcOfTextLiftsProvenance(t *testing.T) {
	cases := []struct{ text, want string }{
		{"【办公室消息｜来自 小鹿】帮我查索引", "小鹿"},
		{"【排队说明】这条消息 14:21 发出……\n\n【办公室消息｜来自 房主】跑", "房主"},
		{"【公告｜来自 房主】\n全员周会", "房主"},
		{"【公告更新｜小鹿 发布】排期表更新", "小鹿"},
		{"【拉回】小猿，你已被拉回项目……", "编制"},
		{"【工作树】你的专属工作树：/tmp/x……\n\n上岗", "编制"},
		{"【当前房间公告（房主 发布）】\n……\n\n你是新同事", "房间公告"},
		{"巡检注入：一切照旧", ""},
	}
	for _, c := range cases {
		if got := srcOfText(c.text); got != c.want {
			t.Fatalf("srcOfText(%q)=%q want %q", c.text[:min(len(c.text), 24)], got, c.want)
		}
	}
}

func TestAskAnswerDigest(t *testing.T) {
	if got := askAnswerDigest(nil); got != "（空）" {
		t.Fatalf("空应答：%q", got)
	}
	got := askAnswerDigest(map[string]string{"B问": "乙", "A问": "甲"})
	if got != "A问 → 甲；B问 → 乙" {
		t.Fatalf("应答摘要应按问序紧凑：%q", got)
	}
}

func TestTraceEntriesOfComesOutTranscriptGrade(t *testing.T) {
	ev := zcodeEventOf("model.streaming", "reasoning_delta",
		map[string]any{"delta": strings.Repeat("思", 30_000)})
	ents := traceEntriesOf(ev)
	if len(ents) != 1 || len(ents[0].Text) != 90_000 {
		t.Fatalf("转录级思考增量不应预裁：len=%d", len(ents[0].Text))
	}
	ev = zcodeEventOf("tool.updated", "result", map[string]any{
		"toolCallId": "c1", "result": map[string]any{"content": strings.Repeat("o", 300_000)}})
	ents = traceEntriesOf(ev)
	if len(ents) != 1 || len(ents[0].Output) > chat.TermOutMax+128 {
		t.Fatalf("转录级产物帽 256K：len=%d", len(ents[0].Output))
	}
}

// zcodeEventOf — 测试桩：拼一枚带 payload kind 的 Event（traceEntriesOf
// 只读 Type/Kind/Payload）。
func zcodeEventOf(typ, kind string, payload map[string]any) zcode.Event {
	return zcode.Event{Type: typ, Kind: kind, Payload: payload}
}
