package fswatch

// watch_test.go — workspace watcher 的行为面：首拍只记账不广播（进程刚
// 起，工作区「存在」不是新闻）、动了的项目落一次 Notify、静止拍不落、
// .git 不进签名（git 操作的回声不广播）、触帽的树诚实降级为「不看」。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

func startWatchStudio(t *testing.T) (*Watcher, string) {
	t.Helper()
	ws := t.TempDir()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "alpha", Name: "项目甲", Workspace: ws}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return NewWatcher(projStore, nil), ws
}

func TestFsWatchNotifiesOnChangeOnly(t *testing.T) {
	w, ws := startWatchStudio(t)
	got := []string{}
	w.Notify = func(p string) { got = append(got, p) }

	w.beat() // 首拍：空 ledger → 全量首签名，只记帐
	if len(got) != 0 {
		t.Fatalf("首拍不应广播，got %v", got)
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.beat()
	if len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("动了的工作区应广播 alpha 一次，got %v", got)
	}
	w.beat() // 没动：不广播
	if len(got) != 1 {
		t.Fatalf("静止拍不应广播，got %v", got)
	}
}

func TestFsWatchIgnoresGitDir(t *testing.T) {
	w, ws := startWatchStudio(t)
	got := []string{}
	w.Notify = func(p string) { got = append(got, p) }
	w.beat()
	if err := os.MkdirAll(filepath.Join(ws, ".git", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "refs", "head"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.beat()
	if len(got) != 0 {
		t.Fatalf(".git 落盘不应广播（那是 git 操作自己的回声），got %v", got)
	}
}

func TestFsWatchCapDegradesQuietly(t *testing.T) {
	w, ws := startWatchStudio(t)
	w.WalkCap = 2 // 小帽：根 + 一个子目录就到顶
	got := []string{}
	w.Notify = func(p string) { got = append(got, p) }
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if sig := w.signature(ws); sig != "\x00capped" {
		t.Fatalf("触帽签名应退化为常量，got %q", sig)
	}
	w.beat()
	if err := os.MkdirAll(filepath.Join(ws, "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.beat() // 帽内帽外签名同值：不再广播（巨树诚实降级）
	if len(got) != 0 {
		t.Fatalf("触帽后不再广播，got %v", got)
	}
}
