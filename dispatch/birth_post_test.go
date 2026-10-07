package dispatch

// birth_post_test.go — 出生面岗位锚（分项目根治②）：--post 把行锚进本项
// 目编制表、身份可按表反查（HR 不再手抄身份串）；adopt 的一次性迁移把
// 锚前时代的存量行（角色串漂移、空 project_role）按「精确→唯一词干」
// 补锚——小检/小笔/小墨 那一代不必重生即入锚。

import (
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// postBirthStage wires a staffed dispatcher with a Docs face carrying a
// project establishment table (twinStage's shape plus the table the
// anchor drinks from).
func postBirthStage(t *testing.T, projectKey, table string) (*Dispatcher, *staffing.Store, *agents.Store) {
	t.Helper()
	dir := t.TempDir()
	agentStore, err := agents.Open(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	staffStore, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	docs, err := kb.OpenDocs(filepath.Join(dir, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	if table != "" {
		if _, err := docs.Write(kb.EstablishmentDocKey(projectKey), "编制表", table, "房主"); err != nil {
			t.Fatalf("预置表：%v", err)
		}
	}
	d := Start(chat.NewHub(), agentStore, &stubBridge{}, Config{
		Workspace: dir, StaffStore: staffStore, Docs: docs, ProjectKey: projectKey,
	})
	t.Cleanup(d.Stop)
	return d, staffStore, agentStore
}

func postTable() string {
	return "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| writer | 写手 | 小说写手（科幻+玄幻） | 2 | 是 |  | 是 |\n" +
		"| reviewer | 质检 | 质检（去AI味与中文格式） | 1 | 否 |  | 是 |\n"
}

// TestBirthPostResolvesRoleFromTable: 不传 role 时身份按表行反查（表行即
// 单一事实源），行落锚。
func TestBirthPostResolvesRoleFromTable(t *testing.T) {
	d, staff, _ := postBirthStage(t, "book", postTable())
	if _, err := d.BirthPost("小墨", "writer", "", "", "", ""); err != nil {
		t.Fatalf("带锚出生不应失败：%v", err)
	}
	row, ok := staff.Get("book", "小墨")
	if !ok || row.PostKey != "writer" {
		t.Fatalf("行应锚 writer，得 %+v", row)
	}
	if row.ProjectRole != "小说写手（科幻+玄幻）" {
		t.Fatalf("身份应按表反查，得 %q", row.ProjectRole)
	}
}

// TestBirthPostExplicitRoleKeepsAnchor: 显式 role 保留调用方的值，锚照落
// （出生面只认锚，不替调用方改口）。
func TestBirthPostExplicitRoleKeepsAnchor(t *testing.T) {
	d, staff, _ := postBirthStage(t, "book", postTable())
	if _, err := d.BirthPost("小检", "reviewer", "质检（去AI味与中文格式）", "", "", ""); err != nil {
		t.Fatalf("带锚出生不应失败：%v", err)
	}
	row, _ := staff.Get("book", "小检")
	if row.PostKey != "reviewer" {
		t.Fatalf("行应锚 reviewer，得 %+v", row)
	}
	if row.ProjectRole != "质检（去AI味与中文格式）" {
		t.Fatalf("显式身份应保留，得 %q", row.ProjectRole)
	}
}

// TestAnchorPostsMigrationStampsLegacyRows: adopt 时存量行按身份补锚——
// 精确命中（project_role 或档案角色＝身份列）与唯一词干命中（「·主笔」
// 尾缀一代）都落锚；身份列改不出的（未知角色）保持未锚走旧对账。
func TestAnchorPostsMigrationStampsLegacyRows(t *testing.T) {
	dir := t.TempDir()
	agentStore, err := agents.Open(filepath.Join(dir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	staffStore, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	docs, err := kb.OpenDocs(filepath.Join(dir, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Write(kb.EstablishmentDocKey("book"), "编制表", postTable(), "房主"); err != nil {
		t.Fatalf("预置表：%v", err)
	}
	// 锚前时代的三行：精确（project_role 撞身份列）、词干（档案角色带
	// 尾缀）、未知（哪都对不上）。
	if _, err := staffStore.Join("book", "小笔", "小说写手（科幻+玄幻）", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := staffStore.Join("book", "小墨", "", ""); err != nil {
		t.Fatal(err)
	}
	agentStore.Upsert("小墨", "小说写手（科幻+玄幻）·主笔", "", "")
	if _, err := staffStore.Join("book", "路人", "吟游诗人", ""); err != nil {
		t.Fatal(err)
	}
	d := Start(chat.NewHub(), agentStore, &stubBridge{}, Config{
		Workspace: dir, StaffStore: staffStore, Docs: docs, ProjectKey: "book",
	})
	t.Cleanup(d.Stop)
	row, _ := staffStore.Get("book", "小笔")
	if row.PostKey != "writer" {
		t.Fatalf("精确命中应补锚 writer，得 %+v", row)
	}
	row, _ = staffStore.Get("book", "小墨")
	if row.PostKey != "writer" {
		t.Fatalf("唯一词干命中应补锚 writer（·主笔一代免重生），得 %+v", row)
	}
	row, _ = staffStore.Get("book", "路人")
	if row.PostKey != "" {
		t.Fatalf("未知身份应保持未锚（旧精确对账继续看），得 %+v", row)
	}
}

// TestJoinReinstatementKeepsPostKey: 离编→复职（Recall→Birth→Join 的账面
// 往返）不洗掉岗位锚——补员对账靠它认人。
func TestJoinReinstatementKeepsPostKey(t *testing.T) {
	d, staff, _ := postBirthStage(t, "book", postTable())
	if _, err := d.BirthPost("小墨", "writer", "", "", "", ""); err != nil {
		t.Fatalf("出生：%v", err)
	}
	if _, err := staff.Offboard("book", "小墨"); err != nil {
		t.Fatalf("离编：%v", err)
	}
	if _, err := staff.Join("book", "小墨", "", ""); err != nil { // 复职（召回路径的形状）
		t.Fatalf("复职：%v", err)
	}
	row, _ := staff.Get("book", "小墨")
	if row.PostKey != "writer" {
		t.Fatalf("复职应保留岗位锚，得 %+v", row)
	}
}
