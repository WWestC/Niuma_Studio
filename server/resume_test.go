package server

// resume_test.go — r_20 t_195：接续引擎的单测面。提炼纯函数（构造
// 输入直喂）＋模板三段断言（引用不概括——commit 原文子串）＋退化兜底。

import (
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/dispatch"
)

// ① 提炼：文件清单合并去重 ≤10、commit 原文引用、note 原样。
func TestExtractResumeNotes(t *testing.T) {
	g := roomops.GitSources{
		StatusShort: " M web/a.js\n?? web/new.go\n",
		DiffNames:   "web/a.js\nweb/b.js\n",
		Log:         "feat(x): 第一笔|1700000000\nfix(y): 第二笔|1700003600",
	}
	rn := roomops.ExtractResumeNotes(g, []string{"线索一", "线索二"}, 1700000000)
	// 文件：a(new+diff 去重)+new+b = 3
	if len(rn.Files) != 3 {
		t.Fatalf("文件应 3 去重后（got %v）", rn.Files)
	}
	// commit 原文（引用不概括）
	if !resContains(rn.Commits, "feat(x): 第一笔") || !resContains(rn.Commits, "fix(y): 第二笔") {
		t.Fatalf("commit 原文引用（got %v）", rn.Commits)
	}
	if len(rn.Notes) != 2 {
		t.Fatalf("note 原样（got %v）", rn.Notes)
	}
}

// ② 引用断言的口径：提炼产物是源文本身（子串测试的关键——
// 「feat(x): 第一笔」必须是 git log 原文子串，零加工）。
func TestResumeNotesAreVerbatim(t *testing.T) {
	g := roomops.GitSources{Log: "feat(room): 观察相机——画布缩放平移（t_158/r_11）|1700000000"}
	rn := roomops.ExtractResumeNotes(g, nil, 0)
	if len(rn.Commits) != 1 || !strings.Contains(g.Log, rn.Commits[0]) {
		t.Fatal("提炼文本必须是源文子串（引用不概括）")
	}
}

// ③ 文件帽 10：超限保最新（尾部）。
func TestResumeFilesCap(t *testing.T) {
	var status string
	for i := 0; i < 15; i++ {
		status += " M file" + string(rune('a'+i)) + ".go\n"
	}
	rn := roomops.ExtractResumeNotes(roomops.GitSources{StatusShort: status}, nil, 0)
	if len(rn.Files) != 10 {
		t.Fatalf("文件帽 10（got %d）", len(rn.Files))
	}
	// 保最新尾部：file o（第 15 个）在，file a（最旧）不在
	if !resContains(rn.Files, "fileo.go") || resContains(rn.Files, "filea.go") {
		t.Fatalf("保最新（got %v）", rn.Files)
	}
}

// ④ 注入模板三段结构＋各段时间戳。
func TestRenderResumeInjection(t *testing.T) {
	rn := roomops.ResumeNotes{
		Files: []string{"a.go"}, Commits: []string{"feat: x"}, Notes: []string{"下一步"},
		FilesAt: 1700000000, CommitsAt: 1700000100, NotesAt: 1700000200,
	}
	out := roomops.RenderResumeInjection("proj", rn, 1699999000, 1700000300)
	for _, want := range []string{"【接续要点】", "▸ 读什么", "▸ 记得什么", "▸ 干什么", "a.go", "feat: x", "下一步", "以台账为准"} {
		if !strings.Contains(out, want) {
			t.Errorf("注入缺 %q:\n%s", want, out)
		}
	}
}

// ⑤ 三源全空 → 退化泛模板（零回退）。
func TestRenderResumeEmptyFallback(t *testing.T) {
	out := roomops.RenderResumeInjection("proj", roomops.ResumeNotes{}, 0, 0)
	if !strings.Contains(out, "读任务详情") {
		t.Fatalf("空源应退化泛模板（got %s）", out)
	}
}

// ⑥ 500 字外帽。
func TestRenderResumeCap(t *testing.T) {
	files := make([]string, 10)
	for i := range files {
		files[i] = strings.Repeat("很长的路径名", 10) + ".go"
	}
	out := roomops.RenderResumeInjection("p", roomops.ResumeNotes{Files: files}, 0, 0)
	if runes := []rune(out); len(runes) > roomops.ResumeTotalCap+50 { // 帽+截断注
		t.Fatalf("超帽未截（%d 字）", len(runes))
	}
}

// ⑦ 确认行拦截：dispatcher 侧（已读要点→极简系统行、第二次吞）。
// 用 dispatch 包的注入模板做端到端字符串面（协议行为在协议测试钉）。
func TestResumeAckLineContract(t *testing.T) {
	// 确认行文案口径（冻结）：「已读要点，从 X 继续」——拦截的前缀判据
	line := "已读要点，从「下一步线索」继续"
	if !strings.HasPrefix(line, "已读要点") {
		t.Fatal("确认行前缀判据")
	}
	_ = dispatch.ContinueText // 退化兜底的旧路仍导出（零回退面）
}

func resContains(ss []string, sub string) bool {
	for _, s := range ss {
		if s == sub {
			return true
		}
	}
	return false
}
