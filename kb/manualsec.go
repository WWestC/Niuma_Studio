package kb

// manualsec.go — 手册分节与按需取读（manual 的便宜入口）。manual.md 是
// 一本 130KB 的契约书，而成员「查一条规矩」的最短路径曾经是整本吞进
// 会话上下文（niuma kb manual → GET /kb/manual 全文吐回，无选节面）——
// 本文件把手册按 `## ` 节界拆开，供 CLI（niuma kb manual <词> /
// --index）与 HTTP（/kb/manual?q=、?index=1）两个按需取节的面共用。
// 全文面（Manual() 与无参 /kb/manual）逐字节不动：整本仍有整本的用处
//（人读、全文检索、打印），便宜的只是「只想要那一节」的这一次。
//
// 拆分口径：`^## ` 行是节界；首个 `## ` 之前的前言（H1 标题＋导语）作
// 为第 0 节，节标题取 H1 行（无 H1 则留空——测试夹具的防御位）。节的
// Body 含标题行自身（整节自足，取出来单独读不缺头）；`###` 小节不拆
// （它们从属于 `##` 节，是节内的结构不是检索粒度）。检索是子串包含
//（ASCII 大小写折叠；中文按原文），标题命中与正文命中同一待遇——
// Body 已含标题行，一条 Contains 即全覆盖。

import (
	"fmt"
	"strings"

	"github.com/WWestC/Niuma_Studio/util"
)

// ManualSection is one `## `-bounded slice of the embedded manual.
type ManualSection struct {
	Title string // 节标题（## 行去前缀；前言节取 H1 文本）
	Body  string // 整节文本（含 ## 标题行自身，尾部换行已 trim）
}

// ManualSections splits the embedded manual on `^## ` boundaries.
func ManualSections() []ManualSection {
	return splitManualSections(manualMD)
}

// splitManualSections is ManualSections' pure core (tests feed
// fixtures). Empty/whitespace-only leading junk never becomes a
// section; a document with no `## ` at all is one whole section.
func splitManualSections(src string) []ManualSection {
	var secs []ManualSection
	var cur strings.Builder
	title := ""
	flush := func() {
		body := strings.TrimRight(cur.String(), "\n")
		if strings.TrimSpace(body) != "" || title != "" {
			secs = append(secs, ManualSection{Title: title, Body: body})
		}
		cur.Reset()
	}
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(ln, "## ") {
			flush()
			title = strings.TrimSpace(strings.TrimPrefix(ln, "## "))
		}
		cur.WriteString(ln)
		cur.WriteByte('\n')
	}
	flush()
	// 前言节的标题从 H1 行取（手册本体必有；夹具无 H1 就留空）。
	if len(secs) > 0 && secs[0].Title == "" {
		if first := strings.TrimSpace(util.FirstLine(secs[0].Body)); strings.HasPrefix(first, "# ") {
			secs[0].Title = strings.TrimSpace(strings.TrimPrefix(first, "# "))
		}
	}
	return secs
}

// QueryManual returns the sections whose text contains q (ASCII
// case-folded; Chinese verbatim), in document order, plus the total
// section count for the receipt. q == "" matches nothing — the full
// book is Manual()'s job, an empty query must not launder it through
// the slice face.
func QueryManual(q string) (hits []ManualSection, total int) {
	q = strings.TrimSpace(q)
	secs := ManualSections()
	if q == "" {
		return nil, len(secs)
	}
	needle := strings.ToLower(q)
	for _, s := range secs {
		if strings.Contains(strings.ToLower(s.Body), needle) {
			hits = append(hits, s)
		}
	}
	return hits, len(secs)
}

// ManualTOC renders the numbered table of contents (the index face —
// one line per section, the cheapest possible orientation read).
func ManualTOC() string {
	secs := ManualSections()
	var b strings.Builder
	fmt.Fprintf(&b, "手册目录（%d 节）——按节取读: niuma kb manual <词>；全文: niuma kb manual", len(secs))
	for i, s := range secs {
		fmt.Fprintf(&b, "\n%2d. %s", i+1, s.Title)
	}
	return b.String()
}
