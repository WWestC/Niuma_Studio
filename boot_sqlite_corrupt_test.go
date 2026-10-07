package main

// boot_sqlite_corrupt_test.go — 转正后回落矩阵的 boot 侧三面钉：
// ①类损坏（垃圾 studio.db）→ 隔离文件留证＋新库照常服务＋房间通告
// 落史（绝不从 JSON 旧快照回导——一次性导入戳与通告一起把「复活陈旧
// 数据」这条路封死）；①类二次损坏（已迁移后损坏）→ 空库重启、种子
// 数据不复活；②类版本超前 → 库文件字节不动＋拒起（JSON 台账已退役，
// 无回退架——升级/修复后重启是唯一出路）。

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/util"
)

// historyHas 扫 hub 史找含关键字的系统行（SystemRecorded 入史面）。
func historyHas(hub *chat.Hub, sub string) bool {
	for _, m := range hub.History() {
		if m.Type == chat.MsgSystem && strings.Contains(m.Text, sub) {
			return true
		}
	}
	return false
}

// bootRig builds the pre-ledger boot furniture (fresh HOME) and
// returns the pieces openStores needs.
func bootRig(t *testing.T) (*projects.Store, *chat.Registry, *chat.Hub) {
	t.Helper()
	return bootRigDirect(t, t.TempDir())
}

func bootRigDirect(t *testing.T, home string) (*projects.Store, *chat.Registry, *chat.Hub) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
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
	return projStore, registry, hub
}

func TestBootSQLiteCorruptQuarantinesAndRecovers(t *testing.T) {
	// 预植垃圾库文件：sqlstore.Open 的 canary 应以 ErrCorrupt 现形，
	// boot 走隔离重开——新库从零服务（临时家无 JSON 台账，桥导零），
	// 房间落一条损坏通告。
	projStore, registry, hub := bootRig(t)
	_ = os.WriteFile(filepath.Join(v2Root(), "studio.db"), []byte("垃圾字节，不是库"), 0o644)
	st := openStoresT(t, projStore, registry, hub, "人类玩家")

	// 隔离文件在（垃圾原文留证）、新库已建且可服务
	matches, _ := filepath.Glob(filepath.Join(v2Root(), "studio.db.corrupt-*"))
	if len(matches) != 1 {
		t.Fatalf("应恰有一个隔离文件，得到 %v", matches)
	}
	if b, err := os.ReadFile(matches[0]); err != nil || string(b) != "垃圾字节，不是库" {
		t.Fatalf("隔离文件应是垃圾原文留证：%v %q", err, string(b))
	}
	if _, err := os.Stat(filepath.Join(v2Root(), "studio.db")); err != nil {
		t.Fatalf("新库应已建：%v", err)
	}
	if req, err := st.reqs.Create("", "demo", "隔离后的第一条", "", "房主"); err != nil || req.ID != "r_01" {
		t.Fatalf("新库应照常发号：%v %+v", err, req)
	}
	// 房间看得见这场损失（降级必须响亮）
	if !historyHas(hub, "数据库文件损坏") {
		t.Fatal("损坏通告应落史（隔离＋空库重启＋绝不回导）")
	}
}

// TestBootSQLiteCorruptAfterMigrationDoesNotResurrect — 转正回落矩阵
// 的地基钉：JSON 时代已被桥消费（导入戳在场）后库损坏 → 重开空库，
// 陈旧 JSON 绝不回导复活。
func TestBootSQLiteCorruptAfterMigrationDoesNotResurrect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	util.MigrateLegacyPaths()
	home := os.Getenv("HOME")
	seedJSONEra(t, home) // 手种 JSON 时代台账（首开桥的导入源）
	projStore, registry, hub := bootRigDirect(t, home)
	st := openStoresT(t, projStore, registry, hub, "人类玩家")
	if got := st.reqs.List(""); len(got) != 1 {
		t.Fatalf("首开应桥入种子需求: %+v", got)
	}
	// 损坏现场：库文件变垃圾，JSON 副本仍在（取证），戳在场。先关掉
	// 首开的库句柄（真实世界的「进程退出」——不关的话残留 WAL 会在
	// 下次打开时把主文件治愈回去，损坏注入就成了假动作）。
	_ = st.ledgerDB.Close()
	_ = os.WriteFile(filepath.Join(v2Root(), "studio.db"), []byte("二次损坏"), 0o644)

	st2 := openStoresT(t, projStore, registry, hub, "人类玩家")
	if got := st2.reqs.List(""); len(got) != 0 {
		t.Fatalf("损坏后绝不从 JSON 回导复活: %+v", got)
	}
	if req, err := st2.reqs.Create("", "demo", "空库重来", "", "房主"); err != nil || req.ID != "r_01" {
		t.Fatalf("空库应从 r_01 重起: %v %+v", err, req)
	}
	if !historyHas(hub, "绝不从 JSON") || !historyHas(hub, "数据库文件损坏") {
		t.Fatal("损坏通告应言明不回导")
	}
}

// TestBootSQLiteNewerSchemaRefusesToStart — ②类：版本超前 → 拒起（无
// 回退架）。库文件字节不动、版本钉不降级。
func TestBootSQLiteNewerSchemaRefusesToStart(t *testing.T) {
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
	dbPath := filepath.Join(v2Root(), "studio.db")
	seed, err := sqlstore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := requirements.OpenStore(seed.ReqShelves(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reqs.Create("", "keep", "版本超前的库里既有的一条", "", "房主"); err != nil {
		t.Fatal(err)
	}
	// 手植未来版本戳后关库（关库即 checkpoint，主文件落定）
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE store_meta SET v = 999 WHERE ns='' AND k='schema'`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	seed.Close()

	// ②类＝拒起（openLedgersLeg 以错误返回，boot 外层 fatal 包装）。
	_, lerr := openLedgersLeg(hub, defaultName(), false)
	if lerr == nil || !strings.Contains(lerr.Error(), "请升级") {
		t.Fatalf("版本超前应拒起（无回退架）: %v", lerr)
	}
	// 拒开不碰库（语义面）：版本钉原样、既有数据原样
	chk, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := chk.QueryRow(`SELECT v FROM store_meta WHERE ns='' AND k='schema'`).Scan(&v); err != nil || v != 999 {
		t.Fatalf("版本钉不得被降级覆写：v=%d err=%v", v, err)
	}
	var n int
	if err := chk.QueryRow(`SELECT count(*) FROM req_shelves WHERE project='keep'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("拒开不得动库内数据：n=%d err=%v", n, err)
	}
	chk.Close()
}

// TestBootStoreKnobRetired — NIUMA_STORE 旋钮退役：仍带值的部署按其
// 期望会破裂——响亮拒起好过静默改道。
func TestBootStoreKnobRetired(t *testing.T) {
	projStore, registry, hub := bootRig(t)
	_ = projStore
	_ = registry
	t.Setenv("NIUMA_STORE", "json")
	if _, err := openLedgersLeg(hub, defaultName(), false); err == nil || !strings.Contains(err.Error(), "NIUMA_STORE") {
		t.Fatalf("旋钮残值应响亮拒起: %v", err)
	}
}
