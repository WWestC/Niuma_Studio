package server

// plan_accept 的发布腿：任务入库成功后必须敲 Options.PlanLanded——
// created/accepted 广播帧不带 @、唤不醒任何人，这条腿才把「发布」真
// 正发出去。钩子（异步）拿到项目、提案身份与全量落地任务快照。

import (
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/tasks"
)

func TestPlanAcceptTapsPlanLanded(t *testing.T) {
	hub := chat.NewHub()
	ps := plan.OpenMemory()
	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{chat.LobbyKey} })
	hookDone := make(chan struct{})
	var gotProject, gotPlanID, gotSubmitter string
	var gotLanded []*tasks.Task
	s, err := Start(hub, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			PlanStore: ps,
			Engine:    eng,
		},
		LocalName: "房主",
		Fleet: FleetFuncs{PlanLandedFn: func(project, planID, planTitle, submitter string, landed []*tasks.Task) error {
			gotProject, gotPlanID, gotSubmitter, gotLanded = project, planID, submitter, landed
			close(hookDone)
			return nil
		}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	stored, _ := ps.Submit("", chat.LobbyKey, "小牛", plan.Plan{
		Title: "拆解", ProjectKey: chat.LobbyKey,
		Tasks: []plan.PlanTask{{Title: "单子一", Assignee: "张三"}, {Title: "单子二"}},
	}, time.Now().Unix())
	msg := s.plansFace().PlanAcceptAs(hub, nil, "房主", stored.ID, nil, "")
	if msg.Event != "accepted" {
		t.Fatalf("提案应被接受：%+v", msg)
	}
	select {
	case <-hookDone:
	case <-time.After(2 * time.Second):
		t.Fatal("PlanLanded 钩子未被触发")
	}
	if gotProject != chat.LobbyKey || gotPlanID != stored.ID || gotSubmitter != "小牛" || len(gotLanded) != 2 {
		t.Fatalf("钩子参数不对：project=%s plan=%s submitter=%s landed=%d", gotProject, gotPlanID, gotSubmitter, len(gotLanded))
	}
	if gotLanded[0].Assignee != "张三" || gotLanded[0].PlanID != stored.ID ||
		gotLanded[1].Assignee != "" || gotLanded[1].PlanID != stored.ID {
		t.Fatalf("落地任务快照不对：%+v %+v", gotLanded[0], gotLanded[1])
	}
}
