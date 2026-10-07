package server

// RefileProjectMinutes（v2.9 隔离追迁）的钉：可归属的项目纪要搬到
// p/<key>/meetings/，归属不可考的（自由议题/需求已删/大厅需求）如实
// 留在大厅架；幂等重跑收敛。v2.10 号段分家后归属解析走跨架检索——
// 同号两架＝归属不可考，同样如实留大厅。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/requirements"
)

func TestRefileProjectMinutes(t *testing.T) {
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	reqs := requirements.OpenMemory()
	// 三条大厅需求（大厅架自己的 r_01–r_03）＋四条 proj-a 需求（proj-a
	// 架自己的 r_01–r_04）——号段分家后各架从 01 各自数，r_03 两架同号。
	for i := 0; i < 3; i++ {
		if _, err := reqs.Create("", "default", "Niuma_Studio 的需求", "", "房主"); err != nil {
			t.Fatalf("create default: %v", err)
		}
	}
	for i := 0; i < 4; i++ {
		if _, err := reqs.Create("", "proj-a", "甲的需求", "", "房主"); err != nil {
			t.Fatalf("create proj-a: %v", err)
		}
	}
	if r, ok := reqs.GetIn("", "default", "r_03"); !ok || r.ProjectKey != "default" {
		t.Fatalf("default 架应有自己的 r_03：%+v %v", r, ok)
	}
	if got := reqs.FindAll("", "r_03"); len(got) != 2 {
		t.Fatalf("r_03 应两架同号（归属不可考的夹具前提）：得 %d 架", len(got))
	}
	// 历史纪要：r04 唯一命中 proj-a（搬）；r03 两架同号（留）；大厅自
	// 己的会与自由议题（留）。
	for key, body := range map[string]string{
		"ops/meetings/r04-1":  "甲的独号场",
		"ops/meetings/r03-1":  "同号歧义场",
		"ops/meetings/r01-1":  "Niuma_Studio 的场（同号歧义）",
		"ops/meetings/free-1": "自由议题",
	} {
		if _, err := docs.Write(key, "内部 · 评审纪要", body, "会议归档"); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	moved := RefileProjectMinutes(docs, reqs, "")
	if len(moved) != 1 {
		t.Fatalf("应只迁 1 篇（r04 唯一归属；r03/r01 同号歧义、free 不可归属都留架），got %v", moved)
	}
	if _, err := docs.Get("p/proj-a/meetings/r04-1", 0); err != nil {
		t.Fatalf("迁移后应存在 p/proj-a/meetings/r04-1: %v", err)
	}
	if _, err := docs.Get("ops/meetings/r04-1", 0); err == nil {
		t.Fatal("原键 ops/meetings/r04-1 应已删除")
	}
	for _, stay := range []string{"ops/meetings/r03-1", "ops/meetings/r01-1", "ops/meetings/free-1"} {
		if _, err := docs.Get(stay, 0); err != nil {
			t.Fatalf("歧义/不可归属的纪要应留在 Niuma_Studio 架（%s）: %v", stay, err)
		}
	}

	// 幂等：重跑零迁移
	if again := RefileProjectMinutes(docs, reqs, ""); len(again) != 0 {
		t.Fatalf("重跑应零迁移，got %v", again)
	}
}
