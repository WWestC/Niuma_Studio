package kb

// The 必须 column (v2.4): the seventh trailing column is the contract's
// own — system posts (编排者/HR/小助手) carry 是 and are locked
// everywhere (UI grey, row API 403), custom posts carry 否. These pins
// cover the tolerant parse (six-column legacy rows keep reading as
// 否), the canonical render/rebuild, the one-time migration over
// existing rooms, the advisor's presence exemption, and the row
// validator the write faces drink from.

import (
	"strings"
	"testing"
)

// mustParseRows parses body or fails the test with the parse error.
func mustParseRows(t *testing.T, body string) []EstablishmentRow {
	t.Helper()
	rows, perr := EstablishmentRows(body)
	if perr != "" {
		t.Fatalf("解析失败：%s\n%s", perr, body)
	}
	return rows
}

func TestMustColumnParse(t *testing.T) {
	body := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| orchestrator | 编排者 | 排期编排 | 1 | 是 |  | 是 |\n" +
		"| be | 后端 | 后端 | 2 | 否 | roles/be | 否 |\n" +
		"| legacy | 旧岗 | 旧身份 | 1 | 否 |  |\n" // 六列旧行：必须缺席=否
	rows := mustParseRows(t, body)
	if len(rows) != 3 {
		t.Fatalf("应解析 3 行，得 %d", len(rows))
	}
	if !rows[0].Must || rows[0].Key != "orchestrator" {
		t.Fatalf("第七列「是」应入 Must：%+v", rows[0])
	}
	if rows[1].Must {
		t.Fatalf("第七列「否」应保持 false：%+v", rows[1])
	}
	if rows[2].Must {
		t.Fatalf("六列旧行必须应默认否：%+v", rows[2])
	}

	bad := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| be | 后端 | 后端 | 2 | 否 |  | 也许 |\n"
	if _, perr := EstablishmentRows(bad); perr == "" || !strings.Contains(perr, "必须列") {
		t.Fatalf("必须列非法值应解析失败，得 %q", perr)
	}
}

func TestRenderPostRowSevenColumns(t *testing.T) {
	got := renderPostRow("developer", "开发", "前端开发", 1, false, "roles/developer", true)
	want := "| developer | 开发 | 前端开发 | 1 | 否 | roles/developer | 是 |"
	if got != want {
		t.Fatalf("七列渲染漂移：\n got %q\nwant %q", got, want)
	}
	// the rendered row must parse back to the same values
	rows := mustParseRows(t, establishmentHeader()+"\n"+got+"\n")
	if len(rows) != 1 || rows[0].Must != true || rows[0].Manual != "roles/developer" {
		t.Fatalf("渲染行回读漂移：%+v", rows[0])
	}
}

func TestRebuildEstablishmentBody(t *testing.T) {
	body := "# 编制表（房主手写）\n\n" +
		"（引言行）\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
		"|---|---|---|---|---|---|\n" +
		"| be | 后端 | 后端 | 2 | 否 | roles/be |\n\n" +
		"（尾注：表格后面还有房主写的正文）\n"
	rows := mustParseRows(t, body)
	rows[0].Headcount = 3
	out, ok := RebuildEstablishmentBody(body, rows)
	if !ok {
		t.Fatal("重建应成功")
	}
	if !strings.Contains(out, "| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |") {
		t.Fatalf("表头应升为七列规范形：\n%s", out)
	}
	if !strings.Contains(out, "| be | 后端 | 后端 | 3 | 否 | roles/be | 否 |") {
		t.Fatalf("行应按新值渲染：\n%s", out)
	}
	if !strings.HasPrefix(out, "# 编制表（房主手写）\n\n（引言行）\n") {
		t.Fatalf("表头前 prose 应原样保留：\n%s", out)
	}
	if !strings.HasSuffix(out, "\n（尾注：表格后面还有房主写的正文）\n") {
		t.Fatalf("表尾 prose 应原样保留：\n%s", out)
	}
}

