package kb

// Flagship establishment tests (v2.2): the registry IS the seed (a
// fresh store's ops/establishment parses to exactly the flagship rows,
// sentinel gone), the shortage/filled math the boot recruiter drinks
// from, the doc-key rule every writer shares, and the one-time
// sentinel retirement over legacy tables.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
)

// legacyTable is a v0.x-era lobby table: the sentinel post still
// seated beside HR — the retirement migration's input.
const legacyTable = "# 编制表（房主维护；哨兵照此核对）\n\n" +
	"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
	"|---|---|---|---|---|---|\n" +
	"| sentinel | 哨兵 | 哨兵 | 1 | 否 | roles/sentinel |\n" +
	"| hr | HR | 人事 | 1 | 否 | roles/hr |\n\n" +
	"（尾注：房主写的正文必须原样保留）\n"

// openFreshDocs opens a brand-new store under a not-yet-existing
// subdir — seeding fires only when the directory is absent, and
// t.TempDir() itself already exists.
func openFreshDocs(t *testing.T) *DocsStore {
	t.Helper()
	docs, err := OpenDocs(filepath.Join(t.TempDir(), "kb"))
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	return docs
}

// TestSeedTableIsFlagshipRegistry pins the lockstep: a fresh store
// seeds ops/establishment with EXACTLY the flagship posts (编排者/HR,
// both auto-fill), and no sentinel row or sentinel handbook lands.
func TestSeedTableIsFlagshipRegistry(t *testing.T) {
	docs := openFreshDocs(t)
	if _, err := docs.Get("roles/sentinel", 0); err == nil {
		t.Fatal("哨兵手册不应再随种子落地（岗位已退役）")
	}
	doc, err := docs.Get("ops/establishment", 0)
	if err != nil {
		t.Fatalf("种子编制表不可读：%v", err)
	}
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" {
		t.Fatalf("种子表解析失败：%s", perr)
	}
	// v2.4：旗舰两行＋小助手行（必须系统岗——看护与启动门都不计它）
	if len(rows) != len(FlagshipPosts)+1 {
		t.Fatalf("种子表应恰有 %d 行（旗舰×%d＋小助手），得 %d", len(FlagshipPosts)+1, len(FlagshipPosts), len(rows))
	}
	for i, p := range FlagshipPosts {
		r := rows[i]
		if r.Key != p.Key || r.Name != p.Name || r.Role != p.Role ||
			r.Headcount != p.Headcount || r.AutoFill != p.AutoFill || r.Manual != p.Manual {
			t.Fatalf("第 %d 行与注册表漂移：表=%+v 注册=%+v", i, r, p)
		}
		if !r.Must {
			t.Fatalf("旗舰行应带 必须=是：%+v", r)
		}
	}
	if r := rows[len(rows)-1]; r.Key != AdvisorPost.Key || r.Name != AdvisorPost.Name || !r.Must {
		t.Fatalf("末行应是必须系统岗小助手：%+v", r)
	}
	if filled, total := FlagshipFilled(rows); filled != 0 || total != 2 {
		t.Fatalf("空房计数应 0/2，得 %d/%d", filled, total)
	}
}

// TestFlagshipPostsShape pins the two mandatory posts themselves:
// 编排者 小牛 / HR 小马, both Lv.8, both auto-fill — the app's boot
// contract in one assertion.
func TestFlagshipPostsShape(t *testing.T) {
	if len(FlagshipPosts) != 2 {
		t.Fatalf("旗舰岗位应恰两个，得 %d", len(FlagshipPosts))
	}
	orch, hr := FlagshipPosts[0], FlagshipPosts[1]
	if orch.Person != "小牛" || orch.Role != agents.OrchestratorRole || orch.Rank != 8 || !orch.AutoFill {
		t.Fatalf("编排者岗位不符：%+v", orch)
	}
	if hr.Person != "小马" || hr.Role != agents.HRRole || hr.Rank != 8 || !hr.AutoFill || hr.Manual != "roles/hr" {
		t.Fatalf("HR 岗位不符：%+v", hr)
	}
}

// TestFlagshipShortageMath walks the reconciliation math: an empty
// room misses both posts in table order, a seated 小牛 leaves only HR
// missing, and a full table (extra headcount included) misses none —
// presence beyond headcount never overfills the counter.
func TestFlagshipShortageMath(t *testing.T) {
	if missing := FlagshipShortage(nil); len(missing) != 2 {
		t.Fatalf("空表应缺两岗，得 %d", len(missing))
	}
	filled := []EstablishmentRow{
		{Key: "orchestrator", Role: agents.OrchestratorRole, Headcount: 1, Present: []string{"小牛"}},
		{Key: "hr", Role: agents.HRRole, Headcount: 1},
	}
	missing := FlagshipShortage(filled)
	if len(missing) != 1 || missing[0].Key != "hr" {
		t.Fatalf("只应缺 HR：%+v", missing)
	}
	if f, total := FlagshipFilled(filled); f != 1 || total != 2 {
		t.Fatalf("计数应 1/2，得 %d/%d", f, total)
	}
	filled[1].Present = []string{"小马", "小马二号"} // 超编的在场只按编制计
	if missing := FlagshipShortage(filled); len(missing) != 0 {
		t.Fatalf("满编不应缺岗：%+v", missing)
	}
	if f, total := FlagshipFilled(filled); f != 2 || total != 2 {
		t.Fatalf("计数应 2/2，得 %d/%d", f, total)
	}
}

