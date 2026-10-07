package main

// boot_sqlite_test.go — 转正后的 boot 装配面（sqlite 恒为台账，旋钮
// 已退役）：openStores 把五域引擎开在 studio.db（落 v2Root）上，写入
// 走 SQL、JSON 台账不再被写（一次性桥在首开导入后落戳）；重启（新句
// 柄读同一库）数据仍在。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/util"
)

func TestBootSQLiteOptIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	util.MigrateLegacyPaths()

	projStore := openProjectStore()
	lobbyWS := stableWorkspace()
	if _, err := projStore.EnsureLobby(lobbyWS); err != nil {
		t.Fatalf("lobby: %v", err)
	}
	registry := chat.NewRegistry(projStore, v2Root(), defaultName(), "人类玩家")
	if _, err := registry.AdoptActive(); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	hub, err := registry.Lobby()
	if err != nil {
		t.Fatalf("lobby hub: %v", err)
	}

	st := openStoresT(t, projStore, registry, hub, "人类玩家")

	// 单库已落盘，且三仓经它服务
	dbPath := filepath.Join(v2Root(), "studio.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("studio.db 应已创建: %v", err)
	}
	req, err := st.reqs.Create("", "demo", "单库里的第一条", "", "房主")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if req.ID != "r_01" {
		t.Fatalf("首号应 r_01: %s", req.ID)
	}
	if stored, _ := st.plans.Submit("", "demo", "编排者", plan.Plan{
		Title: "单库里的提案", Tasks: []plan.PlanTask{{Title: "活"}},
	}, 100); stored == nil || stored.ID != "p_01" {
		t.Fatalf("提案应经单库盖章: %+v", stored)
	}

	// JSON 架不被 sqlite 写路径触碰（切换后新写不入 JSON）
	if _, err := os.Stat(filepath.Join(v2Root(), "requirements", "demo.json")); !os.IsNotExist(err) {
		t.Fatalf("sqlite 模式不应再写 JSON 架: %v", err)
	}

	// 重启：新句柄读同一库，写入都在（WAL 的持久性）
	d2, err := openSQLiteForTest(dbPath)
	if err != nil {
		t.Fatalf("reopen studio.db: %v", err)
	}
	defer d2.Close()
	if shelves, err := d2.ReqShelves().OpenShelves(""); err != nil || len(shelves["demo"].Reqs) != 1 || shelves["demo"].Reqs[0].Title != "单库里的第一条" {
		t.Fatalf("重启后需求应在库: %+v %v", shelves["demo"], err)
	}
	if slots, err := d2.PlanSlots().OpenSlots(""); err != nil || slots["demo"].Pending == nil || slots["demo"].Pending.ID != "p_01" {
		t.Fatalf("重启后提案应在库: %+v %v", slots["demo"], err)
	}
}

// openSQLiteForTest is the test-side reopen: a fresh handle over the
// same file (openStores 的重开等价物——装配面无需重复整个 boot)。
func openSQLiteForTest(path string) (*sqlstore.DB, error) {
	return sqlstore.Open(path)
}

// openStoresT 是 openStores 的测试面：生产进程退出即关库，测试进程
// 还要活很久——POSIX 删得动打开中的库文件，Windows 删不动，不关库
// 时 t.TempDir 的清理在 Windows 整族炸「being used by another
// process」。三平台 CI 走同一份测试，关库就得是测试自己的纪律。
func openStoresT(t *testing.T, p *projects.Store, r *chat.Registry, h *chat.Hub, who string) storeSet {
	t.Helper()
	st := openStores(p, r, h, who)
	t.Cleanup(func() {
		if st.ledgerDB != nil { // 降级内存态 boot 没有库句柄
			_ = st.ledgerDB.Close()
		}
	})
	return st
}
