package server

// planguard_test.go — 跨需求顶替护栏（提案槽一房一件）：槽里挂着的
// 待审件属于别的需求时，plan submit 拒绝——supersede 只服务同需求的
// 修订重提，不能把上一场评审等待房主的结论静默作废；同需求照旧可顶
// （一场会内 p_52→p_55 的收口弧线）。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/plan"
)

func TestPlanSubmitRefusesCrossReqSupersede(t *testing.T) {
	hub := chat.NewHub()
	ps := plan.OpenMemory()
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{PlanStore: ps}, LocalName: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	submit := func(req, title string) chat.Message {
		return s.plansFace().PlanSubmitAs(hub, nil, "房主", &plan.Plan{
			Req: req, Title: title, Tasks: []plan.PlanTask{{Title: "单子一"}},
		})
	}

	// 前置：r_01 的提案在槽等房主
	first := submit("r_01", "第一场的拆解")
	if first.Event == "denied" {
		t.Fatalf("首份提案应可提交：%+v", first)
	}

	// 别的需求（r_02）想顶 → 拒，且槽里还是 p_01
	denied := submit("r_02", "另一场的拆解")
	if denied.Event != "denied" || !strings.Contains(denied.Text, "在等房主审批") {
		t.Fatalf("跨需求顶替应被拒绝并说明在审件，得到：%+v", denied)
	}
	if cur := ps.Pending("", chat.LobbyKey); cur == nil || cur.ID != first.Plan.ID {
		t.Fatalf("被拒后槽里应仍是原待审件 %s，得到：%+v", first.Plan.ID, cur)
	}

	// 无 req 的新提案想顶有 req 的在审件 → 也拒（无法证明是同一件
	// 事的修订）
	denied2 := submit("", "不明归属的提案")
	if denied2.Event != "denied" {
		t.Fatalf("无 req 提案不应顶掉有 req 的在审件：%+v", denied2)
	}

	// 同需求修订重提 → 放行，p_01 被顶成 p_02（收口弧线的合法形状）
	rev := submit("r_01", "第一场的修订版")
	if rev.Event == "denied" {
		t.Fatalf("同需求修订应放行：%+v", rev)
	}
	if cur := ps.Pending("", chat.LobbyKey); cur == nil || cur.ID == first.Plan.ID || cur.Title != "第一场的修订版" {
		t.Fatalf("修订版应顶替为新的待审件，得到：%+v", cur)
	}
}
