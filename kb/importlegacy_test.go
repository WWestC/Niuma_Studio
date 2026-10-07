// importlegacy_test.go — pins the one-shot ~/.niuma_kb → ~/.niuma/kb
// migration: bodies/titles/revs/history tails cross over, the tree
// retires to a .imported-<ts> backup, and the imported store behaves
// like a native one (revs continue from the imported chain).
package kb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildLegacyTree fabricates a pre-v3 store: md files, an
// .index.json, and a .history chain for one doc.
func buildLegacyTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, docsHistoryDir), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("design/r01-scene.md", "# 场景基线\n\n茶水间在东墙。")
	write("ops/chronicle.md", "# 工作室志\n\n2026-10-01｜里程碑｜全室｜r_01 立顶。")
	write("roles/hr.md", "# HR 工作手册\n\n（手编版）")

	// the chain: rev 1 and 2 replaced; index says the file is rev 3
	chain := []HistoryEntry{
		{Rev: 1, By: "seed", TS: 100, Body: "# 工作室志\n"},
		{Rev: 2, By: "小猿", TS: 200, Body: "# 工作室志\n\n2026-10-01｜里程碑｜全室｜r_01"},
	}
	var b strings.Builder
	for _, h := range chain {
		j, _ := json.Marshal(h)
		b.Write(j)
		b.WriteByte('\n')
	}
	write(filepath.Join(docsHistoryDir, "ops__chronicle.jsonl"), b.String())

	idx := map[string]DocMeta{
		"ops/chronicle": {Key: "ops/chronicle", Title: "工作室志", Owner: "seed",
			UpdatedBy: "小猿", UpdatedTS: 300, Rev: 3, Size: 40},
		"roles/hr": {Key: "roles/hr", Title: "HR 工作手册", Owner: "seed", Rev: 2, UpdatedTS: 150},
	}
	ib, _ := json.MarshalIndent(idx, "", "  ")
	write(docsIndexFile, string(ib))
	return dir
}

func TestImportLegacyCarriesStateAndRetiresTree(t *testing.T) {
	legacy := buildLegacyTree(t)
	newDir := filepath.Join(t.TempDir(), "kb")

	n, backup, err := ImportLegacy(newDir, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("应导入 3 篇, got %d", n)
	}
	if backup == "" || !strings.HasPrefix(backup, legacy+".imported-") {
		t.Fatalf("旧树应改名留底: %q", backup)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("原路径应已让位")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("留底应存在: %v", err)
	}

	s, err := OpenDocs(newDir)
	if err != nil {
		t.Fatal(err)
	}
	// indexed meta crosses over verbatim
	d, err := s.Get("ops/chronicle", 0)
	if err != nil || d.Title != "工作室志" || d.Rev != 3 || d.Owner != "seed" || d.UpdatedBy != "小猿" {
		t.Fatalf("索引元数据应原样过户: (%+v, %v)", d.DocMeta, err)
	}
	// the history chain rides along: content at rev 2 = entry 3… but
	// the tail holds revs 1–2, so rev 1 reads entry 2's body
	d1, err := s.Get("ops/chronicle", 1)
	if err != nil || !strings.Contains(d1.Body, "r_01") || d1.Body == d.Body {
		t.Fatalf("历史链应过户且语义不变: (%q, %v)", d1.Body, err)
	}
	// a doc missing from the index adopts synthesized metadata
	h, err := s.Get("design/r01-scene", 0)
	if err != nil || h.Title != "场景基线" || h.Owner != "(导入)" || h.Rev != 1 {
		t.Fatalf("无索引文档应合成元数据: (%+v, %v)", h.DocMeta, err)
	}
	// writing after import continues the imported rev
	m, err := s.Append("ops/chronicle", "2026-10-02｜交付｜小狐｜导入后第一笔", "小狐")
	if err != nil || m.Rev != 4 {
		t.Fatalf("导入后写入应续 rev: rev=%d err=%v", m.Rev, err)
	}
}

