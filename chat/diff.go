package chat

import "strings"

// The kb write broadcasts' line diff (黑板红绿对比): every successful
// kb write broadcasts not just the doc's new metadata but what the write
// actually did to the body — git-style red/green rows painted by the
// chat notice card (web/chat/kbdiff.js). The card is a glance, not a
// code-review tool: rows past DiffMaxRows fold into the frame's
// diff_more count, lines past DiffMaxRunes truncate rune-safely, so a
// full 64KB rewrite can never flood a bubble.
const (
	DiffMaxRows  = 100
	DiffMaxRunes = 200
	lcsMaxSide   = 200 // per side — the LCS table is (n+1)(m+1) int16
)

// DiffLine now lives in the wire package (Message carries the
// kb write's diff rows); chat re-exports via wire.go. The LineDiff
// computation below stays.

// LineDiff turns one doc write into its change rows: deletions and
// additions interleaved in document order. Identical bodies produce no
// rows (the frame then carries no diff at all — a no-op write stays the
// plain event word it always was).
func LineDiff(old, cur string) ([]DiffLine, int) {
	a, b := splitDiffLines(old), splitDiffLines(cur)
	// trim the untouched head/tail first — the common case (append, one
	// section edited) leaves a tiny middle; the LCS table then costs
	// almost nothing and huge untouched docs never reach it at all.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var ops []DiffLine
	if len(ma) > lcsMaxSide || len(mb) > lcsMaxSide {
		// a wholesale rewrite of a big doc: pairing 200+ lines through
		// the table buys nothing the eye reads — straight swap blocks.
		for _, l := range ma {
			ops = append(ops, DiffLine{Kind: "-", Text: clampDiffLine(l)})
		}
		for _, l := range mb {
			ops = append(ops, DiffLine{Kind: "+", Text: clampDiffLine(l)})
		}
	} else {
		ops = lcsDiff(ma, mb)
	}
	if len(ops) > DiffMaxRows {
		return ops[:DiffMaxRows], len(ops) - DiffMaxRows
	}
	return ops, 0
}

// lcsDiff walks the longest common subsequence of two small line
// slices: lines on the LCS path are unchanged (no row), lines only in a
// emit '-', lines only in b emit '+', interleaved in order — a line
// edited in place shows as its '-' old right before its '+' new.
func lcsDiff(a, b []string) []DiffLine {
	n, m := len(a), len(b)
	f := make([][]int16, n+1)
	for i := range f {
		f[i] = make([]int16, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				f[i][j] = f[i+1][j+1] + 1
			} else if f[i+1][j] >= f[i][j+1] {
				f[i][j] = f[i+1][j]
			} else {
				f[i][j] = f[i][j+1]
			}
		}
	}
	var ops []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case f[i+1][j] >= f[i][j+1]:
			ops = append(ops, DiffLine{Kind: "-", Text: clampDiffLine(a[i])})
			i++
		default:
			ops = append(ops, DiffLine{Kind: "+", Text: clampDiffLine(b[j])})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, DiffLine{Kind: "-", Text: clampDiffLine(a[i])})
	}
	for ; j < m; j++ {
		ops = append(ops, DiffLine{Kind: "+", Text: clampDiffLine(b[j])})
	}
	return ops
}

// splitDiffLines cuts a body into diff lines. A single trailing newline
// is file punctuation, not an empty final line — "a\n" and "a" are the
// same one-line doc (Append's write path guarantees the newline, a hand
// edit may not; the card must not report a phantom ±空行 for that).
func splitDiffLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func clampDiffLine(l string) string {
	if r := []rune(l); len(r) > DiffMaxRunes {
		return string(r[:DiffMaxRunes]) + "…"
	}
	return l
}
