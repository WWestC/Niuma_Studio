package requirements_test

// parking_test.go — r_31（t_136）：parking 状态机的生命周期钉子＋双口径
// 计数（口径表即本文件的双口径断言）。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/requirements"
)

func parkStore(t *testing.T) *requirements.Engine {
	t.Helper()
	return requirements.OpenMemory()
}

func mustCreate(t *testing.T, s *requirements.Engine, key, title string) string {
	t.Helper()
	r, err := s.Create("", key, title, "", "房主")
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

// ① 验收 1 前半：open→park 带原因→unpark→恢复可拆解。
func TestParkLifecycle(t *testing.T) {
	s := parkStore(t)
	id := mustCreate(t, s, "demo", "多项目房实战")

	r, err := s.Park("", "demo", id, "等房主建第二项目", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != requirements.StatusParking || r.ParkNote != "等房主建第二项目" || r.ParkTS == 0 {
		t.Fatalf("park 后字段应齐（got %+v）", r)
	}
	// 拆解不可见：MarkSplit 拒（parking 不是 open）
	if _, err := s.MarkSplit("", "demo", id); err == nil || !strings.Contains(err.Error(), "仅 open") {
		t.Fatalf("parking 态 MarkSplit 必拒（got %v）", err)
	}
	// 解暂缓：回 open，原因保留可追溯
	r, err = s.Unpark("", "demo", id)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != requirements.StatusOpen {
		t.Fatalf("unpark 后应回 open（got %s）", r.Status)
	}
	if r.ParkNote != "等房主建第二项目" {
		t.Fatal("unpark 不抹原因（编排决策可追溯）")
	}
	// 恢复可拆解
	if _, err := s.MarkSplit("", "demo", id); err != nil {
		t.Fatalf("unpark 后 MarkSplit 应放行（got %v）", err)
	}
}

// ② 状态机边界：park 只从 open 可达（split/closed 拒）；MarkClosed 拒
// parking（先 unpark 再终局——双向孤岛）。
func TestParkStateMachineEdges(t *testing.T) {
	s := parkStore(t)
	// split 态拒 park
	idS := mustCreate(t, s, "demo", "已拆解的")
	if _, err := s.MarkSplit("", "demo", idS); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Park("", "demo", idS, "试试", 0); err == nil {
		t.Fatal("split 态 park 必拒（任务树不脱钩）")
	}
	// closed 态拒 park
	idC := mustCreate(t, s, "demo", "已关闭的")
	if _, err := s.MarkClosed("", "demo", idC); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Park("", "demo", idC, "试试", 0); err == nil {
		t.Fatal("closed 态 park 必拒（终局留档）")
	}
	// parking 态 MarkClosed 拒
	idP := mustCreate(t, s, "demo", "暂缓中的")
	if _, err := s.Park("", "demo", idP, "等依赖", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkClosed("", "demo", idP); err == nil || !strings.Contains(err.Error(), "unpark") {
		t.Fatalf("parking 态 MarkClosed 必拒并指路 unpark（got %v）", err)
	}
}

// ③ DELETE 可删 parking（误录清理——parking 是库存态非保护态）。
func TestParkDeletable(t *testing.T) {
	s := parkStore(t)
	id := mustCreate(t, s, "demo", "错录的")
	if _, err := s.Park("", "demo", id, "等核对", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete("", "demo", id); err != nil {
		t.Fatalf("parking 应可删（got %v）", err)
	}
}

// ④ 复查期字段往返（omitempty 落盘零报错——旧档兼容由 omitempty 保证）。
func TestParkReviewAfterRoundtrip(t *testing.T) {
	s := parkStore(t)
	id := mustCreate(t, s, "demo", "带复查期")
	after := int64(1791000000)
	if _, err := s.Park("", "demo", id, "等发布窗口", after); err != nil {
		t.Fatal(err)
	}
	got, ok := s.GetIn("", "demo", id)
	if !ok || got.ReviewAfter != after {
		t.Fatalf("复查期应落行（got %+v）", got)
	}
}

// ⑤ 再 park 覆盖原因（新决策覆盖旧决策，历史在编年史）。
func TestParkNoteOverwrite(t *testing.T) {
	s := parkStore(t)
	id := mustCreate(t, s, "demo", "改主意的")
	if _, err := s.Park("", "demo", id, "第一版原因", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Unpark("", "demo", id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Park("", "demo", id, "第二版原因", 0); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIn("", "demo", id)
	if got.ParkNote != "第二版原因" {
		t.Fatalf("再 park 应覆盖原因（got %q）", got.ParkNote)
	}
}