func TestImportLegacyRefusesWhenStoreExists(t *testing.T) {
	legacy := buildLegacyTree(t)
	newDir := t.TempDir()
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportLegacy(newDir, legacy); err == nil {
		t.Fatal("目标库已存在时应拒绝导入")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("被拒时旧树应原位不动")
	}
}

func TestImportLegacyRerunRefused(t *testing.T) {
	legacy := buildLegacyTree(t)
	newDir := filepath.Join(t.TempDir(), "kb")
	n, backup, err := ImportLegacy(newDir, legacy)
	if err != nil || n != 3 || backup == "" {
		t.Fatalf("首次导入应成功: n=%d backup=%q err=%v", n, backup, err)
	}
	// running it again finds a standing store and refuses
	if _, _, err := ImportLegacy(newDir, backup); err == nil {
		t.Fatal("库已存在时再跑应拒绝")
	}
}

// TestReconcileLegacyAccidentalSeedWindow pins the mid-upgrade-relaunch
// repair: a standing store that seeded fresh (new engine, pre-migration
// wiring) while the real tree waited. The window's own writes — a
// ledger line on ops/room-log, a brand-new doc — must survive; the
// tree's history must come home; pristine seeds must yield to the tree.
func TestReconcileLegacyAccidentalSeedWindow(t *testing.T) {
	legacy := buildLegacyTree(t)
	// the tree also holds the room-log with real history
	if err := os.WriteFile(filepath.Join(legacy, "ops/room-log.md"),
		[]byte("# 办公室日志\n\n2026-10-01｜上岗｜小牛｜到岗\n2026-10-01｜上岗｜小马｜到岗\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// the accidental store: fresh seeds, then the window's own writes
	dir := filepath.Join(t.TempDir(), "kb")
	s, err := OpenDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("ops/room-log", "小鹿 已上岗（岗位 设计，启动招聘，2026-10-02）", "调度"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("design/r18-achievements", "成就墙", "# 成就墙\n（窗口期新稿）", "小鹿"); err != nil {
		t.Fatal(err)
	}

	imported, merged, kept, err := ReconcileLegacy(s, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if imported != 3 || merged != 1 || kept != 0 {
		t.Fatalf("账目不符: imported=%d merged=%d kept=%d", imported, merged, kept)
	}

	// the tree's chronicle comes home whole (rev from the legacy index)
	c, err := s.Get("ops/chronicle", 0)
	if err != nil || c.Rev != 3 || !strings.Contains(c.Body, "r_01 立顶") {
		t.Fatalf("编年史应整篇回家: rev=%d err=%v", c.Rev, err)
	}
	// the ledger: tree history + the window's own tail, no line dropped
	lg, err := s.Get("ops/room-log", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"小牛｜到岗", "小马｜到岗", "小鹿 已上岗"} {
		if !strings.Contains(lg.Body, want) {
			t.Fatalf("台账并卷丢行（缺 %s）: %q", want, lg.Body)
		}
	}
	// pristine seed yields to the tree's real manual
	hr, err := s.Get("roles/hr", 0)
	if err != nil || !strings.Contains(hr.Body, "（手编版）") || hr.Rev != 2 {
		t.Fatalf("种子版手册应让位旧树: rev=%d err=%v", hr.Rev, err)
	}
	// the window's own doc stands
	r18, err := s.Get("design/r18-achievements", 0)
	if err != nil || !strings.Contains(r18.Body, "窗口期新稿") {
		t.Fatalf("窗口期新稿必须保留: %v", err)
	}

	// and the merged state survives a reopen (the compact persisted it)
	s2 := reopen(t, s)
	c2, err := s2.Get("ops/chronicle", 0)
	if err != nil || c2.Rev != 3 {
		t.Fatalf("重开应保住合并态: rev=%d err=%v", c2.Rev, err)
	}
}
