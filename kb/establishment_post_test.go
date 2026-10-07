package kb

// EnsureEstablishmentPost tests (v2 post self-registration): the four
// contracts the orchestrator's birth rides on — a missing table is
// created under the frozen header, an existing table gains the row
// INSIDE the table (trailing prose untouched) exactly once (idempotent
// rebirth/recall never bumps the rev), a broken table is refused with
// the store untouched, and a host-written row under the same 岗位 key
// is never rewritten.

import (
	"strings"
	"testing"
)

// openPostDocs starts a seeded store in a temp dir (the v2.2 starter
// docs land; the p/<key>/establishment keys under test never
// collide with them).
func openPostDocs(t *testing.T) *DocsStore {
	t.Helper()
	docs, err := OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	return docs
}

// userTable is a host-authored project table with one be row and
// trailing prose — the append path must thread between them.
const userTable = "# 编制表（房主手写）\n\n" +
	"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
	"|---|---|---|---|---|---|\n" +
	"| be | 后端 | 后端 | 2 | 否 | roles/be |\n\n" +
	"（尾注：表格后面还有房主写的正文）\n"

func TestEnsurePostCreatesMissingTable(t *testing.T) {
	docs := openPostDocs(t)
	created, err := EnsureEstablishmentPost(docs, "p/proj-x/establishment",
		"orchestrator", "编排者", "排期编排", 1, true, "", true, "调度")
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if !created {
		t.Fatal("缺表应建表（created=true）")
	}
	doc, err := docs.Get("p/proj-x/establishment", 0)
	if err != nil {
		t.Fatalf("建表后不可读：%v", err)
	}
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" {
		t.Fatalf("新建表解析失败：%s", perr)
	}
	if len(rows) != 1 {
		t.Fatalf("新建表应恰有一行，得 %d", len(rows))
	}
	r := rows[0]
	if r.Key != "orchestrator" || r.Name != "编排者" || r.Role != "排期编排" ||
		r.Headcount != 1 || !r.AutoFill || r.Manual != "" || !r.Must {
		t.Fatalf("登记行字段不符：%+v", r)
	}
	if doc.Title != "编制表" {
		t.Fatalf("新建表标题应为「编制表」，得 %q", doc.Title)
	}
}

func TestEnsurePostAppendsInsideExistingTable(t *testing.T) {
	docs := openPostDocs(t)
	if _, err := docs.Write("p/proj-x/establishment", "编制表", userTable, "房主"); err != nil {
		t.Fatalf("预置房主表：%v", err)
	}
	created, err := EnsureEstablishmentPost(docs, "p/proj-x/establishment",
		"orchestrator", "编排者", "排期编排", 1, true, "", true, "调度")
	if err != nil || !created {
		t.Fatalf("追加失败：created=%v err=%v", created, err)
	}
	doc, _ := docs.Get("p/proj-x/establishment", 0)
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" || len(rows) != 2 {
		t.Fatalf("追加后应有两行（perr=%q rows=%d）", perr, len(rows))
	}
	if rows[0].Key != "be" || rows[1].Key != "orchestrator" {
		t.Fatalf("原行应在前、新行在后：%+v", rows)
	}
	// placement: the row lands after the last table row and before the
	// trailing prose — markdown renders the table contiguously
	iBe, iNew, iProse := strings.Index(doc.Body, "| be |"),
		strings.Index(doc.Body, "| orchestrator |"), strings.Index(doc.Body, "尾注")
	if !(iBe < iNew && iNew < iProse) {
		t.Fatalf("新行应插在表内（be=%d new=%d 尾注=%d）\n%s", iBe, iNew, iProse, doc.Body)
	}
	if strings.Count(doc.Body, "尾注") != 1 {
		t.Fatal("尾注正文被改动")
	}
	// idempotent: the second call (rebirth/recall) neither writes nor
	// bumps the rev
	rev := doc.Rev
	created2, err2 := EnsureEstablishmentPost(docs, "p/proj-x/establishment",
		"orchestrator", "编排者", "排期编排", 1, true, "", true, "调度")
	if err2 != nil || created2 {
		t.Fatalf("重复登记应为静默 no-op：created=%v err=%v", created2, err2)
	}
	if doc2, _ := docs.Get("p/proj-x/establishment", 0); doc2.Rev != rev {
		t.Fatalf("no-op 不应加版本：rev %d → %d", rev, doc2.Rev)
	}
}

func TestEnsurePostRefusesBrokenTable(t *testing.T) {
	docs := openPostDocs(t)
	broken := "碎表：没有冻结表头\n| be | 后端 | 后端 | 2 | 否 |\n"
	if _, err := docs.Write("p/proj-x/establishment", "编制表", broken, "房主"); err != nil {
		t.Fatalf("预置坏表：%v", err)
	}
	before, _ := docs.Get("p/proj-x/establishment", 0)
	created, err := EnsureEstablishmentPost(docs, "p/proj-x/establishment",
		"orchestrator", "编排者", "排期编排", 1, true, "", true, "调度")
	if created || err == nil {
		t.Fatalf("坏表应拒绝：created=%v err=%v", created, err)
	}
	if !strings.Contains(err.Error(), "拒绝") {
		t.Fatalf("拒绝文案应言明拒绝：%v", err)
	}
	after, _ := docs.Get("p/proj-x/establishment", 0)
	if after.Rev != before.Rev || after.Body != before.Body {
		t.Fatal("坏表必须原样保留——绝不猜测改动")
	}
}

func TestEnsurePostNeverRewritesHostRow(t *testing.T) {
	docs := openPostDocs(t)
	hostTable := "# 编制表\n\n| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
		"|---|---|---|---|---|---|\n" +
		"| orchestrator | 编排者 | 排期编排 | 2 | 否 | roles/orch |\n"
	if _, err := docs.Write("p/proj-x/establishment", "编制表", hostTable, "房主"); err != nil {
		t.Fatalf("预置房主行：%v", err)
	}
	created, err := EnsureEstablishmentPost(docs, "p/proj-x/establishment",
		"orchestrator", "编排者", "排期编排", 1, true, "", true, "调度")
	if err != nil || created {
		t.Fatalf("既有岗位行应为 no-op：created=%v err=%v", created, err)
	}
	doc, _ := docs.Get("p/proj-x/establishment", 0)
	rows, perr := EstablishmentRows(doc.Body)
	if perr != "" || len(rows) != 1 || rows[0].Headcount != 2 || rows[0].AutoFill || rows[0].Manual != "roles/orch" {
		t.Fatalf("房主的手写行被改写了：%+v（perr=%q）", rows, perr)
	}
}
