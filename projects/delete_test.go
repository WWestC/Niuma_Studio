package projects

// delete_test.go — Store.Delete（除名动词）的行为面：仅 draft/archived
// 可删（active 先归档——归档退房离编在前，删除只扫残余）、大厅进程
// 常驻不可删、除名后同 key 可再立（key 是 slug 不是墓碑）、除名后
// Exists/List 同步收敛。

import (
	"path/filepath"
	"strings"
	"testing"
)

func deleteStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.EnsureLobby(filepath.Join(t.TempDir(), "lobby-ws")); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	return s
}

func TestDeleteRefusesLobbyAndUnknown(t *testing.T) {
	s := deleteStore(t)
	if err := s.Delete(LobbyKey); err == nil || !strings.Contains(err.Error(), "不可删除") {
		t.Fatalf("大厅应拒绝删除，得 %v", err)
	}
	if err := s.Delete("ghost"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("未知 key 应报不存在，得 %v", err)
	}
}

func TestDeleteRefusesActive(t *testing.T) {
	s := deleteStore(t)
	if _, err := s.Create(Project{Key: "alpha", Name: "甲", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if _, err := s.Activate("alpha"); err != nil {
		t.Fatalf("Activate alpha: %v", err)
	}
	if err := s.Delete("alpha"); err == nil || !strings.Contains(err.Error(), "先归档") {
		t.Fatalf("active 项目应被拒并指路归档，得 %v", err)
	}
	if !s.Exists("alpha") {
		t.Fatal("被拒的项目不得除名")
	}
}

func TestDeleteDraftAndArchivedThenKeyReusable(t *testing.T) {
	s := deleteStore(t)
	ws := t.TempDir()
	if _, err := s.Create(Project{Key: "draft1", Name: "丁草稿", Workspace: ws + "/d"}); err != nil {
		t.Fatalf("Create draft1: %v", err)
	}
	if err := s.Delete("draft1"); err != nil {
		t.Fatalf("draft 应可删: %v", err)
	}
	if s.Exists("draft1") {
		t.Fatal("除名后 Exists 应为假")
	}
	for _, p := range s.List() {
		if p.Key == "draft1" {
			t.Fatal("除名后 List 不应再有该行")
		}
	}
	// key 是 slug 不是墓碑：除名后同 key 可再立
	if _, err := s.Create(Project{Key: "draft1", Name: "再生", Workspace: ws + "/d2"}); err != nil {
		t.Fatalf("除名后同 key 应可再立: %v", err)
	}
	// archived：归档是可逆的冻结，删除是不可逆的除名——归档态放行
	if _, err := s.Activate("draft1"); err != nil {
		t.Fatalf("Activate draft1: %v", err)
	}
	if _, err := s.Archive("draft1"); err != nil {
		t.Fatalf("Archive draft1: %v", err)
	}
	if err := s.Delete("draft1"); err != nil {
		t.Fatalf("archived 应可删: %v", err)
	}
}
