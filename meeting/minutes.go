// minutes.go — r_25（t_208）：评审会纪要的机械归档——Meeting →
// markdown 纯函数。混合归档（定稿 §〇方案 c）：本函数只做机械压缩先落
// （散会即有，零 AI 回合依赖），主持的修订后补走 kb write 的既有通道。
//
// 压缩规则（定稿 §二，机械算法钉死）：
//   - 开场/总结压 3–5 句：按句号/问号/叹号切句取前 5 句——帽 4000/2000
//     的原文不进纪要全文（「好规则不豁免规则制定者」）；
//   - 每轮发言 60–100 字：切句后累计到 60 起收、超 100 截句尾（句界
//     截断不切半句；不足 60 全取）；
//   - 主张句无条件保留：含「建议/裁定/采纳/反对/风险/量级/验收」的
//     句子跳过字数帽优先入（主张是纪要的本体，描述是包装——小马②）；
//   - 全文 <5KB 保险帽：超帽从最早发言轮起整轮降为「（已压缩）」
//     直到达标。
//
// key 规范（验收①）：ops/meetings/<需求号>-<日期序号>，同需求同日多场
// 按已存序号递增（序号由调用方查 kb 定，MinutesKey 只拼形）。
package meeting

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MinutesClaimKeywords：主张句的关键词表（定稿 §二·小马②）——命中
// 任一的句子跳过字数帽优先入纪要。
var MinutesClaimKeywords = []string{
	"建议", "裁定", "采纳", "反对", "风险", "量级", "验收",
}

// minutesBudget is the <5KB insurance cap (UTF-8 bytes, 定稿 §二).
const minutesBudget = 5 * 1024

// MinutesKey 拼纪要文档键（验收①规范）：ops/meetings/<需求号>-<日期序号>。
// 需求号小写化（kb key 只容小写；r_25 → r25），日期序号 1 起。
func MinutesKey(reqID string, daySeq int) string {
	req := strings.ToLower(strings.TrimSpace(reqID))
	if req == "" {
		req = "free"
	}
	if daySeq < 1 {
		daySeq = 1
	}
	return fmt.Sprintf("ops/meetings/%s-%d", req, daySeq)
}

// MinutesTitle 纪要文档标题（「内部」前缀——小马④：/view 不出评审纪要）。
func MinutesTitle(m Meeting) string {
	return fmt.Sprintf("内部 · 评审纪要 %s %s", m.ReqID, m.ReqTitle)
}

// splitSentences 按句末标点切句（。？！；——分号也算一句的边界，空句
// 丢弃），保留原字符。
func splitSentences(s string) []string {
	out := []string{}
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		if r == '。' || r == '？' || r == '！' || r == '；' || r == '?' || r == '!' || r == ';' {
			if t := strings.TrimSpace(b.String()); t != "" {
				out = append(out, t)
			}
			b.Reset()
		}
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		out = append(out, t) // 无句末标点的尾句也是一句
	}
	return out
}

// isClaim 命中主张关键词表。
func isClaim(sentence string) bool {
	for _, k := range MinutesClaimKeywords {
		if strings.Contains(sentence, k) {
			return true
		}
	}
	return false
}

// compressProse 压开场/总结：前 5 句（定稿 §二「3–5 句」取上沿——
// 议题设定与结论是该节的全部职责）。
func compressProse(s string) string {
	parts := splitSentences(s)
	if len(parts) > 5 {
		parts = parts[:5]
	}
	return strings.Join(parts, "")
}

// compressTurn 压一轮发言：主张句无条件保留（跳过字数帽优先入），
// 其余句子按序累计——60 字起收、超 100 截句尾（不足 60 全取）。
func compressTurn(text string) string {
	parts := splitSentences(text)
	var kept []string
	n := 0
	for _, p := range parts {
		if isClaim(p) {
			kept = append(kept, p) // 主张句无条件保留
			n += utf8.RuneCountInString(p)
			continue
		}
		if n >= 60 {
			continue // 已过起收线：非主张句不再进
		}
		kept = append(kept, p)
		n += utf8.RuneCountInString(p)
		if n > 100 {
			break // 超帽即停（句界截断，不切半句）
		}
	}
	if len(kept) == 0 && len(parts) > 0 {
		kept = parts[:1] // 兜底：至少一句（切句后非空则不至于，防御）
	}
	return strings.Join(kept, "")
}

// RenderMinutes 压 Meeting → 纪要 markdown（定稿 §一 frozen 结构）。
// 纯函数：不改 Meeting、不碰时钟——时间字段全从记录读。
func RenderMinutes(m Meeting) string {
	var b strings.Builder
	date := ""
	if m.StartTS > 0 {
		date = fmtTime(m.StartTS)
	}
	b.WriteString(fmt.Sprintf("# 评审纪要 · %s %s（%s）\n\n", m.ReqID, m.ReqTitle, date))
	b.WriteString(fmt.Sprintf("- 主持：%s\n", m.Chair))
	if len(m.Participants) > 0 {
		b.WriteString(fmt.Sprintf("- 参会：%s\n", strings.Join(m.Participants, "、")))
	}
	if m.StartTS > 0 && m.EndTS > 0 {
		mins := (m.EndTS - m.StartTS + 59) / 60
		b.WriteString(fmt.Sprintf("- 时长：%s-%s（%d 分钟）\n",
			fmtClock(m.StartTS), fmtClock(m.EndTS), mins))
	}
	b.WriteString("- 修订历史见 kb history（机械归档与主持修订的每一版可回溯）\n\n")

	b.WriteString("## 开场（主持的议题设定，压缩 3–5 句）\n")
	if o := compressProse(m.Opening); o != "" {
		b.WriteString(o + "\n\n")
	} else {
		b.WriteString("（无开场记录）\n\n")
	}

	b.WriteString("## 发言纪要\n")
	for _, ln := range m.Transcript {
		if strings.TrimSpace(ln.Text) == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("**%s**：%s\n\n", ln.By, compressTurn(ln.Text)))
	}

	b.WriteString("## 结论与方案\n")
	if c := compressProse(m.Conclusion); c != "" {
		b.WriteString(c + "\n\n")
	} else {
		b.WriteString("（无结论记录）\n\n")
	}

	b.WriteString("## 修订\n")
	b.WriteString("- 机械归档：自动（散会即落）\n")
	return trimMinutes(b.String())
}

// trimMinutes 强制 <5KB 保险帽（定稿 §二）：超帽从最早发言轮起整轮降
// 为「（已压缩）」直到达标。RenderMinutes 内部收口——调用方拿到的必然
// 达标。
func trimMinutes(md string) string {
	if len(md) <= minutesBudget {
		return md
	}
	// 发言段按段落切：**名字**：… 的成对空行块
	blocks := strings.Split(md, "\n\n")
	for i, blk := range blocks {
		if len(md) <= minutesBudget {
			break
		}
		if strings.HasPrefix(blk, "**") && strings.Contains(blk, "**：") {
			// 只降最早的发言段（头部/结论/修订结构段不动）
			md = strings.Replace(md, blk, "（已压缩）", 1)
			_ = i
		}
	}
	return md
}

// fmtTime/fmtClock：本地时区的日期与 HH:MM（纪要给人读，跟随读者时区）。
func fmtTime(ts int64) string {
	return time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
}
func fmtClock(ts int64) string {
	return time.Unix(ts, 0).Local().Format("15:04")
}
