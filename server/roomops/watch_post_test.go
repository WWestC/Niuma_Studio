package roomops

// watch_post_test.go — 岗位锚对账（分项目根治①）：看护按 staffing 行的
// PostKey 对编制，身份串降级为显示文本——book 实录的假缺岗（写手/质检
// 全员在座、角色串带「·主笔」尾缀或为空、报警恒 编制 N/在岗 0）不再发
// 生；未锚的存量行走旧身份串精确匹配；真缺岗的报警带「在房未计入」名
// 单（失配证据不再是报警里的空 （[]））；seatGone 自动离编的旧人重新可
// 被自动补员召回（离编不再埋掉召回路），一期一人一时一房仍然站着——
// 在别项目上班的人跳过、不硬抢。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// postStage is the anchored-reconcile harness: a lobby hub with seated
// members, a seeded establishment table, and a staffing store the
// keeper drinks anchors from. reconcile runs directly (the round's
// lobby leg) — its SystemRecorded alarms land in the hub history the
// assertions read.
func postStage(t *testing.T, table string) (*chat.Hub, *Keepers, *staffing.Store) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	if _, err := docs.Write("ops/establishment", "编制表", table, "房主"); err != nil {
		t.Fatalf("预置表：%v", err)
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatalf("staffing.Open: %v", err)
	}
	k := &Keepers{
		cfg:       KeeperConfig{LobbyHub: lobby, Docs: docs, Staff: staff},
		firstSeen: map[string]time.Time{},
		lastAct:   map[string]time.Time{},
	}
	return lobby, k, staff
}

// historyOf joins the hub's recorded system lines for substring checks.
func historyOf(h *chat.Hub) string {
	var b strings.Builder
	for _, m := range h.History() {
		b.WriteString(m.Text + "\n")
	}
	return b.String()
}

func anchoredTable() string {
	return "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| writer | 写手 | 小说写手（科幻+玄幻） | 2 | 否 |  | 是 |\n" +
		"| reviewer | 质检 | 质检（去AI味与中文格式） | 1 | 否 |  | 是 |\n"
}

// TestKeeperAnchoredCountKillsFalseAlarm: 两位写手在座、行已锚 writer——
// 席位角色串漂移（「·主笔」尾缀）不再读作缺岗；质检岗空且无锚行仍照旧
// 报警，但报警带「在房未计入」点名未锚的质检人（角色为空）。
func TestKeeperAnchoredCountKillsFalseAlarm(t *testing.T) {
	lobby, k, staff := postStage(t, anchoredTable())
	lobby.Join("小笔", "小说写手（科幻+玄幻）", false)
	lobby.Join("小墨", "小说写手（科幻+玄幻）·主笔", false) // 漂移的身份串
	lobby.Join("小检", "", false)               // 空角色（真实数据里的形状）
	for _, p := range []string{"小笔", "小墨", "小检"} {
		if _, err := staff.Join(chat.LobbyKey, p, "", ""); err != nil {
			t.Fatalf("Join %s: %v", p, err)
		}
	}
	for _, p := range []string{"小笔", "小墨"} {
		if _, err := staff.SetPost(chat.LobbyKey, p, "writer"); err != nil {
			t.Fatalf("SetPost %s: %v", p, err)
		}
	}
	old := time.Now().Add(-time.Hour)
	for _, key := range []string{"writer", "reviewer"} {
		k.firstSeen[chat.LobbyKey+"\x00"+key] = old
	}
	k.reconcile(chat.LobbyKey, lobby, time.Now())
	joined := historyOf(lobby)
	if strings.Contains(joined, "写手") {
		t.Fatalf("锚定写手满编（角色串漂移不该读作缺岗）：%s", joined)
	}
	if !strings.Contains(joined, "质检") {
		t.Fatalf("质检空岗应照常报警：%s", joined)
	}
	if !strings.Contains(joined, "在房未计入") || !strings.Contains(joined, "小检") {
		t.Fatalf("报警应带「在房未计入」点名未锚的质检人：%s", joined)
	}
}

