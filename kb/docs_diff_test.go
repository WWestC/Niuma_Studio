package kb

import (
	"errors"
	"os"
	"testing"
)

// DiffAround (黑板红绿对比的取材口): the pre-state must be exactly the
// body the write at that rev replaced — the history archive and the
// file swap are one locked step — and the pair must refuse to serve a
// rev a newer write already covered, so the broadcast can never diff
// the wrong write.

func TestDiffAroundWrite(t *testing.T) {
	s, err := OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	if _, err := s.Write("roles/x", "手册", "第一版\n共用行", "小马"); err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	meta, err := s.Write("roles/x", "手册", "第二版\n共用行", "小牛")
	if err != nil {
		t.Fatalf("write rev2: %v", err)
	}
	old, cur, err := s.DiffAround("roles/x", meta.Rev)
	if err != nil {
		t.Fatalf("DiffAround: %v", err)
	}
	if old != "第一版\n共用行" {
		t.Fatalf("写前正文应取自 rev2 归档链（第一版内容），got %q", old)
	}
	if cur != "第二版\n共用行" {
		t.Fatalf("写后正文应为当前文件，got %q", cur)
	}
}

func TestDiffAroundNewDocEmptyOld(t *testing.T) {
	s, _ := OpenDocs(t.TempDir())
	meta, err := s.Write("roles/y", "新手册", "起初", "小马")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	old, cur, err := s.DiffAround("roles/y", meta.Rev)
	if err != nil || old != "" || cur != "起初" {
		t.Fatalf("新文档 old 应为空、cur 为正文，got old=%q cur=%q err=%v", old, cur, err)
	}
}

func TestDiffAroundStaleRevRefuses(t *testing.T) {
	s, _ := OpenDocs(t.TempDir())
	if _, err := s.Write("roles/z", "手册", "一", "小马"); err != nil {
		t.Fatalf("write rev1: %v", err)
	}
	if _, err := s.Write("roles/z", "手册", "二", "小牛"); err != nil {
		t.Fatalf("write rev2: %v", err)
	}
	if _, _, err := s.DiffAround("roles/z", 1); err == nil {
		t.Fatal("已被覆盖的 rev 应拒绝（diff 会对错写）")
	}
}

func TestDiffAroundAppendAndMissing(t *testing.T) {
	s, _ := OpenDocs(t.TempDir())
	if _, err := s.Write("ops/log", "台账", "首行", "小马"); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := s.Append("ops/log", "第二行", "小牛")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	old, cur, err := s.DiffAround("ops/log", meta.Rev)
	if err != nil || old != "首行" || cur != "首行\n第二行\n" {
		t.Fatalf("追加后 old/cur 应为首行/两行（尾换行是文件标点照读），got old=%q cur=%q err=%v", old, cur, err)
	}
	if _, _, err := s.DiffAround("ops/none", 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("未知 key 应 ErrNotExist，got %v", err)
	}
}