func TestUpgradeMustColumnMigratesLegacyTables(t *testing.T) {
	docs := openPostDocs(t) // 空表库（无种子表——seed 落在 ops/establishment）
	legacy := "# 编制表（房主手写）\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
		"|---|---|---|---|---|---|\n" +
		"| orchestrator | 编排者 | 排期编排 | 1 | 是 |  |\n" +
		"| be | 后端 | 后端 | 2 | 否 | roles/be |\n\n" +
		"（尾注）\n"
	if _, err := docs.Write("ops/establishment", "编制表", legacy, "房主"); err != nil {
		t.Fatalf("预置旧表：%v", err)
	}
	touched := UpgradeMustColumn(docs)
	if len(touched) != 1 || touched[0] != "ops/establishment" {
		t.Fatalf("应恰改写 Niuma_Studio 表，得 %v", touched)
	}
	doc, _ := docs.Get("ops/establishment", 0)
	rows := mustParseRows(t, doc.Body)
	if len(rows) != 3 {
		t.Fatalf("补列后应 3 行（旗舰＋be＋小助手），得 %d：%s", len(rows), doc.Body)
	}
	if !rows[0].Must {
		t.Fatalf("系统岗 orchestrator 补列后必须=是：%+v", rows[0])
	}
	if rows[1].Must || rows[1].Key != "be" {
		t.Fatalf("自建岗必须应保持否：%+v", rows[1])
	}
	last := rows[len(rows)-1]
	if last.Key != AdvisorPost.Key || !last.Must {
		t.Fatalf("末行应是必须小助手行：%+v", last)
	}
	if !strings.HasSuffix(doc.Body, "\n（尾注）\n") {
		t.Fatalf("迁移不得吞尾注：%s", doc.Body)
	}

	// 幂等：再跑一字不写
	if again := UpgradeMustColumn(docs); len(again) != 0 {
		t.Fatalf("已收敛的表不应再改写，得 %v", again)
	}

	// 损坏的表拒绝改动
	broken := "| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n| be |\n"
	if _, err := docs.Write("p/broken/establishment", "编制表", broken, "房主"); err != nil {
		t.Fatalf("预置坏表：%v", err)
	}
	if touched := UpgradeMustColumn(docs); len(touched) != 0 {
		t.Fatalf("坏表应拒绝补列，得 %v", touched)
	}
}

func TestReconcileRowsAdvisorExempt(t *testing.T) {
	rows := []EstablishmentRow{
		{Key: "hr", Role: "人事", Headcount: 1},
		{Key: AdvisorPost.Key, Role: "使用顾问", Headcount: 1},
	}
	ReconcileRows(rows, map[string][]string{"人事": {"小马"}})
	if !rows[0].System || !rows[0].Must || rows[0].Missing != 0 {
		t.Fatalf("在岗旗舰行应为系统岗且不缺：%+v", rows[0])
	}
	if rows[1].Missing != 0 || rows[1].Present != nil {
		t.Fatalf("小助手行应在场差分中豁免：%+v", rows[1])
	}
	if !rows[1].System {
		t.Fatalf("小助手行应盖系统岗标记：%+v", rows[1])
	}
}

// TestOrderReportRows pins the report faces' display order: 小助手
// first, the flagship system posts next in registry order (编排者,
// HR), the host's custom rows last in their own doc order (stable) —
// the doc's line order itself stays the host's authoring.
func TestOrderReportRows(t *testing.T) {
	rows := []EstablishmentRow{
		{Key: "developer", Name: "开发"},
		{Key: "orchestrator", Name: "编排者"},
		{Key: "designer", Name: "美术"},
		{Key: AdvisorPost.Key, Name: "小助手"},
		{Key: "hr", Name: "HR"},
		{Key: "backend", Name: "后端"},
	}
	OrderReportRows(rows)
	want := []string{AdvisorPost.Key, "orchestrator", "hr", "developer", "designer", "backend"}
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Key
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("展示序应为 %v，得 %v", want, got)
		}
	}
}

func TestValidatePostRow(t *testing.T) {
	ok := EstablishmentRow{Key: "developer", Name: "开发", Role: "前端开发", Headcount: 1}
	if err := ValidatePostRow(ok); err != nil {
		t.Fatalf("合法自建岗不应拒绝：%v", err)
	}
	system := EstablishmentRow{Key: "hr", Name: "HR", Role: "人事", Headcount: 1}
	if err := ValidatePostRow(system); err == nil || !strings.Contains(err.Error(), "系统岗") {
		t.Fatalf("系统岗 key 应拒绝自建，得 %v", err)
	}
	mustful := ok
	mustful.Must = true
	if err := ValidatePostRow(mustful); err == nil || !strings.Contains(err.Error(), "必须") {
		t.Fatalf("自建岗带必须应拒绝，得 %v", err)
	}
	badCount := ok
	badCount.Headcount = 0
	if err := ValidatePostRow(badCount); err == nil {
		t.Fatal("编制 0 应拒绝")
	}
	badKey := ok
	badKey.Key = "has space"
	if err := ValidatePostRow(badKey); err == nil {
		t.Fatal("岗位 key 含空格应拒绝")
	}
}