// TestKeeperLegacyRowsKeepExactRoleMatch: 未锚的行照旧按身份串精确匹配
// （在座角色与身份列一字不差＝在岗；分毫之差＝缺岗——升级前语义不变）。
func TestKeeperLegacyRowsKeepExactRoleMatch(t *testing.T) {
	lobby, k, staff := postStage(t, anchoredTable())
	lobby.Join("小笔", "小说写手（科幻+玄幻）", false) // 精确一致
	lobby.Join("小墨", "小说写手（科幻+玄幻）·主笔", false)
	for _, p := range []string{"小笔", "小墨"} {
		if _, err := staff.Join(chat.LobbyKey, p, "", ""); err != nil {
			t.Fatalf("Join %s: %v", p, err)
		}
	}
	k.firstSeen[chat.LobbyKey+"\x00writer"] = time.Now().Add(-time.Hour)
	k.reconcile(chat.LobbyKey, lobby, time.Now())
	joined := historyOf(lobby)
	// 无锚行：精确匹配只数到小笔 → 写手 1/2，报警（缺一人），且小墨
	// 出现在「在房未计入」（他占着行、坐在房里、没被任何行计入）。
	if !strings.Contains(joined, "写手") {
		t.Fatalf("未锚行应按身份串精确匹配计 1/2 并报警：%s", joined)
	}
	if !strings.Contains(joined, "小墨") {
		t.Fatalf("漂移角色串的在座者应进「在房未计入」：%s", joined)
	}
}

// TestKeeperRefillsOffboardedAnchorHolder: seatGone 自动离编的旧人是
// 本项目的岗位旧部——自动补员应召回 TA（行还在、锚还在、绑定还在），
// 报警落「已自动补员召回」而不是裸报警循环。
func TestKeeperRefillsOffboardedAnchorHolder(t *testing.T) {
	table := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| writer | 写手 | 写手 | 1 | 是 |  | 否 |\n"
	lobby, k, staff := postStage(t, table)
	if _, err := staff.Join(chat.LobbyKey, "小笔", "写手", ""); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if _, err := staff.SetPost(chat.LobbyKey, "小笔", "writer"); err != nil {
		t.Fatalf("SetPost: %v", err)
	}
	if _, err := staff.Offboard(chat.LobbyKey, "小笔"); err != nil { // seatGone 的账面形状
		t.Fatalf("Offboard: %v", err)
	}
	recalled := ""
	k.cfg.Recall = func(project, name string) bool {
		recalled = name
		return true
	}
	k.firstSeen[chat.LobbyKey+"\x00writer"] = time.Now().Add(-time.Hour)
	k.reconcile(chat.LobbyKey, lobby, time.Now())
	if recalled != "小笔" {
		t.Fatalf("离编旧部应被自动补员召回，得 %q；历史：%s", recalled, historyOf(lobby))
	}
	if !strings.Contains(historyOf(lobby), "已自动补员召回 小笔") {
		t.Fatalf("报警应落「已自动补员召回」：%s", historyOf(lobby))
	}
}

// TestKeeperSkipsAnchorHolderSeatedElsewhere: 同一位旧部若已在他项目
// 上班（占用他房行），本项目不抢——一期一人一时一房在补员腿上仍站着，
// 裸报警照发。
func TestKeeperSkipsAnchorHolderSeatedElsewhere(t *testing.T) {
	table := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| writer | 写手 | 写手 | 1 | 是 |  | 否 |\n"
	lobby, k, staff := postStage(t, table)
	if _, err := staff.Join(chat.LobbyKey, "小笔", "写手", ""); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if _, err := staff.SetPost(chat.LobbyKey, "小笔", "writer"); err != nil {
		t.Fatalf("SetPost: %v", err)
	}
	if _, err := staff.Offboard(chat.LobbyKey, "小笔"); err != nil {
		t.Fatalf("Offboard: %v", err)
	}
	if _, err := staff.Join("other", "小笔", "写手", ""); err != nil { // 他项目在编
		t.Fatalf("Join elsewhere: %v", err)
	}
	recalled := ""
	k.cfg.Recall = func(project, name string) bool {
		recalled = name
		return true
	}
	k.firstSeen[chat.LobbyKey+"\x00writer"] = time.Now().Add(-time.Hour)
	k.reconcile(chat.LobbyKey, lobby, time.Now())
	if recalled != "" {
		t.Fatalf("他项目在编者不该被抢召回：%q", recalled)
	}
	joined := historyOf(lobby)
	if !strings.Contains(joined, "写手") || strings.Contains(joined, "已自动补员召回") {
		t.Fatalf("应落裸报警（无候选可召回）：%s", joined)
	}
}
