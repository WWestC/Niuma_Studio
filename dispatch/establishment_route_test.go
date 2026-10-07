package dispatch

// establishment_route_test.go — v2.5 分项目编制的路由钉：落在项目表的
// 【加编通知】先送该项目自己的在岗 HR（编排者/HR 每个项目各一套，自
// 己项目的加编自己跟进），项目没有 HR 时回落大厅 HR（工作室级招聘
// 台），两边都没有才如实报错——AutoPilotPatrol 同款回落学说。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
)

// TestFleetEstablishmentAddedPrefersProjectHR walks the three rungs:
// nobody seated → honest error; lobby HR only → fallback delivers with
// the scope named; project's own HR seated → the notice moves to the
// project desk and the lobby HR hears nothing further.
func TestFleetEstablishmentAddedPrefersProjectHR(t *testing.T) {
	f, rb := patrolFleetStage(t)
	row := kb.EstablishmentRow{Key: "be", Name: "后端", Role: "后端",
		Headcount: 1, AutoFill: true, Manual: "roles/be"}

	// 两处都无 HR：如实报错指路
	err := f.EstablishmentAdded("proj-p", row, 0, "房主")
	if err == nil || !strings.Contains(err.Error(), "HR") {
		t.Fatalf("两处均无 HR 应回错指路（got %v）", err)
	}

	// 大厅 HR 落座：回落送达，通知自带项目范围
	if err := f.Get(chat.LobbyKey).attach("小马", agents.HRRole, "s-hr"); err != nil {
		t.Fatal(err)
	}
	if err := f.EstablishmentAdded("proj-p", row, 0, "房主"); err != nil {
		t.Fatalf("项目无 HR 应回落 Niuma_Studio 送达：%v", err)
	}
	waitFor(t, func() bool { return strings.Count(rb.all("s-hr"), "【加编通知") == 1 },
		"Niuma_Studio HR 应收到一封加编通知")
	if got := rb.all("s-hr"); !strings.Contains(got, "项目 proj-p 的编制表") {
		t.Fatalf("回落送达应带项目范围：%q", got)
	}

	// 项目自己的 HR 到岗：通知改送项目 HR，大厅 HR 不再新增
	if err := f.Get("proj-p").attach("人事姐", agents.HRRole, "s-hr-p"); err != nil {
		t.Fatal(err)
	}
	if err := f.EstablishmentAdded("proj-p", row, 0, "房主"); err != nil {
		t.Fatalf("项目 HR 在岗应自己收：%v", err)
	}
	waitFor(t, func() bool { return strings.Count(rb.all("s-hr-p"), "【加编通知") == 1 },
		"项目 HR 应收到加编通知")
	if n := strings.Count(rb.all("s-hr"), "【加编通知"); n != 1 {
		t.Fatalf("项目 HR 到岗后 Niuma_Studio HR 不应再收（got %d 封）", n)
	}

	// 大厅范围的行照旧直送大厅 HR（项目腿不参与）
	if err := f.EstablishmentAdded(chat.LobbyKey, row, 1, "房主"); err != nil {
		t.Fatalf("Niuma_Studio 范围应送达 Niuma_Studio HR：%v", err)
	}
	waitFor(t, func() bool { return strings.Count(rb.all("s-hr"), "【加编通知") == 2 },
		"Niuma_Studio 范围行应再送 Niuma_Studio HR 一封")
	if got := rb.all("s-hr-p"); strings.Contains(got, "Niuma_Studio 编制表") {
		t.Fatalf("Niuma_Studio 范围行不该绕道项目 HR：%q", got)
	}
}
