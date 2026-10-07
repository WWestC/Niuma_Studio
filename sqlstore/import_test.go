package sqlstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
)

// import_test.go — the JSON→SQLite bridge: shelves import with their
// counters floored past deleted high numbers, the bridge is idempotent
// (a side that already holds rows is left alone), and the file side
// is never touched (forensic copy stays). Since the promotion the
// bridge covers the whole ledger family (requirements/plan/merge/
// meeting/notice + the tasks side bridge).

func writeShelf(t *testing.T, root, domain, key, body string) {
	t.Helper()
	dir := filepath.Join(root, domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportLocalShelves(t *testing.T) {
	root := t.TempDir()
	// 需求架：next=5 高水位（r_02..r_04 已被删——号不回卷），r_01 在册
	writeShelf(t, root, "requirements", "demo",
		`{"next": 5, "reqs": [{"id":"r_01","project_key":"demo","title":"在册的","status":"open","created_ts":1}]}`)
	writeShelf(t, root, "requirements", "book",
		`{"next": 2, "reqs": [{"id":"r_01","project_key":"book","title":"书的","status":"split","created_ts":2}]}`)
	// 提案槽：p_03 在等（next 已走到 4）
	writeShelf(t, root, "plan", "demo",
		`{"next": 4, "pending": {"id":"p_03","title":"在等的","tasks":[{"title":"活"}],"submitted_ts":9}}`)
	// 合并槽：只带计数（无在等件）
	writeShelf(t, root, "merge", "demo", `{"next": 7, "pending": null}`)
	// 会议架：next=3 高水位，m_01 在史
	writeShelf(t, root, "meetings", "demo",
		`{"next": 3, "hist": [{"id":"m_01","project_key":"demo","chair":"编排者","status":"done","end_ts":5}]}`)
	// 公告：demo 一条在册
	writeShelf(t, root, "notice", "demo",
		`{"content":"过桥的公告","by":"房主","ts":1,"rev":2}`)
	// 外来文件：不读不导
	writeShelf(t, root, "requirements", "not!a!key", `{"next": 9, "reqs": []}`)

	d, err := Open(filepath.Join(t.TempDir(), "studio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rep, err := d.ImportLocal(root)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.ReqShelves != 2 || rep.Reqs != 2 || rep.Plans != 1 || rep.Merges != 1 || rep.Meetings != 1 || rep.Notices != 1 {
		t.Fatalf("导入报告不符: %+v", rep)
	}
	reqs, err := requirements.OpenStore(d.ReqShelves(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := reqs.List(""); len(got) != 2 {
		t.Fatalf("应导入两条需求: %+v", got)
	}
	// 高水位过桥：demo 架 next=5（删过的 r_02..r_04 不复生），book r_02
	if id, _ := reqs.Create("", "demo", "过桥后的新号", "", "小牛"); id.ID != "r_05" {
		t.Fatalf("需求高水位应过桥（r_05）: %s", id.ID)
	}
	if id, _ := reqs.Create("", "book", "书的第二条", "", "小苗"); id.ID != "r_02" {
		t.Fatalf("book 应从 r_02（本架最大+1）: %s", id.ID)
	}
	plans, err := plan.OpenStore(d.PlanSlots(), "")
	if err != nil {
		t.Fatal(err)
	}
	if p := plans.Pending("", "demo"); p == nil || p.ID != "p_03" {
		t.Fatalf("在等提案应过桥: %+v", p)
	}
	if next, _ := plans.Submit("", "demo", "编排者", plan.Plan{Title: "接续", Tasks: []plan.PlanTask{{Title: "活"}}}, 10); next.ID != "p_04" {
		t.Fatalf("提案计数应过桥（p_04）: %s", next.ID)
	}
	merges, err := merge.OpenStore(d.MergeSlots(), "")
	if err != nil {
		t.Fatal(err)
	}
	if next, _ := merges.Submit("", "demo", "编排者", merge.Merge{Branch: "feat", Into: "main"}, 10); next.ID != "m_07" {
		t.Fatalf("合并计数应过桥（m_07）: %s", next.ID)
	}
	meets, err := meeting.OpenStore(d.MeetingShelves(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := meets.History("demo", 0); len(got) != 1 || got[0].ID != "m_01" {
		t.Fatalf("会议历史应过桥: %+v", got)
	}
	if m, err := meets.Begin("demo", "r_01", "标题", "编排者", nil); err != nil || m.ID != "m_03" {
		t.Fatalf("会议高水位应过桥（m_03）: %+v %v", m, err)
	}
	notices := notice.OpenStore(d.NoticeRows(), "")
	if n, ok := notices.Get("demo"); !ok || n.Rev != 2 || n.Content != "过桥的公告" {
		t.Fatalf("公告应过桥: %+v %v", n, ok)
	}

	// 幂等：已导过的侧不再动（计数已推进，重导不得回卷或重复）
	if _, err := d.ImportLocal(root); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if got := reqs.List(""); len(got) != 4 {
		t.Fatalf("重导不得重复行: %+v", got)
	}
}
