package zcode

// reason.go — 模型通道失败原因的人话层。provider 侧的 raw reason
// （connection reset / 401 / 429 …）会原样出现在成员的聊天流与小助
// 手面板里，用户无从下手；这里认得出的给一句方向，认不出的原样带
// 回——原文始终保留（诊断时仍是第一手证据）。dispatch 与 assistant
// 共用本层，词汇表只有这一份。

import (
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// TurnFailHint appends a plain-language hint to a recognized provider
// failure reason（网络/鉴权/配额三族；其余原样）.
func TurnFailHint(reason string) string {
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "connection reset"), strings.Contains(r, "broken pipe"),
		strings.Contains(r, "eof"), strings.Contains(r, "timeout"),
		strings.Contains(r, "unreachable"), strings.Contains(r, "refused"):
		return reason + i18n.S("——像是网络中断，通道会自动重连；持续出现请检查网络或代理")
	case strings.Contains(r, "401"), strings.Contains(r, "403"),
		strings.Contains(r, "unauthorized"), strings.Contains(r, "invalid api key"),
		strings.Contains(r, "authentication"):
		return reason + i18n.S("——像是鉴权失败，请检查 ~/.zcode/v2/provider_config.json 里的密钥")
	case strings.Contains(r, "429"), strings.Contains(r, "quota"),
		strings.Contains(r, "rate limit"), strings.Contains(r, "insufficient"),
		strings.Contains(r, "balance"):
		return reason + i18n.S("——像是配额或限流，稍等再试，或检查 provider 额度")
	case strings.Contains(r, "prompt_failed"):
		// CLI 的伞面停止原因：provider 侧回合失败时线上只带这一个词，
		// 底层真因（连接超时/鉴权/并发限流）不出现在协议里——具体族
		// 全部排在前面的用意：reason 里若捎带了真因，先按真因说话。
		return reason + i18n.S("——模型请求没能送达或中途失败（常见：provider 端点连不上、鉴权/配额、并发限流）；连续出现请检查模型档指向的通道")
	}
	return reason
}
