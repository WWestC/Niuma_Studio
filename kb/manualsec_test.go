package kb

// manualsec_test.go — 手册拆节与检索的纯函数契约：夹具钉住拆分口径
//（## 节界、前言取 H1、Body 含标题行、空前言不成节、无 ## 的整篇一
// 节），检索钉住子串包含＋ASCII 折叠＋空词不匹配；再对真实 manual.md
// 做最小健全性（节数、知名节可命中、目录含全部标题、全文可由节拼回）。

import (
	"strings"
	"testing"
)

const splitFixture = `# 测试手册（v9）

> 导语：这是前言。

## 甲节（Alpha）

甲的内容一。
甲的内容二。

### 甲的小节

小节属于甲，不单独成节。

## 乙节（Beta）

乙的内容。

## 丙节

丙的内容。`

func TestSplitManualSectionsFixture(t *testing.T) {
	secs := splitManualSections(splitFixture)
	if len(secs) != 4 {
		t.Fatalf("节数 = %d, want 4（前言＋三节）: %+v", len(secs), secs)
	}
	if secs[0].Title != "测试手册（v9）" {
		t.Fatalf("前言节标题应取 H1: %q", secs[0].Title)
	}
	if !strings.Contains(secs[0].Body, "导语：这是前言") {
		t.Fatalf("前言节丢了导语: %q", secs[0].Body)
	}
	if secs[1].Title != "甲节（Alpha）" {
		t.Fatalf("甲节标题: %q", secs[1].Title)
	}
	if !strings.HasPrefix(secs[1].Body, "## 甲节（Alpha）") {
		t.Fatalf("Body 应含标题行自身（取出来单独读不缺头）: %q", secs[1].Body)
	}
	if !strings.Contains(secs[1].Body, "### 甲的小节") || strings.Contains(secs[2].Body, "甲的小节") {
		t.Fatal("### 小节应留在所属 ## 节内，不单独成节")
	}
	if !strings.HasSuffix(secs[3].Body, "丙的内容。") {
		t.Fatalf("末节收尾应无悬挂换行: %q", secs[3].Body)
	}
}

func TestSplitManualSectionsEdges(t *testing.T) {
	// 无 ## 的整篇：一节，标题取 H1
	one := splitManualSections("# 只有前言\n正文一行")
	if len(one) != 1 || one[0].Title != "只有前言" || !strings.Contains(one[0].Body, "正文一行") {
		t.Fatalf("无 ## 整篇应成一节: %+v", one)
	}
	// 空白不成节
	blank := splitManualSections("\n\n## 甲\n\n内容\n\n")
	if len(blank) != 1 || blank[0].Title != "甲" {
		t.Fatalf("空白段不该成节: %+v", blank)
	}
}

func TestQueryManualOnFixture(t *testing.T) {
	prev := manualMD
	manualMD = splitFixture
	t.Cleanup(func() { manualMD = prev })

	// 标题词命中该节（正文含标题行，一条 Contains 全覆盖）
	hits, total := QueryManual("乙节")
	if total != 4 || len(hits) != 1 || hits[0].Title != "乙节（Beta）" {
		t.Fatalf("标题词命中: total=%d hits=%+v", total, hits)
	}
	// 正文词命中
	hits, _ = QueryManual("内容二")
	if len(hits) != 1 || hits[0].Title != "甲节（Alpha）" {
		t.Fatalf("正文词命中: %+v", hits)
	}
	// ASCII 折叠：beta 命中 Beta
	hits, _ = QueryManual("beta")
	if len(hits) != 1 || hits[0].Title != "乙节（Beta）" {
		t.Fatalf("ASCII 折叠: %+v", hits)
	}
	// 多节命中保文档序
	hits, _ = QueryManual("的内容")
	if len(hits) != 3 || hits[0].Title != "甲节（Alpha）" || hits[2].Title != "丙节" {
		t.Fatalf("多节命中应保文档序: %+v", hits)
	}
	// 未命中：空＋总数仍在
	if hits, total = QueryManual("不存在"); len(hits) != 0 || total != 4 {
		t.Fatalf("未命中应空: hits=%v total=%d", hits, total)
	}
	// 空词不匹配（全文是 Manual() 的事，检索面不得被空词洗白）
	if hits, _ = QueryManual("  "); hits != nil {
		t.Fatalf("空词不该命中: %+v", hits)
	}
}

// 真实手册的最小健全性：节数量级、知名节可命中、目录含全部标题、
// 节拼回应等于全文（拆分不丢字——便宜入口不能变成失真入口）。
func TestManualSectionsLiveSanity(t *testing.T) {
	secs := ManualSections()
	if len(secs) < 20 {
		t.Fatalf("手册应至少 20 节（前言＋19 个 ## 节）: %d", len(secs))
	}
	hits, _ := QueryManual("访客通道")
	if len(hits) == 0 || !strings.Contains(hits[0].Title, "访客通道") {
		t.Fatal("知名节「访客通道」应可按标题命中")
	}
	toc := ManualTOC()
	for _, s := range secs {
		if s.Title != "" && !strings.Contains(toc, s.Title) {
			t.Fatalf("目录缺节标题: %q", s.Title)
		}
	}
	// 无损校验（节尾空行会随 TrimRight 吃掉，逐字节拼回不成立）：
	// ① 每节去尾换行后必是原文的连续子串（零发明）；② 非空行数守恒
	//（每一行都属于且只属于一节——便宜入口不能丢行）。
	joined := ""
	for _, s := range secs {
		if s.Body != "" && !strings.Contains(manualMD, s.Body) {
			t.Fatalf("节内容不是原文子串（拆分发明了内容）: %q…", s.Body[:min(len(s.Body), 40)])
		}
		joined += s.Body + "\n"
	}
	if n, want := countNonEmptyLines(joined), countNonEmptyLines(manualMD); n != want {
		t.Fatalf("非空行数不守恒: 拼回 %d ≠ 原文 %d", n, want)
	}
}

// countNonEmptyLines counts the trimmed-non-empty lines.
func countNonEmptyLines(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
