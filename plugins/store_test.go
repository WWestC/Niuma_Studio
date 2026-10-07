package plugins

// store.go 的磁盘真相测试：扫描、启用态持久化、跨插件板块撞车让位、
// install/remove 的复制语义。衣橱应用（ApplyWardrobe）端到端在
// wardrobe_test.go。

import (
	"os"
	"path/filepath"
	"testing"
)

const goodPluginJSON = `{
	"id": "a.demo", "name": "演示", "version": "0.1.0",
	"permissions": ["boards"],
	"contributes": { "boards": [ {"id": "demo", "title": "演示板", "entry": "web/main.js"} ] }
}`

func writePlugin(t *testing.T, root, id, manifest string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func openTempStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "plugins.json")
	return Open(root, state), root, state
}

func TestLoadScansAndDefaultsEnabled(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.demo", goodPluginJSON, map[string]string{"web/main.js": "export function activate(){}"})
	// 一个不是插件的目录：没有 plugin.json
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := s.Load()
	if len(ps) != 2 {
		t.Fatalf("该扫出 2 个目录，得到 %d", len(ps))
	}
	if ps[0].ID != "a.demo" || !ps[0].Enabled || len(ps[0].Problems) != 0 {
		t.Fatalf("健康插件走样：%+v", ps[0])
	}
	if ps[1].Manifest != nil || ps[1].Enabled {
		t.Fatalf("缺 manifest 的目录该记为停用坏件：%+v", ps[1])
	}
}

func TestSetEnabledPersists(t *testing.T) {
	s, root, state := openTempStore(t)
	writePlugin(t, root, "a.demo", goodPluginJSON, nil)
	if err := s.SetEnabled("a.demo", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("启用态没落盘：%v", err)
	}
	// 新 Store（重启视角）读同一份状态文件
	s2 := Open(root, state)
	ps := s2.Load()
	if len(ps) != 1 || ps[0].Enabled {
		t.Fatalf("停用态没跨 Store 存活：%+v", ps)
	}
	if err := s2.SetEnabled("a.demo", true); err != nil {
		t.Fatal(err)
	}
	if ps = s2.Load(); len(ps) != 1 || !ps[0].Enabled {
		t.Fatalf("恢复启用失败：%+v", ps)
	}
	if err := s2.SetEnabled("ghost.x", true); err == nil {
		t.Fatal("给没装过的插件翻开关该被拒")
	}
}

func TestLoadBoardConflictEarlierIDWins(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.first", `{
		"id": "a.first", "name": "先到", "version": "0.1.0",
		"contributes": { "boards": [ {"id": "dup", "title": "先到的", "entry": "web/x.js"} ] }
	}`, nil)
	writePlugin(t, root, "b.second", `{
		"id": "b.second", "name": "后到", "version": "0.1.0",
		"contributes": { "boards": [ {"id": "dup", "title": "后到的", "entry": "web/y.js"}, {"id": "mine", "title": "自留", "entry": "web/z.js"} ] }
	}`, nil)
	ps := s.Load()
	var second *Plugin
	for _, p := range ps {
		if p.ID == "b.second" {
			second = p
		}
	}
	if second == nil {
		t.Fatal("没扫到 b.second")
	}
	if len(second.Boards()) != 1 || second.Boards()[0].ID != "mine" {
		t.Fatalf("撞车板块该被摘掉、自留的保住：%v", second.Boards())
	}
	if !hasProblem(second.Problems, "撞 id") {
		t.Fatalf("撞车该记问题：%v", second.Problems)
	}
}

func TestEngineFloorDisablesAtLoad(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.future", `{
		"id": "a.future", "name": "未来件", "version": "0.1.0", "engine": ">=9.9",
		"contributes": { "boards": [ {"id": "fut", "title": "未来板", "entry": "web/x.js"} ] }
	}`, nil)
	ps := s.Load()
	if len(ps) != 1 || ps[0].Enabled {
		t.Fatalf("未来 engine 的插件该在装载面停用：%+v", ps[0])
	}
}

func TestInstallAndRemove(t *testing.T) {
	s, root, _ := openTempStore(t)
	src := t.TempDir()
	writePlugin(t, src, "ignored-dir-name", goodPluginJSON, map[string]string{
		"web/main.js":  "export function activate(){}",
		"assets/x.png": "png",
	})
	id, err := s.Install(filepath.Join(src, "ignored-dir-name"), false)
	if err != nil {
		t.Fatal(err)
	}
	if id != "a.demo" {
		t.Fatalf("安装后以 manifest id 为址，得到 %s", id)
	}
	// 目录名 = manifest id；文件都搬过去了
	if _, err := os.Stat(filepath.Join(root, "a.demo", "web", "main.js")); err != nil {
		t.Fatalf("资产没搬过去：%v", err)
	}
	// 装好的能扫到且默认启用
	ps := s.Load()
	if len(ps) != 1 || !ps[0].Enabled {
		t.Fatalf("安装后扫描走样：%+v", ps)
	}
	// 重复装被拒，--force 放行
	if _, err := s.Install(filepath.Join(src, "ignored-dir-name"), false); err == nil {
		t.Fatal("重复安装该被拒")
	}
	if _, err := s.Install(filepath.Join(src, "ignored-dir-name"), true); err != nil {
		t.Fatalf("force 覆盖该放行：%v", err)
	}
	// 坏 manifest 拒装
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "plugin.json"), []byte(`{"id":"Bad ID"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(bad, false); err == nil {
		t.Fatal("坏 manifest 该拒装")
	}
	// 卸载
	if err := s.Remove("a.demo"); err != nil {
		t.Fatal(err)
	}
	if ps = s.Load(); len(ps) != 0 {
		t.Fatalf("卸载后该扫不到，得到 %d 个", len(ps))
	}
	if err := s.Remove("a.demo"); err == nil {
		t.Fatal("卸载不存在的插件该被拒")
	}
	if err := s.Remove("../evil"); err == nil {
		t.Fatal("路径材料不该被当 id 接受")
	}
}
