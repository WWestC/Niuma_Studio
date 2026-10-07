package meeting

// minutes_test.go — r_25（t_208）：机械归档纯函数的钉子。压缩规则
// （定稿 §二）：开场/总结前 5 句、发言 60–100 字句界截断、主张句无
// 条件保留、<5KB 保险帽。十项模式照 t_187/t_195。

import (
	"strings"
	"testing"
)

func fakeMeeting() Meeting {
	return Meeting{
		ID:           "m_01",
		ProjectKey:   "demo",
		ReqID:        "r_25",
		ReqTitle:     "评审会纪要沉淀",
		Chair:        "小牛",
		Participants: []string{"小猿", "小狐"},
		Status:       StatusDone,
		Opening:      "第一句设定。第二句背景。第三句目标。第四句范围。第五句纪律。第六句多余的。第七句也多。",
		Conclusion:   "结论一。结论二。结论三。结论四。结论五。结论六多余的。",
		Transcript: []Line{
			{TS: 100, By: "小猿", Text: "数据面我核过，五个区都有时间戳字段可以现算。"},
			{TS: 200, By: "小狐", Text: "建议驾驶舱走 DOM 面板不进 canvas。这是我的核心主张。"},
			{TS: 300, By: "小猿", Text: "补充一点，" + strings.Repeat("很长的描述句。", 40)},
		},
		StartTS: 1790000000,
		EndTS:   1790000600,
	}
}

// 验收②结构断言：头部字段/发言纪要/结论三节齐。
func TestRenderMinutesStructure(t *testing.T) {
	md := RenderMinutes(fakeMeeting())
	for _, want := range []string{
		"# 评审纪要 · r_25 评审会纪要沉淀",
		"- 主持：小牛",
		"- 参会：小猿、小狐",
		"## 开场（主持的议题设定，压缩 3–5 句）",
		"## 发言纪要",
		"## 结论与方案",
		"## 修订",
		"修订历史见 kb history",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("纪要应含 %q:\n%s", want, md)
		}
	}
	if !strings.Contains(md, "**小猿**：") || !strings.Contains(md, "**小狐**：") {
		t.Fatal("发言纪要应逐人成段")
	}
}

// 压缩规则：开场/总结压前 5 句（第六句起丢弃）。
func TestRenderMinutesProseFiveSentences(t *testing.T) {
	md := RenderMinutes(fakeMeeting())
	if strings.Contains(md, "第六句多余的") || strings.Contains(md, "结论六多余的") {
		t.Fatal("开场/总结应压到 5 句以内")
	}
	if !strings.Contains(md, "第五句纪律") || !strings.Contains(md, "结论五") {
		t.Fatal("前 5 句应保留")
	}
}

// 压缩规则：主张句无条件保留（跳过字数帽）。
func TestRenderMinutesClaimAlwaysKept(t *testing.T) {
	// 一轮超长的发言，主张句埋在中段——字数帽早过后主张句仍应在
	long := strings.Repeat("这是纯粹的背景描述用来撑过字数帽。", 30) +
		"裁定：归档走混合模式。"
	md := RenderMinutes(Meeting{ReqID: "r_01", Chair: "牛", Opening: "开场。",
		Transcript: []Line{{By: "小马", Text: long}}})
	if !strings.Contains(md, "裁定：归档走混合模式。") {
		t.Fatal("主张句（裁定）应无条件保留——字数帽不豁免主张")
	}
}

// 压缩规则：非主张句 60–100 字句界截断（不切半句）。
func TestRenderMinutesTurnCap(t *testing.T) {
	text := strings.Repeat("每句约十字左右吧。", 20) // 200 字纯描述
	md := RenderMinutes(Meeting{ReqID: "r_01", Chair: "牛", Opening: "开场。",
		Transcript: []Line{{By: "小猿", Text: text}}})
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "**小猿**：") {
			n := len([]rune(strings.TrimPrefix(line, "**小猿**：")))
			if n > 100 {
				t.Fatalf("单轮发言应 ≤100 字（got %d）", n)
			}
			if n < 60 {
				t.Fatalf("描述句应累计到 60 起收（got %d）", n)
			}
			if !strings.HasSuffix(strings.TrimSpace(line), "。") {
				t.Fatalf("应句界截断不切半句: %q…", string([]rune(line)[:30]))
			}
			return
		}
	}
	t.Fatal("没找到发言段")
}

// <5KB 验算：构造 30 轮长会（验收③）。
func TestRenderMinutesUnder5KB(t *testing.T) {
	m := Meeting{ReqID: "r_01", ReqTitle: "长会", Chair: "牛",
		Opening:    strings.Repeat("开场背景介绍句。", 100),
		Conclusion: strings.Repeat("结论陈述句。", 100),
		StartTS:    1790000000, EndTS: 1790003600}
	for i := 0; i < 30; i++ {
		m.Transcript = append(m.Transcript, Line{By: "成员", TS: int64(i),
			Text: strings.Repeat("这一轮发言内容很充实，涵盖多个要点与细节。", 10)})
	}
	md := RenderMinutes(m)
	if len(md) > 5*1024 {
		t.Fatalf("纪要应 <5KB（got %d bytes）", len(md))
	}
}

// 每轮发言 ≤100 字的全面断言（验收②）。
func TestRenderMinutesEveryTurnBounded(t *testing.T) {
	m := Meeting{ReqID: "r_01", Chair: "牛", Opening: "开。"}
	for i := 0; i < 10; i++ {
		m.Transcript = append(m.Transcript, Line{By: "成员甲", TS: int64(i),
			Text: strings.Repeat("背景说明与细节展开。", 15)})
	}
	for _, line := range strings.Split(RenderMinutes(m), "\n") {
		if !strings.HasPrefix(line, "**") {
			continue
		}
		body := line[strings.Index(line, "：")+1:]
		if n := len([]rune(body)); n > 100 {
			t.Fatalf("发言段应 ≤100 字（got %d）: %s…", n, string([]rune(body)[:20]))
		}
	}
}

// key 规范（验收①）：ops/meetings/<需求号小写>-<序号>。
func TestMinutesKey(t *testing.T) {
	if got := MinutesKey("r_25", 1); got != "ops/meetings/r_25-1" {
		t.Fatalf("key 应 ops/meetings/r_25-1（got %s）", got)
	}
	if got := MinutesKey("R_07", 3); got != "ops/meetings/r_07-3" {
		t.Fatalf("大写需求号应小写化（got %s）", got)
	}
	if got := MinutesKey("", 1); got != "ops/meetings/free-1" {
		t.Fatalf("空需求号应 free（got %s）", got)
	}
	if got := MinutesKey("r_01", 0); got != "ops/meetings/r_01-1" {
		t.Fatalf("序号下限 1（got %s）", got)
	}
}

// 标题带「内部」前缀（小马④）。
func TestMinutesTitle(t *testing.T) {
	title := MinutesTitle(Meeting{ReqID: "r_25", ReqTitle: "沉淀"})
	if !strings.HasPrefix(title, "内部") {
		t.Fatalf("标题应带内部前缀（got %s）", title)
	}
}
