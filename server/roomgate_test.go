package server

// v2.10 号段分家的房间门：WS 任务动词的裸号只在本房货架解析——
// 别家房间的单，本房摸不到（crossRoomTask 判据＋taskUpdateGateOp/
// taskConfirmGateOp 的拒收），池单照旧全室面（大厅架）。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/tasks"
)

func TestTaskVerbsRoomGate(t *testing.T) {
	s, lobby := startIsolatedStudio(t)
	hubA, err := s.opts.Registry.Hub("proj-a")
	if err != nil {
		t.Fatalf("hub proj-a: %v", err)
	}
	hubB, err := s.opts.Registry.Hub("proj-b")
	if err != nil {
		t.Fatalf("hub proj-b: %v", err)
	}

	var idA string
	for _, tt := range s.opts.Stores.Engine.ListFiltered("", "", "proj-a", "") {
		idA = tt.ID
	}
	if idA == "" {
		t.Fatal("夹具应有 proj-a 任务")
	}

	// 判据面：proj-b 视角看 proj-a 的单＝跨房；proj-a 自看＝本房
	if !s.gitFace().CrossRoomTask("proj-b", idA) {
		t.Fatal("proj-b 视角应判跨房")
	}
	if s.gitFace().CrossRoomTask("proj-a", idA) {
		t.Fatal("proj-a 自看不应判跨房")
	}

	// 动词面：proj-b 房间里的成员拿 proj-a 的裸号更新 → 拒收不落地
	s.gitFace().TaskUpdateGateOp(hubB, nil, "小蓝", idA, tasks.Patch{Status: tasks.StatusDoing})
	if got, _ := s.opts.Stores.Engine.GetIn("proj-a", idA); got.Status != tasks.StatusTodo {
		t.Fatalf("跨房更新不应落地：status=%s", got.Status)
	}
	s.gitFace().TaskConfirmGateOp(hubB, nil, "小蓝", idA)
	// 同房（proj-a 的小红，本单负责人）照常落地
	s.gitFace().TaskUpdateGateOp(hubA, nil, "小红", idA, tasks.Patch{Status: tasks.StatusDoing})
	if got, _ := s.opts.Stores.Engine.GetIn("proj-a", idA); got.Status != tasks.StatusDoing {
		t.Fatalf("本房更新应落地：status=%s", got.Status)
	}

	// 池单（无挂靠）住大厅架：大厅房间照旧可动——池本就是全室面
	out := s.opts.Stores.Engine.Create("", "房主", "池单", "", "")
	if out.Denied {
		t.Fatalf("create pool: %s", out.Reason)
	}
	if s.gitFace().CrossRoomTask(s.hubProject(lobby), out.Task.ID) {
		t.Fatal("池单住 Niuma_Studio 架，Niuma_Studio 视角不算跨房")
	}
	s.gitFace().TaskUpdateGateOp(lobby, nil, "Niuma_Studio 小明", out.Task.ID, tasks.Patch{Status: tasks.StatusDoing})
	if got, _ := s.opts.Stores.Engine.GetIn("default", out.Task.ID); got.Status != tasks.StatusDoing {
		t.Fatalf("Niuma_Studio 房动池单应落地：status=%s", got.Status)
	}

	// 本房不存在的号：不是跨房（别家也没有）——引擎按原口径报不存在，
	// 不 panic
	s.gitFace().TaskUpdateGateOp(hubA, nil, "小红", "t_999", tasks.Patch{Status: tasks.StatusDoing})
}
