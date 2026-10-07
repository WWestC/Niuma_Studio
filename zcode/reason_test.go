package zcode

import (
	"strings"
	"testing"
)

// TurnFailHint 的词汇面：认得出的族给方向，认不出的原样带回；原文
// 永远在前（诊断时仍是第一手证据）。prompt_failed 是 CLI 的伞面停止
// 原因——真因不上协议线，所以它有自己的族；但 reason 里若捎带了具体
// 真因（网络/鉴权/配额关键词），具体族优先——伞面只兜裸词。
func TestTurnFailHint(t *testing.T) {
	cases := []struct{ in, wantSub string }{
		{"prompt_failed", "模型请求没能送达或中途失败"},
		{"turn prompt_failed: provider 500", "模型请求没能送达或中途失败"},
		{"prompt_failed: connection reset by peer", "网络中断"},
		{"connection reset by peer", "网络中断"},
		{"Cannot connect: timeout", "网络中断"},
		{"HTTP 401 unauthorized", "鉴权失败"},
		{"429 rate limit", "配额或限流"},
		{"mysterious blob", ""},
	}
	for _, c := range cases {
		got := TurnFailHint(c.in)
		if !strings.HasPrefix(got, c.in) {
			t.Errorf("TurnFailHint(%q) = %q —— 原文必须原样保留在前", c.in, got)
		}
		if c.wantSub != "" && !strings.Contains(got, c.wantSub) {
			t.Errorf("TurnFailHint(%q) = %q，缺提示 %q", c.in, got, c.wantSub)
		}
	}
	if got := TurnFailHint("mysterious blob"); got != "mysterious blob" {
		t.Errorf("认不出的 reason 应原样带回，got %q", got)
	}
}
