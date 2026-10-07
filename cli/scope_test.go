package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/util"
)

// The CLI's default project scope (v2.9 isolation): explicit env wins,
// else the workspace→project table probed along the cwd ancestor
// chain (deepest binding wins — a member worktree nested under the
// project's own root resolves to the worktree's own binding, which is
// the same key anyway). HOME is redirected so the machine's real
// table is never read.

func TestScopeProjectFromEnv(t *testing.T) {
	t.Setenv("NIUMA_PROJECT", "book")
	if got := scopeProject(); got != "book" {
		t.Fatalf("env 缺省应生效，got %q", got)
	}
}

func TestScopeProjectWalksAncestors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	root := filepath.Join(home, "ws")
	deep := filepath.Join(root, "sub", "deeper")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := util.BindWorkspaceProject(root, "book"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := scopeProjectAt(deep); got != "book" {
		t.Fatalf("子目录应沿祖先链解析到 book，got %q", got)
	}
	if got := scopeProjectAt(home); got != "" {
		t.Fatalf("未绑定目录应无范围，got %q", got)
	}
}

func TestScopeProjectDeepestBindingWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	outer := filepath.Join(home, "ws")
	inner := filepath.Join(outer, "wt", "member")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := util.BindWorkspaceProject(outer, "book"); err != nil {
		t.Fatal(err)
	}
	if err := util.BindWorkspaceProject(inner, "book"); err != nil {
		t.Fatal(err)
	}
	if got := scopeProjectAt(inner); got != "book" {
		t.Fatalf("最深绑定应先命中，got %q", got)
	}
}

func TestPickScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())        // 无绑定表：隐式范围为空
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE
	cases := []struct{ flagged, want string }{
		{"", ""},           // 无 flag 无绑定 → 全室
		{"book", "book"},   // 显式 flag 赢
		{"-", ""},          // 显式全室（房主逃生口）
		{"all", ""},        // 同上
		{" book ", "book"}, // 去空白
	}
	for _, c := range cases {
		if got := pickScope(c.flagged); got != c.want {
			t.Errorf("pickScope(%q) = %q, want %q", c.flagged, got, c.want)
		}
	}
}

func TestWithQuery(t *testing.T) {
	if got := withQuery("/kb/docs"); got != "/kb/docs" {
		t.Fatalf("无参不加问号：%q", got)
	}
	if got := withQuery("/kb/docs", "project", "book"); got != "/kb/docs?project=book" {
		t.Fatalf("单参：%q", got)
	}
	if got := withQuery("/kb/t/x", "rev", "3", "project", "book"); got != "/kb/t/x?project=book&rev=3" {
		t.Fatalf("多参空值剔除＋url.Values 序：%q", got)
	}
}
