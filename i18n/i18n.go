// Package i18n — 后端文案的语言切换（中文/英文）。
//
// 设计与前端 web/ui/i18n.js 同一口径：以中文原文为字典键——S("req
// 参数必填") 在 zh 模式原样返回（行为零变化），en 模式查 en.go 词典、
// 缺键回落原文。带参数的走 Sf（fmt 动词模板，en 词典里是同动词的英文
// 模板，英文语序不被中文拼接顺序拖累）。迁移就是把调用点的内联中文
// 包一层函数，可逐文件推进。
//
// 语言值是进程级单一真源（atomic）：本应用单用户本地跑，全部 UI 客户
// 端都是房主自己的窗口，服务端按一个值出文案即可，不做逐连接协商。
// 真源文件 ~/.niuma/lang.json（POST /lang 落盘＋热切，GET /lang 读）；
// 前端只在手动选过语言时推送（手动窗每次启动重推一遍兼作自愈；跟随
// 系统的自动判定不写——外来英文环境页面不得捋走全工作室语言）。
//
// 适用面：写进 JSON 应答、经 hub.System/Say 广播、reply() 回给 UI 的
// 用户可见文案。日志（log.Printf）不翻——那是终端面；AI 提示词
// （agents/）也不翻——成员仍以中文思考与发言，那是另一个独立特性。
package i18n

import (
	"fmt"
	"sync/atomic"
)

// lang holds the current UI language ("zh" | "en"); zero value (unset)
// means Chinese — the app's founding language and every existing
// consumer's default.
var lang atomic.Value

// Lang returns the effective UI language: "zh" or "en" (default "zh").
func Lang() string {
	if v, ok := lang.Load().(string); ok && (v == "zh" || v == "en") {
		return v
	}
	return "zh"
}

// SetLang swaps the process-wide UI language. Invalid values are ignored
// (callers pass validated input; the set stays atomic either way). Hot:
// the next S/Sf call already answers in the new language.
func SetLang(l string) {
	if l == "zh" || l == "en" {
		lang.Store(l)
	}
}

// En reports whether messages should come out in English — for the rare
// branch that needs more than a dictionary swap (format thresholds etc.).
func En() bool { return Lang() == "en" }

// S looks one message up by its Chinese source text. Chinese mode returns
// the key verbatim (zero behavior change); English mode consults enDict
// and falls back to the Chinese text when untranslated.
func S(zh string) string {
	if En() {
		if en, ok := enDict[zh]; ok && en != "" {
			return en
		}
	}
	return zh
}

// Sf formats a parameterized message: tpl is a fmt-style Chinese template
// used verbatim in Chinese; in English the enDict entry (same verbs, new
// word order) wins, falling back to the Chinese template untranslated.
func Sf(tpl string, args ...any) string {
	use := tpl
	if En() {
		if en, ok := enDict[tpl]; ok && en != "" {
			use = en
		}
	}
	return fmt.Sprintf(use, args...)
}
