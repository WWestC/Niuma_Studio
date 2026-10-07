package i18n

// i18n_test.go — 语言框架的行为面：默认中文（零值）、SetLang 热切、
// S 缺键回落、Sf 模板代换与语序、针脚一致性（archive/kick 回执被
// offboard 按前缀/包含判定——译文针脚必须与译文模板逐字咬合）。

import (
	"strings"
	"testing"
)

func TestDefaultIsChinese(t *testing.T) {
	SetLang("zh")
	if got := Lang(); got != "zh" {
		t.Fatalf("Lang() = %q, want zh", got)
	}
	if got := S("载荷缺失"); got != "载荷缺失" {
		t.Fatalf("zh 模式 S 应原样返回键：got %q", got)
	}
	if got := Sf("已将 %s 的等级调整为 %d", "小猿", 5); got != "已将 小猿 的等级调整为 5" {
		t.Fatalf("zh 模式 Sf 应按中文模板：got %q", got)
	}
}

func TestSetLangHotSwapAndValidation(t *testing.T) {
	t.Cleanup(func() { SetLang("zh") })
	SetLang("en")
	if !En() {
		t.Fatal("En() 应为真")
	}
	if got := S("载荷缺失"); got != "payload missing" {
		t.Fatalf("en 模式查词典：got %q", got)
	}
	SetLang("fr") // 非法值：忽略，不炸也不换
	if got := Lang(); got != "en" {
		t.Fatalf("非法值应被忽略：Lang() = %q", got)
	}
	SetLang("zh")
	if got := S("载荷缺失"); got != "载荷缺失" {
		t.Fatalf("切回 zh 后应原样：got %q", got)
	}
}

func TestFallbackAndSf(t *testing.T) {
	t.Cleanup(func() { SetLang("zh") })
	SetLang("en")
	if got := S("这条键没收录过"); got != "这条键没收录过" {
		t.Fatalf("缺键应回落中文原文：got %q", got)
	}
	got := Sf("已将 %s 的等级调整为 %d", "小猿", 5)
	want := "adjusted 小猿 to Lv.5"
	if got != want {
		t.Fatalf("en Sf 语序：got %q want %q", got, want)
	}
	// 缺键模板回落中文模板（未迁移调用点不破相）
	if got := Sf("未收录的%s模板", "x"); got != "未收录的x模板" {
		t.Fatalf("缺键 Sf 应回落中文模板：got %q", got)
	}
}

// 针脚一致性：offboard.go 按前缀/包含判定回执性质——译文针脚必须是
// 译文模板的逐字前缀/子串（改词典一边时另一边要连着改，这里钉住）。
func TestNeedleConsistency(t *testing.T) {
	t.Cleanup(func() { SetLang("zh") })
	SetLang("en")
	kickDenied := S("kick 被拒")
	kickTpl := Sf("kick 被拒：%s 不在办公室（在线成员与幽灵席位中均无此名）", "某牛")
	if !strings.HasPrefix(kickTpl, kickDenied) {
		t.Fatalf("kick 回执模板不以针脚开头：%q vs %q", kickTpl, kickDenied)
	}
	if !strings.Contains(kickTpl, S("不在办公室")) {
		t.Fatalf("kick 回执模板应含「不在办公室」译文：%q", kickTpl)
	}
	archDenied := S("agent_archive 被拒")
	archTpl := Sf("agent_archive 被拒：档案不存在（%s 无已保存配置；纯座位清理请用 kick）", "某牛")
	if !strings.HasPrefix(archTpl, archDenied) {
		t.Fatalf("archive 回执模板不以针脚开头：%q vs %q", archTpl, archDenied)
	}
	if !strings.Contains(archTpl, S("档案不存在")) {
		t.Fatalf("archive 回执模板应含「档案不存在」译文：%q", archTpl)
	}
}
