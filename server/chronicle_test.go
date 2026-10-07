package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/kb"
)

// newChronicleStore builds an in-memory docs store with the chronicle
// doc pre-created (t_151's backfill established it; the hooks only
// append — the overwrite accident's lesson is baked into the design).
func newChronicleStore(t *testing.T) *kb.DocsStore {
	t.Helper()
	dir := t.TempDir()
	st, err := kb.OpenDocs(filepath.Join(dir, "docs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Write(chronicleKey, "工作室志", "# 工作室志\n\n体例见文档头部。", "测试"); err != nil {
		t.Fatal(err)
	}
	return st
}

// readChronicleOf reads one scope's journal lines (lobby or project);
// a missing journal reads as no lines (未建档即无条目).
func readChronicleOf(t *testing.T, st *kb.DocsStore, projectKey string) []string {
	t.Helper()
	doc, err := st.Get(kb.ChronicleDocKey(projectKey), 0)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(doc.Body, "\n") {
		if strings.HasPrefix(l, "202") {
			lines = append(lines, l)
		}
	}
	return lines
}

func readChronicle(t *testing.T, st *kb.DocsStore) []string {
	return readChronicleOf(t, st, "")
}

// t_152：task done 钩子——一行体例、同任务号去重、assignee 空 fallback 全室。
func TestChronicleTaskDone(t *testing.T) {
	st := newChronicleStore(t)
	c := NewChronicle(st)
	c.OnTaskDone("", "t_99", "测试任务标题", "小猿")
	c.OnTaskDone("", "t_99", "测试任务标题", "小猿") // 重复：去重
	c.OnTaskDone("", "t_98", "另一个任务", "")    // 空 assignee
	lines := readChronicle(t, st)
	if len(lines) != 2 {
		t.Fatalf("应 2 条（去重后），实际 %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "t_99") || !strings.Contains(lines[0], "交付") || !strings.Contains(lines[0], "小猿") {
		t.Fatalf("体例不符: %s", lines[0])
	}
	if !strings.Contains(lines[1], "全室") {
		t.Fatalf("空 assignee 应落全室: %s", lines[1])
	}
	if !strings.HasPrefix(lines[0], "202") || !strings.Contains(lines[0], "｜") {
		t.Fatalf("日期竖线格式: %s", lines[0])
	}
}

// t_152：req split 钩子——需求号去重。
func TestChronicleReqSplit(t *testing.T) {
	st := newChronicleStore(t)
	c := NewChronicle(st)
	c.OnReqSplit("", "r_99", "测试需求", "小牛")
	c.OnReqSplit("", "r_99", "测试需求", "小牛")
	lines := readChronicle(t, st)
	if len(lines) != 1 {
		t.Fatalf("应 1 条，实际 %d", len(lines))
	}
	if !strings.Contains(lines[0], "立顶") || !strings.Contains(lines[0], "r_99") {
		t.Fatalf("立顶体例: %s", lines[0])
	}
}

// t_152：去重环容量——第 101 个键不挤掉仍在环内的近期键判定。
func TestChronicleRingCap(t *testing.T) {
	st := newChronicleStore(t)
	c := NewChronicle(st)
	for i := 0; i < 105; i++ {
		c.dedup(string(rune('a'+i%26)) + ":" + string(rune('0'+i/26)) + ":" + string(rune('0'+i%10)) + "x")
	}
	// 环只留最近 100：最早的 5 个键已被挤出，重放应视为首次
	if !c.dedup("a:0:0x") {
		t.Fatal("环挤出后的旧键重放应再次放行")
	}
}

// t_152：nil 安全——nil docs 或 nil receiver 全静默不炸。
func TestChronicleNilSafe(t *testing.T) {
	c := NewChronicle(nil)
	c.OnTaskDone("book", "t_1", "x", "y") // docs nil：append 静默
	var nilC *Chronicle
	nilC.OnTaskDone("book", "t_1", "x", "y") // receiver nil：不炸即过
}

// t_152：60 字帽。
func TestChronicleClamp60(t *testing.T) {
	long := strings.Repeat("很长的标题", 30) // 150 字
	st := newChronicleStore(t)
	c := NewChronicle(st)
	c.OnTaskDone("", "t_97", long, "小猿")
	lines := readChronicle(t, st)
	// 行里标题段 ≤60 字（体例帽）
	if len(lines) != 1 {
		t.Fatalf("应 1 条: %v", lines)
	}
	if i := strings.Index(lines[0], "「"); i >= 0 {
		j := strings.Index(lines[0], "」")
		seg := lines[0][i+3 : j] // 「 是 3 字节 UTF-8
		if len([]rune(seg)) > 60 {
			t.Fatalf("标题超 60 帽: %d 字", len([]rune(seg)))
		}
	}
}

// 按办公室隔离：项目事件落 p/<key>/chronicle，大厅事件落 ops/chronicle，
// 两本志互不渗漏（同源去重环是全局的——同任务号跨房也不重录）。
func TestChroniclePerProjectIsolation(t *testing.T) {
	st := newChronicleStore(t) // 只预建大厅的 ops/chronicle
	c := NewChronicle(st)
	c.OnTaskDone("book", "t_201", "小说第一章", "小鹿")
	c.OnReqSplit("book", "r_77", "写一本小说", "房主")
	c.OnTaskDone("", "t_202", "Niuma_Studio 的活", "小猿")
	c.OnTaskDone("default", "t_203", "显式 Niuma_Studio 键", "小猿")

	lobby := readChronicle(t, st)
	if len(lobby) != 2 {
		t.Fatalf("Niuma_Studio 志应 2 条，实际 %d: %v", len(lobby), lobby)
	}
	if !strings.Contains(lobby[0], "t_202") || !strings.Contains(lobby[1], "t_203") {
		t.Fatalf("Niuma_Studio 志进了别家的行: %v", lobby)
	}
	proj := readChronicleOf(t, st, "book")
	if len(proj) != 2 {
		t.Fatalf("项目志应 2 条，实际 %d: %v", len(proj), proj)
	}
	if !strings.Contains(proj[0], "t_201") || !strings.Contains(proj[1], "r_77") {
		t.Fatalf("项目志条目不符: %v", proj)
	}
	// 跨房同任务号：全局去重环认得它，第二房不重录
	c.OnTaskDone("lobby2", "t_201", "同号他房", "小鹿")
	if got := readChronicleOf(t, st, "lobby2"); len(got) != 0 {
		t.Fatalf("同任务号跨房应被去重环拦下: %v", got)
	}
}

// 项目志缺席自动建档（room-log 同款仪式）：第一行即建档——大厅的
// ops/chronicle 只有跑过 t_151 回溯的库才有，项目志全靠首行落地。
func TestChronicleBirthsMissingDoc(t *testing.T) {
	dir := t.TempDir()
	st, err := kb.OpenDocs(filepath.Join(dir, "d"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, "d", "ops__chronicle.md"))
	c := NewChronicle(st)
	c.OnTaskDone("", "t_1", "x", "y")       // 大厅志缺席——首行建档
	c.OnTaskDone("book", "t_2", "小说", "小鹿") // 项目志缺席——首行建档
	for _, pk := range []string{"", "book"} {
		lines := readChronicleOf(t, st, pk)
		if len(lines) != 1 {
			t.Fatalf("scope %q 应建档 1 条，实际 %d", pk, len(lines))
		}
	}
	// 第二行起走追加（不再覆盖——t_151 事故的红线）
	c.OnTaskDone("", "t_3", "y", "z")
	if got := len(readChronicleOf(t, st, "")); got != 2 {
		t.Fatalf("建档后应正常追加，实际 %d 条", got)
	}
}
