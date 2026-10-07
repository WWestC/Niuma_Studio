package server

// plansubmit_nilshape_test.go — 前置审计 A 的红测试的转正后形态：
// 旧 sqlstore 全量实现
// 的「Submit 事务失败返回 (nil, nil)」形状随该实现一起删除（引擎是
// 内存真源，Submit 恒返回 stored 非-nil）；planSubmitAs 的 nil 守卫
// 保留为防御（消费方永不解引用 nil stored），此处钉正路不误伤。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/plan"
)

func TestPlanSubmitNilGuardDoesNotFireOnSuccess(t *testing.T) {
	hub := chat.NewHub()
	ps := plan.OpenMemory()
	s, err := Start(hub, Options{
		Endpoint:  EndpointConfig{PreferredPort: -1},
		Stores:    StoreSet{PlanStore: ps},
		LocalName: "房主",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	msg := s.plansFace().PlanSubmitAs(hub, nil, "房主", &plan.Plan{
		Title: "正常入库的提案", Tasks: []plan.PlanTask{{Title: "单子一"}},
	})
	if msg.Event == "denied" && strings.Contains(msg.Text, "未落库") {
		t.Fatalf("正路不应触发未落库守卫：%+v", msg)
	}
	if got := ps.Pending("", s.roomKey(hub)); got == nil {
		t.Fatal("提交应已入槽")
	}
}
