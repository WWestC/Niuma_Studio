package dispatch

// 消息注入等待的可调面（设置卡「智能」面板第五位）：Start 归一缺省、
// SetSendWait 原子热改、deliver 的 send ctx 真用到配置值、Fleet.SetSendWait
// 存量热改＋模板带新值（EnsureRoom 晚生的调度器一样带上）。

import (
	"context"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// TestSendWaitDefaultAndSetter: Start 把 0/负值归一到 DefaultSendWait；
// SetSendWait 原子落值，非正值同样回缺省——设置卡写来的任何畸形值都
// 不会让等待变成 0（那等于每投必弃单）。
func TestSendWaitDefaultAndSetter(t *testing.T) {
	d := Start(chat.NewHub(), nil, nil, Config{})
	if got := d.SendWait(); got != DefaultSendWait {
		t.Fatalf("缺省 SendWait = %v, want %v", got, DefaultSendWait)
	}
	d.SetSendWait(42 * time.Second)
	if got := d.SendWait(); got != 42*time.Second {
		t.Fatalf("SetSendWait 后 = %v, want 42s", got)
	}
	d.SetSendWait(0)
	if got := d.SendWait(); got != DefaultSendWait {
		t.Fatalf("SetSendWait(0) 后 = %v, want 缺省 %v", got, DefaultSendWait)
	}
}

// blockBridge 的 Send 挂在 ctx 上直到超时——deliver 的注入等待是不是
// 真用了配置值，只有这条路径能证明（默认 5 分钟等下来测试早超时了）。
type blockBridge struct{ errBridge }

func (b *blockBridge) Send(ctx context.Context, sid, content, inputID string, deny []string) (*zcode.SendAck, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestDeliverHonorsConfiguredSendWait: 等待 60ms 的调度器对挂死 bridge
// 的投递在 ~60ms 落进「结果未知」弃单分支（不排队），且全程远小于
// 缺省 5 分钟——配置值真的流进了 send 的 ctx。
func TestDeliverHonorsConfiguredSendWait(t *testing.T) {
	h := chat.NewHub()
	d := &Dispatcher{
		hub: h, cfg: Config{InboxDir: t.TempDir()},
		projectKey: "default", members: map[string]*member{},
		bridge: &blockBridge{},
	}
	d.SetSendWait(DefaultSendWait) // 构造绕过 Start 时的手工对齐
	d.SetSendWait(60 * time.Millisecond)
	seat, _ := h.Join("小H", "人事", false)
	m := &member{name: "小H", role: "人事", sessionID: "s_1", seat: seat, status: StatusIdle}
	d.members["小H"] = m

	t0 := time.Now()
	d.deliver("小H", "【办公室消息】去招牛马", nil, nil)
	elapsed := time.Since(t0)
	if elapsed >= 5*time.Second {
		t.Fatalf("投递挂了 %v——send ctx 显然没吃到 60ms 配置值", elapsed)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("投递 %v 就返回了——没等就弃单，等待值没接上", elapsed)
	}
	if len(m.lanes.queue) != 0 {
		t.Fatalf("超时（结果未知）竟入了重试队列: %v", m.lanes.queue)
	}
}

// TestFleetSetSendWaitFanout: SetSendWait 热改存量调度器，且值进 fleet
// 模板——之后 EnsureRoom 晚生的调度器一出生就带上新值。
func TestFleetSetSendWaitFanout(t *testing.T) {
	dir := t.TempDir()
	projs, err := projects.Open(dir + "/projects.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.EnsureLobby(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := projs.Create(projects.Project{Key: "proj-x", Name: "项目X",
		Workspace: dir + "/proj-x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := projs.Activate("proj-x"); err != nil {
		t.Fatal(err)
	}
	staff, err := staffing.Open(dir + "/staffing.json")
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	hub := chat.NewHub()
	f := StartFleet(&stubBridge{}, agentStore, projs, staff,
		map[string]*chat.Hub{chat.LobbyKey: hub}, Config{Workspace: dir})
	t.Cleanup(f.Stop)

	lobby := f.Lobby()
	if lobby == nil {
		t.Fatal("Niuma_Studio 调度器缺席")
	}
	if got := lobby.SendWait(); got != DefaultSendWait {
		t.Fatalf("出生 SendWait = %v, want 缺省 %v", got, DefaultSendWait)
	}
	f.SetSendWait(42 * time.Second)
	if got := lobby.SendWait(); got != 42*time.Second {
		t.Fatalf("热改后 Niuma_Studio SendWait = %v, want 42s", got)
	}
	// 晚生的调度器：EnsureRoom 走 fleet 模板，必须同样 42s
	dx, err := f.EnsureRoom("proj-x", chat.NewHub())
	if err != nil {
		t.Fatalf("EnsureRoom: %v", err)
	}
	if got := dx.SendWait(); got != 42*time.Second {
		t.Fatalf("晚生调度器 SendWait = %v, want 42s（模板没带新值）", got)
	}
}
