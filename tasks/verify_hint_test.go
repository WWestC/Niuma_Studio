package tasks

// verify_hint_test.go — r_26（t_215）：验收类任务 desc 尾附提示行的
// 兜底钉子。标题含「验收」即附（Create 与 CreatePlanned 双路——CLI 与
// plan 入库同一闸门）；幂等（手写过的不再叠）；非验收单不沾。

import (
	"strings"
	"testing"
)

func openEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := OpenMemory("房主")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// ① Create：标题含「验收」→ desc 尾附一行索引。
func TestVerifyHintCreate(t *testing.T) {
	e := openEngine(t)
	out := e.Create("", "小牛", "档案页验收（轮值）", "按验收口径四条执行。", "小马")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if got := out.Task.Desc; !strings.Contains(got, "⚠ 验收前读 kb manual 验收纪律节（八条）") {
		t.Fatalf("验收单 desc 应尾附提示行（got %q）", got)
	} else if !strings.Contains(got, "按验收口径四条执行。") {
		t.Fatal("原 desc 应保留在提示行之前")
	}
}

// ② CreatePlanned（plan 入库路）同样附。
func TestVerifyHintPlanned(t *testing.T) {
	e := openEngine(t)
	out := e.CreatePlanned("小牛", Task{Title: "纪要沉淀验收", Desc: "对照验收口径。", Assignee: "小马"})
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if !strings.Contains(out.Task.Desc, "验收纪律节") {
		t.Fatalf("plan 入库的验收单同样应附（got %q）", out.Task.Desc)
	}
}

// ③ 幂等：手写提示行的 desc 不再叠第二份。
func TestVerifyHintIdempotent(t *testing.T) {
	e := openEngine(t)
	desc := "自备提示：⚠ 验收前读 kb manual 验收纪律节（八条）"
	out := e.Create("", "小牛", "触屏验收", desc, "小鹿")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if n := strings.Count(out.Task.Desc, "验收纪律节"); n != 1 {
		t.Fatalf("手写过的不再叠（got %d 份）", n)
	}
}

// ④ 非验收单不沾边。
func TestVerifyHintOnlyAcceptance(t *testing.T) {
	e := openEngine(t)
	out := e.Create("", "小牛", "档案浮层实现", "照定稿实现。", "小猿")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if strings.Contains(out.Task.Desc, "验收纪律") {
		t.Fatal("非验收单不该附提示行")
	}
}

// ⑤ 空 desc 的验收单：提示行独立成文（不空指针不悬空换行）。
func TestVerifyHintEmptyDesc(t *testing.T) {
	e := openEngine(t)
	out := e.Create("", "小牛", "纪律库验收", "", "小马")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if !strings.Contains(out.Task.Desc, "⚠ 验收前读") {
		t.Fatalf("空 desc 验收单也应附（got %q）", out.Task.Desc)
	}
}