// TestEstablishmentDocKey pins the one address rule: the lobby keeps
// ops/establishment, a project gets p/<key>/establishment — the lobby
// must never grow an unwatched p/default shadow table.
func TestEstablishmentDocKey(t *testing.T) {
	if got := EstablishmentDocKey(chat.LobbyKey); got != "ops/establishment" {
		t.Fatalf("Niuma_Studio 表址应 ops/establishment，得 %s", got)
	}
	if got := EstablishmentDocKey("proj-x"); got != "p/proj-x/establishment" {
		t.Fatalf("项目表址应 p/proj-x/establishment，得 %s", got)
	}
}

// TestRetireSentinelPosts walks the migration: a legacy table loses
// its sentinel row with the trailing prose untouched, the second pass
// is a no-op, a broken table is refused, and non-establishment docs
// are never in play.
func TestRetireSentinelPosts(t *testing.T) {
	docs, err := OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	if _, err := docs.Write("ops/establishment", "编制表", legacyTable, "房主"); err != nil {
		t.Fatalf("铺旧表失败：%v", err)
	}
	if _, err := docs.Write("p/proj-x/establishment", "编制表", legacyTable, "房主"); err != nil {
		t.Fatalf("铺旧项目表失败：%v", err)
	}

	touched := RetireSentinelPosts(docs)
	if len(touched) != 2 {
		t.Fatalf("应改写两张表，得 %v", touched)
	}
	for _, key := range []string{"ops/establishment", "p/proj-x/establishment"} {
		doc, err := docs.Get(key, 0)
		if err != nil {
			t.Fatalf("%s 退役后不可读：%v", key, err)
		}
		if strings.Contains(doc.Body, "sentinel") {
			t.Fatalf("%s 哨兵行未移除：%s", key, doc.Body)
		}
		rows, perr := EstablishmentRows(doc.Body)
		if perr != "" {
			t.Fatalf("%s 退役后解析失败：%s", key, perr)
		}
		if len(rows) != 1 || rows[0].Key != "hr" {
			t.Fatalf("%s 应只剩 hr 行：%+v", key, rows)
		}
		if !strings.Contains(doc.Body, "（尾注：房主写的正文必须原样保留）") {
			t.Fatalf("%s 尾注被改动：%s", key, doc.Body)
		}
	}

	if again := RetireSentinelPosts(docs); len(again) != 0 {
		t.Fatalf("二次退役应空手而归，得 %v", again)
	}

	// a broken table is refused — never edited on a guess
	broken := "# 编制表\n\n| 岗位 key | 名称 |\n|---|---|\n| sentinel | 哨兵 |\n"
	if _, err := docs.Write("p/broken/establishment", "编制表", broken, "房主"); err != nil {
		t.Fatalf("铺坏表失败：%v", err)
	}
	if touched := RetireSentinelPosts(docs); len(touched) != 0 {
		t.Fatalf("坏表不应被改写，得 %v", touched)
	}
}

// TestProjectSystemPostsRegistry pins the per-project floor (v2.5):
// every system key the keeper and the row API know is in it, the
// roles are the exact agents constants (presence matches by exact
// text — the anchor the whole reconciliation drinks from), and the
// advisor rides exactly once at the tail.
func TestProjectSystemPostsRegistry(t *testing.T) {
	posts := ProjectSystemPosts()
	if len(posts) != len(FlagshipPosts)+1 {
		t.Fatalf("项目系统岗下限应 = 旗舰×%d＋小助手，得 %d", len(FlagshipPosts), len(posts))
	}
	for i, p := range FlagshipPosts {
		if posts[i] != p {
			t.Fatalf("前 %d 位应与旗舰注册表一致：got %+v want %+v", i, posts[i], p)
		}
	}
	tail := posts[len(posts)-1]
	if tail.Key != AdvisorPost.Key || tail.AutoFill {
		t.Fatalf("末位应是唯一的小助手（自动补员=否——它无座可补）：%+v", tail)
	}
	for _, p := range posts {
		if !SystemPostKey(p.Key) {
			t.Fatalf("下限里的每个 key 都应是系统岗：%+v", p)
		}
		if p.Role != agents.OrchestratorRole && p.Role != agents.HRRole && p.Key != AdvisorPost.Key {
			t.Fatalf("系统岗身份应是注册表常量：%+v", p)
		}
	}
}

