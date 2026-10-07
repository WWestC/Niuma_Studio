package dispatch

// inbox_path_test.go — 成员名→收件箱路径的消毒钉子（安全核查修复）：
// 出生名字可来自 HTTP body 与 HR 员工自己的 `dispatch birth --name`
// （LLM 可控），而 sanitizeName 只去控制字符，'/' 与 '..' 能存活——
// 未消毒时 saveLanes 会原子替换 join 出的任意 ~/.niuma 路径。这里钉死：
// 折叠后永远落在收件箱目录内、敌意名字互不共享文件、干净名字（含中文）
// 路径逐字节不变。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
)

func TestInboxPathFoldsTraversalNames(t *testing.T) {
	inbox := t.TempDir()
	d := &Dispatcher{cfg: Config{InboxDir: inbox}}
	bad := []string{"../pwn", "a/b", "a\\b", "..", ".", "…/up", "a:b", "x\x00y", "", strings.Repeat("长", 200)}
	seen := map[string]bool{}
	for _, name := range bad {
		path, err := d.inboxPath(name)
		if err != nil {
			t.Fatalf("inboxPath(%q): %v", name, err)
		}
		if filepath.Dir(path) != inbox {
			t.Errorf("inboxPath(%q) 逃出收件箱：%s", name, path)
		}
		leaf := filepath.Base(path)
		if !strings.HasSuffix(leaf, ".json") || leaf == ".json" {
			t.Errorf("inboxPath(%q) 叶子异常：%q", name, leaf)
		}
		if seen[leaf] {
			t.Errorf("两个敌意名字折叠到同一文件：%q", leaf)
		}
		seen[leaf] = true
	}
}

func TestInboxPathKeepsCleanNamesByteForByte(t *testing.T) {
	inbox := t.TempDir()
	d := &Dispatcher{cfg: Config{InboxDir: inbox}}
	for _, name := range []string{"小牛", "小马-2", "book", "AI_01"} {
		path, err := d.inboxPath(name)
		if err != nil {
			t.Fatalf("inboxPath(%q): %v", name, err)
		}
		if want := filepath.Join(inbox, name+".json"); path != want {
			t.Errorf("干净名字路径被改动：%s ≠ %s", path, want)
		}
	}
}

func TestSaveLanesStaysInsideInboxDir(t *testing.T) {
	inbox := t.TempDir()
	root := filepath.Dir(inbox) // 收件箱的父目录——逃逸会在这里现形
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	d := Start(hub, store, &ackBridge{}, Config{
		Workspace:   t.TempDir(),
		InboxDir:    inbox,
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("../pwn", agents.OrchestratorRole, "s-t-1"); err != nil {
		t.Fatal(err)
	}
	m := d.lookup("../pwn")
	if m == nil {
		t.Fatal("attach 后 lookup 落空")
	}
	d.saveLanes(m)
	d.FlushInbox() // 落盘已异步：读盘前排干

	ents, err := os.ReadDir(inbox)
	if err != nil || len(ents) != 1 {
		t.Fatalf("收件箱应恰有一个文件：%v %v", ents, err)
	}
	leaf := ents[0].Name()
	if !strings.HasSuffix(leaf, ".json") || strings.Contains(leaf, "..") {
		t.Fatalf("落盘叶子未消毒：%q", leaf)
	}
	outside, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range outside {
		if e.Name() != filepath.Base(inbox) && e.Name() != filepath.Base(d.cfg.Workspace) && strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("逃逸文件出现在收件箱外：%s/%s", root, e.Name())
		}
	}
	// 折叠后的文件还能被 loadLanes 读回（同一名字同一路径，回程不走样）。
	d.loadLanes(m)
}
