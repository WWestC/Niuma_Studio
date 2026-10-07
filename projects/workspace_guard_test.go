package projects

// workspace_guard_test.go — v2.13 立项工作区守卫的行为面：Create/Update
// 拒绝文件系统根目录（大厅的根毒修复条款记录过根下出生的代价——这里
// 让它根本立不了项）；拒绝与在册（draft/active）项目重复的工作区
// （v2.9 隔离绑定按目录一对一，两个活项目共用一个目录会互相踩踏）；
// 归档项目的目录不算占用——归档→另立的迁移路径保住。

import (
	"path/filepath"
	"strings"
	"testing"
)

func guardStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestCreateRefusesRootWorkspace(t *testing.T) {
	volRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator) // Windows 的根要带卷名（C:\ 才算绝对路径）
	s := guardStore(t)
	_, err := s.Create(Project{Key: "rooted", Name: "根下项目", Workspace: volRoot})
	if err == nil || !strings.Contains(err.Error(), "根目录") {
		t.Fatalf("根目录 workspace 应被拒，得 %v", err)
	}
	if s.Exists("rooted") {
		t.Fatal("拒绝的项目不得入账")
	}
}

func TestCreateRefusesWorkspaceHeldByLiveProject(t *testing.T) {
	ws := t.TempDir()
	s := guardStore(t)
	if _, err := s.Create(Project{Key: "alpha", Name: "甲", Workspace: ws}); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if _, err := s.Activate("alpha"); err != nil {
		t.Fatalf("Activate alpha: %v", err)
	}
	// active 占用：同名目录立第二项 → 拒，且报出占用者
	_, err := s.Create(Project{Key: "beta", Name: "乙", Workspace: ws})
	if err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("active 项目的目录应拒绝复用并点名占用者，得 %v", err)
	}
	// draft 同样占用：大厅之外第一个草稿就锁住目录
	if _, err := s.Create(Project{Key: "gamma", Name: "丙", Workspace: ws + "/sub"}); err != nil {
		t.Fatalf("Create gamma: %v", err)
	}
	if _, err := s.Create(Project{Key: "delta", Name: "丁", Workspace: ws + "/sub"}); err == nil {
		t.Fatal("draft 项目的目录也应拒绝复用")
	}
	// 归档释放：alpha 归档后，它的目录可被新项目接手（迁移路径）
	if _, err := s.Archive("alpha"); err != nil {
		t.Fatalf("Archive alpha: %v", err)
	}
	if _, err := s.Create(Project{Key: "heir", Name: "接班", Workspace: ws}); err != nil {
		t.Fatalf("归档后的目录应可复用: %v", err)
	}
}

func TestUpdateWorkspaceGuards(t *testing.T) {
	volRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	wsA, wsB := t.TempDir(), t.TempDir()
	s := guardStore(t)
	if _, err := s.Create(Project{Key: "alpha", Name: "甲", Workspace: wsA}); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if _, err := s.Activate("alpha"); err != nil {
		t.Fatalf("Activate alpha: %v", err)
	}
	if _, err := s.Create(Project{Key: "beta", Name: "乙", Workspace: wsB}); err != nil {
		t.Fatalf("Create beta: %v", err)
	}
	// draft 改到根 → 拒
	if _, err := s.Update("beta", Patch{Workspace: volRoot}); err == nil || !strings.Contains(err.Error(), "根目录") {
		t.Fatalf("draft 改工作区到根应被拒，得 %v", err)
	}
	// draft 改到 active 的目录 → 拒
	if _, err := s.Update("beta", Patch{Workspace: wsA}); err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("draft 改工作区撞 active 应被拒并点名，得 %v", err)
	}
	// 原样重述自己的目录 → 过（不是变更，无冲突可言）
	if _, err := s.Update("beta", Patch{Workspace: wsB, Name: "乙改"}); err != nil {
		t.Fatalf("重述自己的工作区应放行: %v", err)
	}
}
