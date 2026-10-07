// db_test.go — pins the log-structured engine: WAL replay, torn-tail
// tolerance, the governance lifecycle (archive/unarchive/delete),
// ledger rollover volumes, and the explicit body-limit refusals that
// replaced the file store's silent clamps.
package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openDB(t *testing.T) *DocsStore {
	t.Helper()
	// a NOT-yet-existing subdir: OpenDocs seeds only a missing dir
	// (an existing one is never reseeded — the v0.5 discipline)
	s, err := OpenDocs(filepath.Join(t.TempDir(), "kb"))
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	return s
}

// reopen closes-and-reopens the same dir (a crash-free restart).
func reopen(t *testing.T, s *DocsStore) *DocsStore {
	t.Helper()
	dir := s.dir
	s2, err := OpenDocs(dir)
	if err != nil {
		t.Fatalf("reopen OpenDocs: %v", err)
	}
	return s2
}

func TestDBReplayRestoresState(t *testing.T) {
	s := openDB(t)
	if _, err := s.Write("design/a", "甲", "第一版", "小鹿"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("design/a", "补一行", "小猿"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive("roles/hr", "房主"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("ops/tmp", "临时", "用完即删", "小狐"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ops/tmp", "房主"); err != nil {
		t.Fatal(err)
	}

	s2 := reopen(t, s)
	d, err := s2.Get("design/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "第一版\n补一行\n"
	if d.Body != want {
		t.Fatalf("重放后正文 = %q, want %q", d.Body, want)
	}
	if d.Rev != 2 {
		t.Fatalf("重放后 rev = %d, want 2", d.Rev)
	}
	// rev 1 的内容 = 2 号历史条目（v0.5 契约）
	d1, err := s2.Get("design/a", 1)
	if err != nil || d1.Body != "第一版" {
		t.Fatalf("Get(rev=1) = (%q, %v), want 第一版", d1.Body, err)
	}
	if _, ok := metaList(s2.ListOpt(ListOptions{})).lenHas("roles/hr"); ok {
		t.Fatal("归档位没随重放恢复")
	}
	hr, err := s2.Get("roles/hr", 0)
	if err != nil || !hr.Archived {
		t.Fatal("归档文档应仍可读且带 archived 位")
	}
	if _, err := s2.Get("ops/tmp", 0); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("已删除文档应 404，得到 %v", err)
	}
}

// lenHas: tiny helper so the assertion above reads like a check.
type metaList []DocMeta

func (m metaList) lenHas(key string) (int, bool) {
	for i, x := range m {
		if x.Key == key {
			return i, true
		}
	}
	return 0, false
}

func TestDBTornTailLineIsSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kb")
	s, err := OpenDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write("ops/x", "账", "种子行", "seed"); err != nil {
		t.Fatal(err)
	}
	// a crash mid-append: half a JSON line, then (bizarrely) nothing
	f, err := os.OpenFile(filepath.Join(dir, dbLogFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"seq":99,"op":"wri`)
	f.Close()

	s2, err := OpenDocs(dir)
	if err != nil {
		t.Fatalf("撕裂尾行不应拒绝启动: %v", err)
	}
	d, err := s2.Get("ops/x", 0)
	if err != nil || d.Body != "种子行" {
		t.Fatalf("撕裂行前的状态应完好: (%q, %v)", d.Body, err)
	}
	// the store keeps writing past the scar
	if _, err := s2.Append("ops/x", "伤后一行", "小猿"); err != nil {
		t.Fatal(err)
	}
	s3 := reopen(t, s2)
	d, _ = s3.Get("ops/x", 0)
	if d.Body != "种子行\n伤后一行\n" {
		t.Fatalf("伤后写入应照常重放: %q", d.Body)
	}
}

func TestDBFoldedLogLineIsNoOp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kb")
	s, _ := OpenDocs(dir)
	s.Write("a", "甲", "一", "x")
	log := filepath.Join(dir, dbLogFile)
	b, _ := os.ReadFile(log)
	// duplicate an already-applied line (the compact crash window)
	f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write(b)
	f.Close()
	s2, _ := OpenDocs(dir)
	d, _ := s2.Get("a", 0)
	if d.Rev != 1 {
		t.Fatalf("已折叠行重放应为 no-op, rev=%d want 1", d.Rev)
	}
}

func TestDBBodyLimitRefusesInsteadOfClamping(t *testing.T) {
	s := openDB(t)
	fat := strings.Repeat("大", 33*1024) // 99KB > 64KB
	if _, err := s.Write("design/fat", "肥稿", fat, "小鹿"); err == nil {
		t.Fatal("超限 Write 应拒绝")
	} else {
		var ble *BodyLimitError
		if !errors.As(err, &ble) || ble.Key != "design/fat" {
			t.Fatalf("应返回 *BodyLimitError, got %T(%v)", err, err)
		}
	}
	// append: fill to the cliff, then assert the refusal leaves the
	// body byte-identical (the old store silently truncated)
	s.Write("ops/ledger", "账", strings.Repeat("a", DocMaxBody-100), "seed")
	if _, err := s.Append("ops/ledger", strings.Repeat("b", 200), "小狐"); err == nil {
		t.Fatal("超限 Append 应拒绝")
	}
	d, _ := s.Get("ops/ledger", 0)
	if len(d.Body) != DocMaxBody-100 || strings.Contains(d.Body, "bbb") {
		t.Fatalf("拒绝后正文应原样 (%d 字节)", len(d.Body))
	}
}

func TestDBRolloverVolumes(t *testing.T) {
	s := openDB(t)
	s.Write("ops/chronicle", "工作室志", "# 工作室志\n\n体例说明。\n", "seed")
	line := strings.Repeat("事", 200) // 600 bytes a line
	const volMax = 4000
	// in-limit appends ride the plain path (seed ~30B + 6×601B < 4000)
	for i := 0; i < 6; i++ {
		if _, err := s.AppendRollover("ops/chronicle", line, "chronicle", volMax); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := s.Get("ops/chronicle", 0)
	if len(d.Body) < 3000 || len(d.Body) > volMax {
		t.Fatalf("卷内正文应在上限内: %d", len(d.Body))
	}
	preRev := d.Rev
	preBody := d.Body
	// the overflow: the live body lands whole in vol-001 (archived),
	// the live doc restarts with the new line
	if _, err := s.AppendRollover("ops/chronicle", line, "chronicle", volMax); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get("ops/chronicle", 0)
	if d.Body != line+"\n" {
		t.Fatalf("溢出后活卷应以新行重启: %d 字节", len(d.Body))
	}
	if d.Rev != preRev+1 {
		t.Fatalf("活卷 rev 应继续爬升: %d want %d", d.Rev, preRev+1)
	}
	vol, err := s.Get("ops/chronicle-vol-001", 0)
	if err != nil || vol.Body != preBody || !vol.Archived {
		t.Fatalf("vol-001 应归档且装下整卷旧正文: (%d 字节, archived=%v, %v)", len(vol.Body), vol.Archived, err)
	}
	if !strings.Contains(vol.Title, "第1卷") {
		t.Fatalf("卷标题应带卷号: %q", vol.Title)
	}
	// the shelf: List hides it, ListOpt{Archived} shows it, and a
	// second rollover numbers forward
	if _, ok := metaList(s.List()).lenHas("ops/chronicle-vol-001"); ok {
		t.Fatal("卷不应进默认列表")
	}
	if _, ok := metaList(s.ListOpt(ListOptions{Archived: true})).lenHas("ops/chronicle-vol-001"); !ok {
		t.Fatal("卷应在归档架上")
	}
	for i := 0; i < 30; i++ {
		if _, err := s.AppendRollover("ops/chronicle", line, "chronicle", volMax); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Get("ops/chronicle-vol-002", 0); err != nil {
		t.Fatalf("第二卷应顺号: %v", err)
	}
	// replay reproduces the volume set exactly
	s2 := reopen(t, s)
	for _, key := range []string{"ops/chronicle", "ops/chronicle-vol-001", "ops/chronicle-vol-002"} {
		a, e1 := s.Get(key, 0)
		b2, e2 := s2.Get(key, 0)
		if e1 != nil || e2 != nil || a.Body != b2.Body || a.Rev != b2.Rev || a.Archived != b2.Archived {
			t.Fatalf("重放失真 %s: (%v/%v)", key, e1, e2)
		}
	}
}

func TestDBArchiveLifecycleAndCap(t *testing.T) {
	s := openDB(t)
	// a fresh store already seats 3 seed docs; fill the active shelf
	// to the cap on top of them
	bulk := DocMaxCount - 3
	for i := 0; i < bulk; i++ {
		key := "bulk/" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if _, err := s.Write(key, "占位", "x", "bulk"); err != nil {
			t.Fatalf("第 %d 篇应可写: %v", i+1, err)
		}
	}
	if _, err := s.Write("bulk/overflow", "超编", "x", "bulk"); err == nil {
		t.Fatal("在编满 200 后新建应被拒")
	}
	if _, err := s.Archive("bulk/aa", "房主"); err != nil {
		t.Fatal(err)
	}
	if got := s.Count(); got != DocMaxCount-1 {
		t.Fatalf("归档应离开在编计数: %d", got)
	}
	if _, err := s.Write("bulk/after-archive", "补位", "x", "bulk"); err != nil {
		t.Fatalf("归档腾出的名额应可用: %v", err)
	}
	// the shelf is full again — unarchive must refuse
	if _, err := s.Unarchive("bulk/aa", "房主"); err == nil {
		t.Fatal("在编已满时恢复归档应被拒")
	}
	if err := s.Delete("bulk/after-archive", "房主"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unarchive("bulk/aa", "房主"); err != nil {
		t.Fatalf("删除腾位后恢复应放行: %v", err)
	}
	d, err := s.Get("bulk/aa", 0)
	if err != nil || d.Archived {
		t.Fatal("恢复后应在编")
	}
	// archive does not touch rev — expect_rev anchors hold
	if d.Rev != 1 {
		t.Fatalf("归档/恢复不动 rev: %d", d.Rev)
	}
}

func TestDBDeleteThenRecreateStartsFresh(t *testing.T) {
	s := openDB(t)
	s.Write("ops/gone", "删了", "v1", "x")
	s.Write("ops/gone", "删了", "v2", "x")
	if err := s.Delete("ops/gone", "房主"); err != nil {
		t.Fatal(err)
	}
	m, err := s.Write("ops/gone", "重建", "fresh", "y")
	if err != nil || m.Rev != 1 {
		t.Fatalf("重建应从 rev 1 重新开始: rev=%d err=%v", m.Rev, err)
	}
}

func TestDBListOptFilters(t *testing.T) {
	s := openDB(t)
	s.Write("design/r1", "场景", "…", "小鹿")
	s.Write("design/r2", "表现", "…", "小鹿")
	s.Write("ops/log", "日志", "…", "seed")
	s.Archive("design/r2", "房主")

	if got := len(s.ListOpt(ListOptions{Prefix: "design/"})); got != 1 {
		t.Fatalf("design/ 在编应剩 1: %d", got)
	}
	if got := len(s.ListOpt(ListOptions{Prefix: "design/", Archived: true})); got != 1 {
		t.Fatalf("design/ 归档架应有 1: %d", got)
	}
	if got := len(s.ListOpt(ListOptions{Q: "表现"})); got != 0 {
		t.Fatalf("搜索不应漏进归档: %d", got)
	}
	if got := len(s.ListOpt(ListOptions{Q: "表现", Archived: true})); got != 1 {
		t.Fatalf("归档架搜索应命中: %d", got)
	}
}

func TestDBExportTree(t *testing.T) {
	s := openDB(t)
	s.Write("design/r1", "场景", "# 场景", "小鹿")
	s.Write("roles/hr2", "手册", "# 手册", "seed")
	s.Archive("roles/hr2", "房主")
	out := t.TempDir()
	files, err := s.Export(out)
	if err != nil {
		t.Fatal(err)
	}
	var sawDoc, sawIndex bool
	for _, f := range files {
		if f == "design/r1.md" {
			sawDoc = true
		}
		if f == "_index.md" {
			sawIndex = true
		}
	}
	if !sawDoc || !sawIndex {
		t.Fatalf("导出树缺件: %v", files)
	}
	idx, err := os.ReadFile(filepath.Join(out, "_index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "roles/hr2") || !strings.Contains(string(idx), "是") {
		t.Fatal("清单应标出归档文档")
	}
}