// TestEnsureProjectEstablishment pins the 编制-is-mandatory convergence:
// a missing project table is created whole with the three system posts
// (编排者/HR 分项目各一套＋全工作室唯一的小助手), an existing table
// gains only what it lacks appended after its last table line (the
// host's rows and their order are never touched), a converged table
// writes nothing, a broken table is refused on a guess, and the lobby
// is not this function's business.
func TestEnsureProjectEstablishment(t *testing.T) {
	docs := openFreshDocs(t)

	// 缺表：整张补建，三行系统岗按注册表序落地，全部 必须=是
	touched, err := EnsureProjectEstablishment(docs, "proj-a", "测试")
	if err != nil {
		t.Fatalf("缺表收敛失败：%v", err)
	}
	if len(touched) != len(ProjectSystemPosts()) {
		t.Fatalf("缺表应写齐 %d 行，得 %v", len(ProjectSystemPosts()), touched)
	}
	doc, err := docs.Get("p/proj-a/establishment", 0)
	if err != nil {
		t.Fatalf("项目表应已建：%v", err)
	}
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" {
		t.Fatalf("补建表解析失败：%s", perr)
	}
	posts := ProjectSystemPosts()
	if len(rows) != len(posts) {
		t.Fatalf("补建表应恰 %d 行，得 %v", len(posts), rows)
	}
	for i, p := range posts {
		r := rows[i]
		if r.Key != p.Key || r.Name != p.Name || r.Role != p.Role ||
			r.Headcount != p.Headcount || r.AutoFill != p.AutoFill || !r.Must {
			t.Fatalf("第 %d 行与注册表漂移：表=%+v 注册=%+v", i, r, p)
		}
	}

	// 幂等：已收敛的表一字不写（rev 不动）
	before := doc.Rev
	touched, err = EnsureProjectEstablishment(docs, "proj-a", "测试")
	if err != nil || len(touched) != 0 {
		t.Fatalf("已收敛表应零写（got touched=%v err=%v）", touched, err)
	}
	if after, _ := docs.Get("p/proj-a/establishment", 0); after.Rev != before {
		t.Fatalf("收敛表不应前进 rev：%d → %d", before, after.Rev)
	}

	// 已有自建行＋编排者行（编排者在项目出生时自登记过的形状）：只补
	// HR＋小助手，自建行与既有行序原样在前
	custom := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| be | 后端 | 后端 | 2 | 否 | roles/be | 否 |\n" +
		"| orchestrator | 编排者 | 排期编排 | 1 | 是 |  | 是 |\n"
	if _, err := docs.Write("p/proj-b/establishment", "编制表", custom, "房主"); err != nil {
		t.Fatalf("铺半张表：%v", err)
	}
	touched, err = EnsureProjectEstablishment(docs, "proj-b", "测试")
	if err != nil {
		t.Fatalf("半张表收敛失败：%v", err)
	}
	if len(touched) != 2 || touched[0] != "hr" || touched[1] != AdvisorPost.Key {
		t.Fatalf("半张表应只补 hr＋小助手，得 %v", touched)
	}
	doc, _ = docs.Get("p/proj-b/establishment", 0)
	rows = mustRows(t, doc.Body)
	want := []string{"be", "orchestrator", "hr", "assistant"}
	if len(rows) != len(want) {
		t.Fatalf("收敛后应 %d 行，得 %v", len(want), rows)
	}
	for i, k := range want {
		if rows[i].Key != k {
			t.Fatalf("行序应 %v（自建行在前、系统岗尾行追加），得第 %d 行 %s", want, i, rows[i].Key)
		}
	}
	if rows[0].Headcount != 2 || rows[0].Manual != "roles/be" || rows[0].Must {
		t.Fatalf("自建行被改动：%+v", rows[0])
	}

	// 损坏的表：拒绝（不猜），一字不写
	broken := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| be | 后端 | 后端 | 两 | 否 |  | 否 |\n"
	if _, err := docs.Write("p/proj-c/establishment", "编制表", broken, "房主"); err != nil {
		t.Fatalf("铺坏表：%v", err)
	}
	if _, err := EnsureProjectEstablishment(docs, "proj-c", "测试"); err == nil {
		t.Fatal("坏表应拒绝收敛，不得猜")
	}
	if after, _ := docs.Get("p/proj-c/establishment", 0); strings.Contains(after.Body, "hr") {
		t.Fatalf("坏表不应被写入：%s", after.Body)
	}

	// 大厅不是本函数的事：no-op，且不落任何新表
	if touched, err := EnsureProjectEstablishment(docs, chat.LobbyKey, "测试"); err != nil || len(touched) != 0 {
		t.Fatalf("Niuma_Studio 应 no-op（got touched=%v err=%v）", touched, err)
	}
	if _, err := docs.Get("p/default/establishment", 0); err == nil {
		t.Fatal("不应给 Niuma_Studio 落 p/default 影子表")
	}
}

// mustRows parses or fails the test — the convergence test's reader.
func mustRows(t *testing.T, body string) []EstablishmentRow {
	t.Helper()
	rows, perr := EstablishmentRows(body)
	if perr != "" {
		t.Fatalf("解析失败：%s\n%s", perr, body)
	}
	return rows
}
