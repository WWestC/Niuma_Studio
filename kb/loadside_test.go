package kb

// loadside_test.go — 加载侧重验证 key 的钉子（安全核查修复）：
// snapshot.json 与 log.jsonl 是状态真相源，但 key 若被毒化成 '../x'
// 一类，导出面（Export、CLI kb export）会把它写出目标目录。全仓其他
// store 都在加载时重验外来文件，kb 这两条腿以前没有——现在钉死：
// 非法 key 的快照行与日志行都进不了状态，导出永远不出目标目录。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// poisonKB lays down a hand-crafted store dir: a snapshot with one legal
// and two poisoned keys, and a log tail with one poisoned write and one
// legal write.
func poisonKB(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := map[string]any{
		"version": 1,
		"applied": 3,
		"docs": map[string]any{
			"good/snap": map[string]any{
				"meta": map[string]any{"key": "good/snap", "title": "合法行", "rev": 1},
				"body": "正文",
			},
			"../evil-snap": map[string]any{
				"meta": map[string]any{"key": "../evil-snap", "title": "毒行", "rev": 1},
				"body": "不该进来",
			},
			"a/../../evil-deep": map[string]any{
				"meta": map[string]any{"key": "a/../../evil-deep", "title": "毒行", "rev": 1},
				"body": "不该进来",
			},
		},
	}
	sb, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), sb, 0o600); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"seq":4,"op":"write","key":"../evil-log","title":"毒行","body":"不该进来","by":"x","ts":1}`,
		`{"seq":5,"op":"write","key":"good/log","title":"合法日志行","body":"正文","by":"x","ts":2}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "log.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadSideRejectsPoisonedKeys(t *testing.T) {
	dir := poisonKB(t)
	s, err := OpenDocs(dir)
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	for _, key := range []string{"../evil-snap", "a/../../evil-deep", "../evil-log"} {
		if _, err := s.Get(key, 0); err == nil {
			t.Errorf("毒化 key %q 不该进状态", key)
		}
	}
	for _, key := range []string{"good/snap", "good/log"} {
		if _, err := s.Get(key, 0); err != nil {
			t.Errorf("合法 key %q 该照常加载：%v", key, err)
		}
	}
	// 直接查状态表（Get 自身也会验 key，绕开它才不是空赢）。
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.docs) != 2 {
		keys := make([]string, 0, len(s.docs))
		for k := range s.docs {
			keys = append(keys, k)
		}
		t.Fatalf("状态该只剩两条合法行，实际 %d 条：%v", len(s.docs), keys)
	}
	for k := range s.docs {
		if ValidateKey(k) != nil {
			t.Errorf("非法 key %q 进了状态", k)
		}
	}
}

func TestExportStaysInsideTargetDir(t *testing.T) {
	dir := poisonKB(t)
	s, err := OpenDocs(dir)
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	holder := t.TempDir()
	out := filepath.Join(holder, "export")
	written, err := s.Export(out)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for _, p := range written {
		// Export 返回相对叶子——本地相对路径（不越出、不绝对）即合格。
		if !filepath.IsLocal(p) {
			t.Errorf("导出路径不落在目标目录内：%s", p)
		}
	}
	// 目标目录之外不该多出任何目录（逃逸现形处）；out 内部的子目录是
	// key 层级的正常落位。
	_ = filepath.Walk(holder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && path != holder && !strings.HasPrefix(path, out) {
			t.Errorf("导出在目标外建了目录：%s", path)
		}
		return nil
	})
}
