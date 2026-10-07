package util

// reset_test.go — the factory reset's data half: the wipe must take
// every studio-authored path under the home (v2 root, kb docs, flat
// ledgers, legacy import source, seat tokens, wait cursors) while
// leaving a stranger's files untouched, and must refuse unsafe homes.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// seedStudio lays out one fully-lived-in studio under home: every
// store surface the real app writes, plus an unrelated file that must
// survive.
func seedStudio(t *testing.T, home string) {
	t.Helper()
	mk := func(rel string) {
		t.Helper()
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mk(".niuma/room/default.json")
	mk(".niuma/history/default.jsonl")
	mk(".niuma/staffing.json")
	mk(".niuma/projects.json")
	mk(".niuma/notice/default.json")
	mk(".niuma/capabilities.json")
	mk(".niuma/audit/assembly.jsonl")
	mk(".niuma/assistant.json")
	mk(".niuma_kb/manual/说明书.md")
	mk(".niuma_kb/roles/hr.md")
	mk(".niuma_agents.json")
	mk(".niuma_tasks.json")
	mk(".niuma_ranks.json")
	mk(".niuma_blackboard.md")
	mk(".niuma_token_小明")
	mk(".niuma_wait_小红")
	mk("notes-aplenty.md") // 无关文件：擦除不得越界
}

func TestWipeStudioDataTakesEveryStudioPath(t *testing.T) {
	home := t.TempDir()
	seedStudio(t, home)

	removed, err := wipeStudioDataAt(home)
	if err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if len(removed) == 0 {
		t.Fatal("擦除清单为空——店铺数据没被碰到")
	}
	gone := []string{
		".niuma", ".niuma_kb", ".niuma_agents.json", ".niuma_tasks.json",
		".niuma_ranks.json", ".niuma_blackboard.md",
		".niuma_token_小明", ".niuma_wait_小红",
	}
	for _, rel := range gone {
		if _, err := os.Stat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("%s 擦除后仍存在", rel)
		}
	}
	// 无关文件与端口发现文件都不在擦除面里（后者归 srv.Close 摘）
	if _, err := os.Stat(filepath.Join(home, "notes-aplenty.md")); err != nil {
		t.Errorf("无关文件被误擦: %v", err)
	}
}

func TestWipeStudioDataOnFreshHomeIsNoop(t *testing.T) {
	home := t.TempDir() // 首装状态：无可擦，也无错
	removed, err := wipeStudioDataAt(home)
	if err != nil {
		t.Fatalf("空屋擦除应无错: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("空屋擦除清单应为空，得 %v", removed)
	}
}

func TestWipeStudioDataRefusesUnsafeHome(t *testing.T) {
	if _, err := wipeStudioDataAt(""); err == nil {
		t.Fatal("空家目录必须拒绝")
	}
	if _, err := wipeStudioDataAt("/"); err == nil {
		t.Fatal("根目录必须拒绝")
	}
}

// TestOpenBundleScript — the macOS bundle relay must (a) hand the
// launch to Launch Services（open，绝不裸 exec 内层二进制——没登记的
// 启动会把 Dock 图标打回通用/幽灵 tile）, (b) wait for the old pid to
// die first（对着垂死的已登记实例 open 只会被当 reopen，工作室就再也
// 回不来了）, and (c) pass flags through --args. Non-bundle runs and
// other platforms keep the direct-exec relay (ok=false).
func TestOpenBundleScript(t *testing.T) {
	const inBundle = "/Users/x/Studio/Niuma_Studio.app/Contents/MacOS/niuma"
	script, ok := openBundleScript(inBundle, 42, []string{"--port", "8888"})
	if runtime.GOOS != "darwin" {
		if ok {
			t.Fatal("非 darwin 不得走 bundle 接力")
		}
		return
	}
	if !ok {
		t.Fatal("bundle 内必须走 Launch Services 接力")
	}
	if !strings.Contains(script, "kill -0 42") {
		t.Errorf("接力必须先等旧 pid 退场: %s", script)
	}
	if !strings.Contains(script, `exec /usr/bin/open "/Users/x/Studio/Niuma_Studio.app"`) {
		t.Errorf("接力必须经 open 拉起 bundle 本体: %s", script)
	}
	if !strings.Contains(script, `--args "--port" "8888"`) {
		t.Errorf("旗标必须经 --args 透传: %s", script)
	}
	if _, ok := openBundleScript("/Users/x/Studio/niuma", 42, nil); ok {
		t.Fatal("裸二进制（终端/开发态）保持直接 exec 接力")
	}
}
