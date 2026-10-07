package recruit

// templeaf_test.go — 名字→临时叶子消毒与剪贴板备份生命周期的钉子
// （安全核查修复）：敌意名字不出 TempDir、干净名字逐字节不变；备份
// 文件不可预测命名、0600、恢复成功即删、恢复失败才留盘。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSafeTempLeafFoldsAndKeeps(t *testing.T) {
	for _, name := range []string{"../pwn", "a/b", "..", "", strings.Repeat("长", 200)} {
		leaf := SafeTempLeaf("dh_recall_", name, ".txt")
		if filepath.IsAbs(leaf) || strings.Contains(leaf, "/") || strings.Contains(leaf, "\\") {
			t.Errorf("SafeTempLeaf(%q) 仍是路径：%q", name, leaf)
		}
		if strings.Contains(leaf, "..") {
			t.Errorf("SafeTempLeaf(%q) 残留点连点：%q", name, leaf)
		}
	}
	if got := SafeTempLeaf("dh_recall_", "小牛", ".txt"); got != "dh_recall_小牛.txt" {
		t.Errorf("干净名字被改动：%s", got)
	}
	if SafeTempLeaf("p_", "a/b", ".t") == SafeTempLeaf("p_", "a\\b", ".t") {
		t.Error("两个敌意名字不该共享一个叶子")
	}
}

// driveStage wires a fully injected Driver: pbpaste/pbcopy/verify all
// ride the Shell hook; nothing touches the real system.
type driveStage struct {
	restoreCalls []string
	restoreErr   error
}

func (st *driveStage) driver(t *testing.T, briefPath string) *Driver {
	t.Helper()
	return &Driver{
		AppName:   "ZCode",
		BriefPath: briefPath,
		TaskDB:    "/tmp/nope.sqlite",
		Attempts:  1,
		Verify:    func() (bool, string) { return true, "t-1 running 2026-10-05" }, // verify 命中
		RunScript: func(string) error { return nil },
		Sleep:     func(time.Duration) {},
		Printf:    func(string, ...any) {},
		Shell: func(name string, args ...string) (string, error) {
			switch {
			case name == "pbpaste":
				return "SECRET-CLIPBOARD", nil
			case name == "/bin/sh" && len(args) == 2 && strings.HasPrefix(args[1], "pbcopy < "):
				path := strings.Trim(args[1][len("pbcopy < "):], "'")
				if !strings.Contains(filepath.Base(path), "dh_recruit_clipboard_") {
					return "", nil // 装填 brief，不是恢复备份
				}
				st.restoreCalls = append(st.restoreCalls, path)
				return "", st.restoreErr
			case name == "osascript":
				return "1", nil // 窗口预检
			}
			return "", fmt.Errorf("unexpected shell call: %s %v", name, args)
		},
	}
}

func writeBrief(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "brief.txt")
	if err := os.WriteFile(p, []byte("入职标题\n正文\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClipboardBackupUnpredictableAndRemovedAfterRestore(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("GUI 注入路径仅 macOS——剪贴板语义钉在 darwin 面跑")
	}
	st := &driveStage{}
	d := st.driver(t, writeBrief(t))
	if err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.restoreCalls) != 1 {
		t.Fatalf("恢复该恰好一次，实际 %d 次", len(st.restoreCalls))
	}
	path := st.restoreCalls[0]
	if filepath.Base(path) == "dh_recruit_clipboard.bak" {
		t.Error("备份名不可再是旧固定名（可预测 symlink 目标）")
	}
	b, err := os.ReadFile(path)
	if err == nil {
		t.Errorf("恢复成功后备份该已删除，却仍在：%s（%d 字节）", path, len(b))
	}
}

func TestClipboardBackupKeptWhenRestoreFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("GUI 注入路径仅 macOS——剪贴板语义钉在 darwin 面跑")
	}
	st := &driveStage{restoreErr: os.ErrPermission}
	d := st.driver(t, writeBrief(t))
	if err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.restoreCalls) != 1 {
		t.Fatalf("恢复该尝试一次，实际 %d 次", len(st.restoreCalls))
	}
	path := st.restoreCalls[0]
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("恢复失败的备份该留盘：%v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("备份权限该 0600，实际 %o", perm)
	}
	_ = os.Remove(path)
}
