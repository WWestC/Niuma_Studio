package chat

// term_test.go — 终端转录（r_17）的折叠/落盘/重放/清理契约：
//   1. 折叠语义与 AppendTrace 同构：工具按 Call 复用 seq 原位合并、
//      think 同流尾并、流断开新 seq、input/ask/sys 各自新 seq；
//   2. 落盘与重启续册：新 hub 挂同一目录，seq 从文件尾地板（不回卷）、
//      跨重启的工具回填仍并入原卡（重放终态＝所见）；
//   3. 保留期：PruneTermFile 按行 ts 保新写回；
//   4. 叶子折叠：斜杠等不安全字符下划线化。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendTermFoldSemantics(t *testing.T) {
	h := NewHub()
	ok := true
	// 工具生命周期：三态一个 seq，终态带全量卡。
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceTool, Call: "c1", Tool: "Bash", State: "call"}})
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceTool, Call: "c1", State: "run"}})
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceTool, Call: "c1", State: "done", Output: "ok", OK: &ok}})
	// 思考流：两次增量并成一条；草稿是另一条流，新 seq。
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceThink, Text: "abc"}})
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceThink, Text: "def"}})
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceDraft, Text: "回话"}})
	// 输入行：两条各自立行（不折叠）。
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceInput, Src: "房主", Text: "第一令"}})
	h.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceInput, Text: "第二令"}})

	rows := h.TermSnapshot("小猿", 100)["小猿"]
	if len(rows) != 5 {
		t.Fatalf("折叠后应 5 条（工具1+思考1+草稿1+输入2），得 %d", len(rows))
	}
	tool := rows[0]
	if tool.Kind != TraceTool || tool.Seq != rows[0].Seq || tool.Tool != "Bash" ||
		tool.State != "done" || tool.Output != "ok" || tool.OK == nil || !*tool.OK {
		t.Fatalf("工具终态卡不符：%+v", tool)
	}
	if rows[1].Kind != TraceThink || rows[1].Text != "abcdef" {
		t.Fatalf("思考尾并不符：%+v", rows[1])
	}
	if rows[2].Kind != TraceDraft || rows[2].Text != "回话" {
		t.Fatalf("草稿不符：%+v", rows[2])
	}
	if rows[3].Src != "房主" || rows[4].Src != "" {
		t.Fatalf("input 行 src 不符：%+v %+v", rows[3], rows[4])
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("seq 非单调：%d %d", rows[i-1].Seq, rows[i].Seq)
		}
	}
	// ?name= 收窄：他人一行不见。
	if _, hit := h.TermSnapshot("小鹿", 10)["小猿"]; hit {
		t.Fatal("聚焦查询不该捎带他人")
	}
}

func TestAppendTermPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	h1 := NewHub()
	h1.SetTermDir(dir)
	h1.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceTool, Call: "c1", Tool: "Bash", State: "call"}})
	h1.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceInput, Src: "房主", Text: "跑一下"}})
	h1.FlushTerm() // 落盘已异步化（termSave flusher）：重启读文件前先排干——与聊天历史的家法同款

	h2 := NewHub()
	h2.SetTermDir(dir) // 重启：文件尾扫出 seq 地板与折叠基准
	// 跨重启的工具回填：并入原卡（Tool 名继承自文件里的首建行）。
	ok := true
	h2.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceTool, Call: "c1", State: "done", Output: "done-out", OK: &ok}})
	h2.AppendTerm([]TraceEntry{{From: "小猿", Kind: TraceThink, Text: "新回合"}})
	h2.FlushTerm()

	rows := h2.TermSnapshot("", 100)["小猿"]
	if len(rows) != 3 {
		t.Fatalf("重启后应 3 条（输入+工具终态+新思考），得 %d：%+v", len(rows), rows)
	}
	var tool TraceEntry
	for _, r := range rows {
		if r.Kind == TraceTool {
			tool = r
		}
	}
	if tool.Tool != "Bash" || tool.State != "done" || tool.Output != "done-out" {
		t.Fatalf("跨重启回填不符：%+v", tool)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("重启后 seq 回卷：%+v", rows)
		}
	}
	// 文件里确实逐行落了账（append-only：状态迁移的中间行也在）。
	b, err := os.ReadFile(filepath.Join(dir, "小猿.jsonl"))
	if err != nil || !strings.Contains(string(b), "跑一下") || !strings.Contains(string(b), "call") {
		t.Fatalf("转录文件内容不符：%v %q", err, string(b))
	}
}

func TestPruneTermFileKeepsRecent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "小猿.jsonl")
	old := `{"seq":1,"ts":100,"from":"小猿","kind":"input","text":"旧令"}` + "\n" +
		`{"seq":2,"ts":9999999999,"from":"小猿","kind":"think","text":"新思"}` + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if dropped := PruneTermFile(path, 1000); dropped != 1 {
		t.Fatalf("应清 1 条，得 %d", dropped)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "旧令") || !strings.Contains(string(b), "新思") {
		t.Fatalf("清理写回不符：%q", string(b))
	}
	// 幂等：再扫不动。
	if dropped := PruneTermFile(path, 1000); dropped != 0 {
		t.Fatalf("幂等失败：%d", dropped)
	}
}

func TestTermFileLeafSanitizes(t *testing.T) {
	if got := TermFileLeaf("a/b\\c:d"); got != "a_b_c_d.jsonl" {
		t.Fatalf("斜杠折叠不符：%s", got)
	}
	if got := TermFileLeaf(""); got != "_.jsonl" {
		t.Fatalf("空名兜底不符：%s", got)
	}
	if got := TermFileLeaf("小明"); got != "小明.jsonl" {
		t.Fatalf("中文名应原样：%s", got)
	}
}
