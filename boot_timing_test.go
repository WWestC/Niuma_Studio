package main

// boot_timing_test.go — 冷启动开盘 A/B：同一二进制内并行 legs vs 串行
// 基线（bootStoresParallel 开关就是为这份实测留的缝）。种子家先暖身
// 一次（首开含播种/迁移写，不是重启的真实形状），随后每模式各测五
// 轮取中位。断言只做形状（两模式开出的仓内容一致），性能数字由人读。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/util"
)

func seedTimingHome(t *testing.T, home string) {
	t.Helper()
	// 种子直接落工作室根（~/.niuma 同构）：agents 档案在 ~/.niuma_agents.json，
	// 其余各仓在 .niuma/ 下各就各位。
	root := filepath.Join(home, ".niuma")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(home, ".niuma_agents.json")
	// agents：50 份档案
	as, err := agents.Open(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		as.Upsert(fmt.Sprintf("成员%02d", i), "dev", "手册"+fmt.Sprint(i), "提示"+fmt.Sprint(i))
	}
	// capability：100 条技能＋30 个 MCP
	skills, mcps := filepath.Join(root, "skills.json"), filepath.Join(root, "mcps.json")
	lib, err := capability.Open(skills, mcps)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		if _, err := lib.UpsertSkill(capability.Skill{Key: fmt.Sprintf("sk%03d", i), Name: "技能" + fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 16; i++ {
		if _, err := lib.UpsertMCP(capability.MCPServer{Key: fmt.Sprintf("mcp%02d", i), Name: fmt.Sprintf("mcp%02d", i), Type: "http", URL: "https://example.test"}); err != nil {
			t.Fatal(err)
		}
	}
	// docs：200 篇黑板文档（log-structured 回放是开盘大头）
	docs, err := kb.OpenDocs(filepath.Join(root, "kb"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 190; i++ {
		if _, err := docs.Write(fmt.Sprintf("p/proj%02d/doc%03d", i%10, i), "文档", "正文"+string(make([]byte, 512)), "房主"); err != nil {
			t.Fatal(err)
		}
	}
	// 需求架：10 项目 × 50 条；提案/合并槽各 10——种进 boot 将打开的
	// 单库（转正后台账恒 sqlite，开盘装载的重量在库里）
	db, err := sqlstore.Open(filepath.Join(root, "studio.db"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := requirements.OpenStore(db.ReqShelves(), "")
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < 10; p++ {
		key := fmt.Sprintf("proj%02d", p)
		for i := 0; i < 50; i++ {
			if _, err := rs.Create("", key, fmt.Sprintf("需求%03d", i), "正文", "房主"); err != nil {
				t.Fatal(err)
			}
		}
	}
	ps, err := plan.OpenStore(db.PlanSlots(), "")
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < 10; p++ {
		ps.Submit("", fmt.Sprintf("proj%02d", p), "编排者", plan.Plan{
			Title: "提案", Tasks: []plan.PlanTask{{Title: "活"}},
		}, 1)
	}
	db.Close()
}

func timeOpenStores(t *testing.T, home string, rounds int) []time.Duration {
	t.Helper()
	times := make([]time.Duration, 0, rounds)
	for i := 0; i < rounds; i++ {
		t.Setenv("HOME", home)        // 每轮同一家：重启形状（盘已暖）
		t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE
		projStore := openProjectStore()
		lobbyWS := stableWorkspace()
		if _, err := projStore.EnsureLobby(lobbyWS); err != nil {
			t.Fatalf("lobby: %v", err)
		}
		registry := chat.NewRegistry(projStore, v2Root(), defaultName(), "计时玩家")
		if _, err := registry.AdoptActive(); err != nil {
			t.Fatalf("adopt: %v", err)
		}
		hub, err := registry.Lobby()
		if err != nil {
			t.Fatalf("lobby hub: %v", err)
		}
		start := time.Now()
		st := openStoresT(t, projStore, registry, hub, "计时玩家")
		elapsed := time.Since(start)
		if len(st.reqs.List("")) != 500 {
			t.Fatalf("开盘后需求应为 500 条（种子一致性）: %d", len(st.reqs.List("")))
		}
		times = append(times, elapsed)
	}
	return times
}

func median(ds []time.Duration) time.Duration {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[len(ds)/2]
}

func TestBootTimingParallelVsSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("计时用例不进 -short")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	util.MigrateLegacyPaths()
	seedTimingHome(t, home)

	// 暖身一轮：首开含播种与一次性迁移（全新盘的形状），不计入。
	timeOpenStores(t, home, 1)

	bootStoresParallel = false
	serial := timeOpenStores(t, home, 5)
	bootStoresParallel = true
	parallel := timeOpenStores(t, home, 5)

	sm, pm := median(serial), median(parallel)
	t.Logf("冷启动开盘中位（同盘 5 轮）：串行 %v ｜ 并行 %v ｜ Δ %v（%.1f%%）",
		sm, pm, sm-pm, float64(sm-pm)/float64(sm)*100)
	if pm > sm {
		t.Logf("注意：并行未快于串行（种子盘较小或调度噪声）——数字如实记录")
	}
}
