package dispatch

// patrol_fallback_test.go — 复查#4 的钉：项目房的产能巡报在自己房里
// 没有 HR 岗时回落到大厅 HR（人事是工作室级岗位——EstablishmentAdded
// 同款路由学说），快照自带项目名；项目房与大厅都没有 HR 才如实报错。
// 无回落的老形状在标准编制拓扑（HR 只驻大厅）下让每个项目房的巡报
// 永远静默跳过——文案承诺的巡报从未送达。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// patrolFleetStage is one two-room fleet over a recording bridge: the
// lobby and one project room, both ACTIVE — the standard topology with
// the studio-level HR seated in the lobby only.
func patrolFleetStage(t *testing.T) (*Fleet, *recBridge) {
	t.Helper()
	dir := t.TempDir()
	projs, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{chat.LobbyKey, "proj-p"} {
		if _, err := projs.Create(projects.Project{Key: key, Name: key,
			Workspace: filepath.Join(dir, key)}); err != nil {
			t.Fatal(err)
		}
		if _, err := projs.Activate(key); err != nil {
			t.Fatal(err)
		}
	}
	staff, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	rb := &recBridge{got: map[string][]string{}}
	f := StartFleet(rb, agentStore, projs, staff,
		map[string]*chat.Hub{chat.LobbyKey: chat.NewHub(), "proj-p": chat.NewHub()},
		Config{Workspace: dir, InboxDir: t.TempDir(),
			Model: "GLM-5.3", Reasoning: "high",
			PatrolEvery: -1, DailyAt: "off"})
	t.Cleanup(f.Stop)
	return f, rb
}

// TestFleetPatrolFallsBackToLobbyHR: 项目房只有编排者、大厅坐着 HR——
// 巡报经回落送到大厅 HR 的会话，注入带项目名；两处都没有 HR 时如实
// 报错（不再静默饿死）。
func TestFleetPatrolFallsBackToLobbyHR(t *testing.T) {
	f, rb := patrolFleetStage(t)
	if err := f.Get("proj-p").attach("小牛", agents.OrchestratorRole, "s-orch"); err != nil {
		t.Fatal(err)
	}

	// 两处都无 HR：如实报错
	err := f.AutoPilotPatrol("proj-p", "饱和度 0%（在办 0/1 座）｜需求池 open 3 条")
	if err == nil || !strings.Contains(err.Error(), "Niuma_Studio") {
		t.Fatalf("项目房与 Niuma_Studio 均无 HR 应回错指路（got %v）", err)
	}

	// 大厅落座 HR（工作室级岗位的标准位置）→ 回落送达
	if err := f.Get(chat.LobbyKey).attach("人事姐", agents.HRRole, "s-hr"); err != nil {
		t.Fatal(err)
	}
	if err := f.AutoPilotPatrol("proj-p", "饱和度 0%（在办 0/1 座）｜需求池 open 3 条"); err != nil {
		t.Fatalf("项目房无 HR 应回落 Niuma_Studio 送达：%v", err)
	}
	if got := rb.all("s-hr"); !strings.Contains(got, "产能巡报") || !strings.Contains(got, "proj-p") {
		t.Fatalf("Niuma_Studio HR 应收到带项目上下文的巡报：%q", got)
	}
	if got := rb.all("s-orch"); strings.Contains(got, "产能巡报") {
		t.Fatal("巡报不该误送项目房的编排者")
	}
}
