package dispatch

// project_recruit_test.go — v2.5 分项目编制的招聘钉：每个 active 项目的
// 系统岗（编排者/HR 各一套）随启动/开张自动出生到岗——大厅先满编（门
// 只等它），项目随后一拍一雇；项目的人从取名池各取其名（与大厅的小牛/
// 小马、与其他项目互不重名）；本项目离编的旧人优先复职（身份连续）；
// 池名被占则确定性避让、池尽则「小牛·<key>」兜底；招聘循环收摊后项目
// 开张会重新点火（EnsureRoom 尾部），且 splash 的门不再回等。

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// projectRecruitStage is one fleet over fresh stores with the lobby
// plus the given active projects, each with its room — the per-project
// recruiter's stage (recruitStage's multi-room twin).
func projectRecruitStage(t *testing.T, bridge Bridge, keys ...string) (*Fleet, map[string]*chat.Hub, *staffing.Store, *agents.Store, *kb.DocsStore, map[string]int) {
	t.Helper()
	dir := t.TempDir()
	projs, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.EnsureLobby(dir); err != nil {
		t.Fatal(err)
	}
	hubs := map[string]*chat.Hub{chat.LobbyKey: chat.NewHub()}
	for _, key := range keys {
		if _, err := projs.Create(projects.Project{Key: key, Name: key,
			Workspace: filepath.Join(dir, key)}); err != nil {
			t.Fatal(err)
		}
		if _, err := projs.Activate(key); err != nil {
			t.Fatal(err)
		}
		hubs[key] = chat.NewHub()
	}
	staff, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := kb.OpenDocs(filepath.Join(dir, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	ranks := map[string]int{}
	oldTick := recruitTick
	recruitTick = 20 * time.Millisecond
	t.Cleanup(func() { recruitTick = oldTick })
	f := StartFleet(bridge, store, projs, staff, hubs,
		Config{Workspace: dir, Docs: docs,
			SetRank: func(name string, lv int) { ranks[name] = lv }})
	t.Cleanup(f.Stop)
	return f, hubs, staff, store, docs, ranks
}

// seatedRoles snapshots one room's role → persons (live seats only).
func seatedRoles(hub *chat.Hub) map[string][]string {
	out := map[string][]string{}
	for _, m := range hub.Members() {
		if m.Role != "" {
			out[m.Role] = append(out[m.Role], m.Name)
		}
	}
	return out
}

// waitProjectSeated polls until the room holds both system roles.
func waitProjectSeated(t *testing.T, hub *chat.Hub, key string) map[string][]string {
	t.Helper()
	waitFor(t, func() bool {
		r := seatedRoles(hub)
		return len(r[agents.OrchestratorRole]) >= 1 && len(r[agents.HRRole]) >= 1
	}, "项目 "+key+" 的编排者/HR 应自动到岗")
	return seatedRoles(hub)
}

// TestProjectRecruitBirthsPerProjectSystemPosts is the parity pin: one
// boot kick fills the lobby (小牛/小马) AND every project's own pair —
// pool names distinct across projects, staffing rows active, Lv.8
// stamped, the project tables carrying the seated present view.
func TestProjectRecruitBirthsPerProjectSystemPosts(t *testing.T) {
	f, hubs, staff, store, docs, ranks := projectRecruitStage(t, &stubBridge{}, "proj-p1", "proj-p2")
	f.KickFlagshipRecruit()

	waitRecruit(t, f, func(p RecruitProgress) bool { return p.Done })
	p1 := waitProjectSeated(t, hubs["proj-p1"], "proj-p1")
	p2 := waitProjectSeated(t, hubs["proj-p2"], "proj-p2")

	// 大厅照旧是小牛/小马；项目的人不是他们（分项目各一套）
	lobby := seatedRoles(hubs[chat.LobbyKey])
	if lobby[agents.OrchestratorRole][0] != "小牛" || lobby[agents.HRRole][0] != "小马" {
		t.Fatalf("Niuma_Studio 旗舰应仍是小牛/小马：%v", lobby)
	}
	for _, roles := range []map[string][]string{p1, p2} {
		for _, persons := range roles {
			for _, name := range persons {
				if name == "小牛" || name == "小马" {
					t.Fatalf("项目系统岗不该复用 Niuma_Studio 的人：%v", roles)
				}
			}
		}
	}
	// 两个项目的四个人互不重名（agents 全局唯一、一人一时一房）
	seen := map[string]bool{}
	for _, roles := range []map[string][]string{p1, p2} {
		for _, persons := range roles {
			for _, name := range persons {
				if seen[name] {
					t.Fatalf("两个项目不应共用同一人：%s", name)
				}
				seen[name] = true
			}
		}
	}
	// staffing 行 active、Lv.8 已落、档案带手册锚（HR → roles/hr）
	for _, key := range []string{"proj-p1", "proj-p2"} {
		waitFor(t, func() bool {
			act := 0
			for _, row := range staff.ListByProject(key) {
				if row.State == staffing.StateActive {
					act++
				}
			}
			return act >= 2
		}, key+" 的两名系统岗 staffing 行应转 active")
	}
	for name := range seen {
		if ranks[name] != 8 {
			t.Fatalf("项目系统岗 %s 应 Lv.8：%d", name, ranks[name])
		}
	}
	if hrName := p2[agents.HRRole][0]; hrName != "" {
		if cfg, ok := store.Get(hrName); !ok || cfg.Manual != "roles/hr" {
			t.Fatalf("项目 HR 档案应带 roles/hr：%v", cfg)
		}
	}
	// 项目表的报告面照常对账：在岗落座（LiveEstablishmentRows 口径）
	rows, perr := kb.LiveEstablishmentRows(hubs["proj-p1"], docs, "p/proj-p1/establishment")
	if perr != "" {
		t.Fatalf("项目表不可读：%s", perr)
	}
	byKey := map[string]kb.EstablishmentRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if len(byKey["orchestrator"].Present) != 1 || len(byKey["hr"].Present) != 1 {
		t.Fatalf("项目系统岗应在岗：%v", rows)
	}
	if byKey["assistant"].Missing != 0 {
		t.Fatalf("小助手免对账：%+v", byKey["assistant"])
	}
}

// TestProjectRecruitRebirthsOffboardedHolder pins the identity rule:
// the project's own OFFBOARDED holder comes back (same person, same
// name — kick/归档 后的复职)， not a fresh pool name.
func TestProjectRecruitRebirthsOffboardedHolder(t *testing.T) {
	f, hubs, staff, _, _, _ := projectRecruitStage(t, &stubBridge{}, "proj-r")
	// 预置一名离编旧人：曾是 proj-r 的编排者
	if _, err := staff.Join("proj-r", "小鹿", agents.OrchestratorRole, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.Offboard("proj-r", "小鹿"); err != nil {
		t.Fatal(err)
	}
	f.KickFlagshipRecruit()
	waitRecruit(t, f, func(p RecruitProgress) bool { return p.Done })
	roles := waitProjectSeated(t, hubs["proj-r"], "proj-r")
	if got := roles[agents.OrchestratorRole]; len(got) != 1 || got[0] != "小鹿" {
		t.Fatalf("离编旧人小鹿应复职回编排者岗：%v", got)
	}
}

// TestFreshProjectPersonDodgesTaken unit-pins the name roller: the
// deterministic pick avoids names already in the store, and an
// exhausted pool falls back to 小牛·<key> (within the 24-rune cap).
func TestFreshProjectPersonDodgesTaken(t *testing.T) {
	f, _, _, store, _, _ := projectRecruitStage(t, &stubBridge{})
	post := kb.FlagshipPosts[0] // orchestrator，大厅首任小牛
	first := f.freshProjectPerson("proj-x", post)
	// 占掉确定性首选 → 避让到池内下一个未用名
	store.Upsert(first, "路人", "", "")
	next := f.freshProjectPerson("proj-x", post)
	if next == first {
		t.Fatalf("首选被占应避让：%s", next)
	}
	if !inPool(next) {
		t.Fatalf("避让名应仍在池内：%s", next)
	}
	// 池全占 → 兜底「小牛·proj-x」
	for _, name := range projectPersonPool {
		store.Upsert(name, "路人", "", "")
	}
	if got := f.freshProjectPerson("proj-x", post); got != "小牛·proj-x" {
		t.Fatalf("池尽应兜底小牛·proj-x，得 %s", got)
	}
}

func inPool(name string) bool {
	for _, p := range projectPersonPool {
		if p == name {
			return true
		}
	}
	return false
}

// TestKickProjectRecruitRearmsSettledLoop pins the activation leg:
// after the boot loop settles (lobby done, no projects), a project
// opened mid-run re-arms it through EnsureRoom — its pair gets hired
// — and the door's progress never re-waits (Done stays true).
func TestKickProjectRecruitRearmsSettledLoop(t *testing.T) {
	f, hubs, _, _, _, _ := projectRecruitStage(t, &stubBridge{})
	f.KickFlagshipRecruit()
	waitRecruit(t, f, func(p RecruitProgress) bool { return p.Done })
	// 等循环收摊（收摊才谈「重新点火」）
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.recruitRunning
	}, "招聘循环应收摊")
	hubLate := chat.NewHub()
	hubs["proj-late"] = hubLate
	if _, err := f.projs.Create(projects.Project{Key: "proj-late", Name: "proj-late",
		Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.projs.Activate("proj-late"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.EnsureRoom("proj-late", hubLate); err != nil {
		t.Fatalf("开张应起调度器：%v", err)
	}
	waitProjectSeated(t, hubLate, "proj-late")
	if rp := f.FlagshipRecruit(); !rp.Done {
		t.Fatalf("项目招聘不该让 splash 的门回等：%+v", rp)
	}
}
