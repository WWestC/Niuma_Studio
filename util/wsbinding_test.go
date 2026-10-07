package util

import (
	"os"
	"path/filepath"
	"testing"
)

// The workspace→project table (v2.9 isolation's session anchor): bind,
// read, unbind, and the never-bind-a-meaningless-root guard. HOME is
// redirected per test so the machine's real table is never touched.

func bindTable(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
}

func TestWorkspaceBindingRoundTrip(t *testing.T) {
	bindTable(t)
	ws := filepath.Join(t.TempDir(), "proj-book")
	if err := BindWorkspaceProject(ws, "book"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if key, ok := ProjectOfWorkspace(ws); !ok || key != "book" {
		t.Fatalf("bound key = %q ok=%v, want book", key, ok)
	}
	// rebind same value is a no-op; unbind removes
	if err := BindWorkspaceProject(ws, "book"); err != nil {
		t.Fatalf("idempotent bind: %v", err)
	}
	if err := BindWorkspaceProject(ws, ""); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if _, ok := ProjectOfWorkspace(ws); ok {
		t.Fatal("unbound workspace should not resolve")
	}
}

func TestWorkspaceBindingNeverBindsRoot(t *testing.T) {
	bindTable(t)
	for _, ws := range []string{"", ".", "/"} {
		if err := BindWorkspaceProject(ws, "book"); err != nil {
			t.Fatalf("bind %q: %v", ws, err)
		}
	}
	m, err := ReadWorkspaceProjects()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("meaningless roots must leave the table empty, got %v", m)
	}
}

func TestWorkspaceBindingMissingFileIsEmpty(t *testing.T) {
	bindTable(t)
	m, err := ReadWorkspaceProjects()
	if err != nil || len(m) != 0 {
		t.Fatalf("missing table should read empty, got %v err=%v", m, err)
	}
	if _, ok := ProjectOfWorkspace("/nowhere"); ok {
		t.Fatal("empty table resolves nothing")
	}
	_ = os.UserHomeDir // keep os import honest for HOME-sensitive helpers
}
