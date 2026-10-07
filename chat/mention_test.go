package chat

// The @ boundary rule (v2.6.1 放宽): a mention opens anywhere except
// inside an ASCII word/handle. CJK before the '@' always worked（中文
// 没有空格分词，"帮我@小策" 不需要前导空格）; the relaxed case is '@'
// right after ASCII word text — still a mention when the NAME heads
// with CJK, because a CJK-headed name can't be the tail of an email:
// 工作室聊天里满是 bug/v2.6/t_103 这类词，紧跟其后的 @ 不该哑火。
// An ASCII-headed handle keeps the old guard ("a@b.com" stays inert
// even with a member literally named "b"), and a doubled '@' never
// opens one.

import (
	"reflect"
	"testing"
)

func TestExtractMentionBoundaries(t *testing.T) {
	names := [][]rune{[]rune("小策"), []rune("阿仁"), []rune("b"), []rune("HR")}
	cases := []struct {
		text string
		want []string
	}{
		{"帮我@小策看看", []string{"小策"}},     // CJK 前 直接 @——一直生效，钉住
		{"你好@阿仁，来一下", []string{"阿仁"}},   // CJK 逗号后
		{"@小策 看看", []string{"小策"}},      // 行首
		{"这个bug@小策 看下", []string{"小策"}}, // 英文词后（本次放宽）
		{"v2.6@小策 跟进", []string{"小策"}},  // 数字后
		{"t_103@小策 进来", []string{"小策"}}, // 下划线词后
		{"问@HR 一下", []string{"HR"}},     // CJK 后的 ASCII 名照常
		{"a@b.com", nil},                // 邮箱保持 inert……
		{"邮箱foo@bar.com 别误会", nil},      // ……即使名册里真有成员叫 b
		{"qq@HR 走", nil},                // ASCII 词后的 ASCII 名：仍是邮箱形状
		{"@@小策", nil},                   // 叠 @ 不开提及
	}
	for _, c := range cases {
		if got := ExtractMentions(c.text, names); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExtractMentions(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// 房主在对话里不打前导空格直接点名——say 的 mentions 照常点名唤醒。
func TestSayMentionsWithoutLeadingSpace(t *testing.T) {
	h := NewHub()
	h.Join("小策", "", false)
	sp, _ := h.Join("房主", "", false)

	msg, ok := h.Say(sp, "这个bug@小策 看下")
	if !ok {
		t.Fatal("say 未送达")
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "小策" {
		t.Fatalf("英文词后紧跟的 @小策 该随 mentions 解析点名: %+v", msg.Mentions)
	}
}

// 召唤词族（@所有人/@全员）共用 atMentionStart：ASCII 词后紧跟的
// 召唤词照常生效；成对引号内的字样仍只是被谈论（ARB-4 不变）。
func TestAllMentionKindBoundaries(t *testing.T) {
	if got := allMentionKind("v2.6@所有人 点名"); got != allRollCall {
		t.Errorf("v2.6@所有人 该是点名, got %v", got)
	}
	if got := allMentionKind("讨论『@全员』这个词"); got != allNone {
		t.Errorf("引号内的 @全员 不算使用, got %v", got)
	}
}

// 人群词误用检测（纯词面）：不带 @ 的「大家/各位/全部人都去…」读起来像
// 想喊全房——词面命中即真；「是否真的没唤醒」由 hub 侧按最终 mentions
// 把门（活 @所有人 展开后 mentions 非空，不会自我唠叨）。
func TestCrowdAddressMiss(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"全部人都去查看 r_01", true}, // 房主实录原句
		{"大家看看这个草案", true},
		{"各位辛苦了，再核对一遍", true},
		{"全员都去查看", true},
		{"@所有人 开会", true}, // 词面仍命中；唤醒与否归 hub 把门
		{"今天进度不错", false},
		{"这个方案稳妥", false},
	} {
		if got := crowdAddressMiss(c.text); got != c.want {
			t.Errorf("crowdAddressMiss(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
