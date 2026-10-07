package chat

import (
	"strings"
	"testing"
)

// The kb write broadcasts' line diff (黑板红绿对比): the chat card
// paints '-' red / '+' green, so the rows must read like a git hunk —
// deletions and additions interleaved in document order, untouched
// head/tail lines silent, and the whole thing capped so a 64KB rewrite
// can't flood a bubble.

func diffText(ops []DiffLine) string {
	var b strings.Builder
	for _, op := range ops {
		b.WriteString(op.Kind)
		b.WriteString(op.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestLineDiffNewDocAllAdds(t *testing.T) {
	ops, more := LineDiff("", "# 手册\n正文一行")
	if more != 0 {
		t.Fatalf("新文档不应有省略行，got more=%d", more)
	}
	if got, want := diffText(ops), "+# 手册\n+正文一行\n"; got != want {
		t.Fatalf("新文档应全为增行，got %q want %q", got, want)
	}
}

func TestLineDiffAppendOnlyTail(t *testing.T) {
	ops, more := LineDiff("# 手册\n旧段", "# 手册\n旧段\n2026-10-02 追加的一行")
	if more != 0 || len(ops) != 1 || ops[0].Kind != "+" || ops[0].Text != "2026-10-02 追加的一行" {
		t.Fatalf("追加应只有尾行一枚增行，got %+v more=%d", ops, more)
	}
}

func TestLineDiffEditInPlace(t *testing.T) {
	ops, more := LineDiff("一\n旧句\n三", "一\n新句\n三")
	if more != 0 {
		t.Fatalf("单行改写不应有省略，got more=%d", more)
	}
	if got, want := diffText(ops), "-旧句\n+新句\n"; got != want {
		t.Fatalf("单行改写应为 -旧 +新（删在增前），got %q want %q", got, want)
	}
}

func TestLineDiffInterleavesAroundKeptLines(t *testing.T) {
	// LCS keeps the untouched "锚" line: two separate edits show as two
	// hunks in document order, not one lumped del-block + add-block.
	ops, _ := LineDiff("一\n旧A\n锚\n旧B\n五", "一\n新A\n锚\n新B\n五")
	if got, want := diffText(ops), "-旧A\n+新A\n-旧B\n+新B\n"; got != want {
		t.Fatalf("应按文档序交错（锚行两侧各成一 hunk），got %q want %q", got, want)
	}
}

func TestLineDiffIdenticalAndNewlinePunctuation(t *testing.T) {
	if ops, more := LineDiff("正文", "正文"); ops != nil || more != 0 {
		t.Fatalf("同文不应有行，got %+v more=%d", ops, more)
	}
	// 尾换行是文件标点不是空行：a\n → a 不报 ±空行
	if ops, more := LineDiff("正文\n", "正文"); ops != nil || more != 0 {
		t.Fatalf("尾换行差异不应有行，got %+v more=%d", ops, more)
	}
}

func TestLineDiffRowCapFoldsIntoMore(t *testing.T) {
	var oldB, curB strings.Builder
	for i := 0; i < 300; i++ {
		oldB.WriteString("旧")
		oldB.WriteString(strings.Repeat("x", i)) // 全不同，杜绝 LCS 误配
		oldB.WriteByte('\n')
		curB.WriteString("新")
		curB.WriteString(strings.Repeat("y", i))
		curB.WriteByte('\n')
	}
	ops, more := LineDiff(oldB.String(), curB.String())
	if len(ops) != DiffMaxRows {
		t.Fatalf("行数应夹在 %d，got %d", DiffMaxRows, len(ops))
	}
	if want := 600 - DiffMaxRows; more != want {
		t.Fatalf("省略计数应恰为超出部分 %d，got %d", want, more)
	}
}

func TestLineDiffLongLineClampedRuneSafe(t *testing.T) {
	long := strings.Repeat("牛", DiffMaxRunes+50)
	ops, _ := LineDiff("旧", long)
	if len(ops) != 2 {
		t.Fatalf("应为 -旧 +长行，got %+v", ops)
	}
	got := []rune(ops[1].Text)
	if len(got) != DiffMaxRunes+1 { // 帽值 + 省略号
		t.Fatalf("长行应夹到 %d 字符＋省略号，got %d", DiffMaxRunes, len(got))
	}
	if string(got[len(got)-1]) != "…" || string(got[len(got)-2]) != "牛" {
		t.Fatalf("夹断须落在完整字符上并以省略号收尾，got %q", ops[1].Text[len(ops[1].Text)-8:])
	}
}
