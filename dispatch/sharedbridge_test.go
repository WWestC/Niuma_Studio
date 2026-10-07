package dispatch

// sharedbridge 的换桥面（看门狗死亡恢复的承接层）：app-server 子进程
// 中途死掉后，Fleet.ReplaceBridge 把活的新子进程换进来——钩子/反问
// 必须重装到新桥、透传调用必须走新桥，惰性车队（无桥可换）必须拒绝。

import (
	"context"
	"testing"

	"github.com/WWestC/Niuma_Studio/zcode"
)

// fakeBridge 记录钩子安装与透传调用，供换桥断言。
type fakeBridge struct {
	baseBridge
	hooks   int
	reverse int
	listed  int
}

func (f *fakeBridge) ListSessions(ctx context.Context) ([]zcode.SessionInfo, error) {
	f.listed++
	return nil, nil
}

func (f *fakeBridge) SetHooks(h zcode.Hooks) { f.hooks++ }
func (f *fakeBridge) SetReverse(fn func(method string, params map[string]any) (any, error)) {
	f.reverse++
}

// TestSharedBridgeSwapRewires pins 换桥的承接语义：wire 钩子重装到新桥
// （旧桥上的安装作废），透传调用从换桥那一刻起走新桥。
func TestSharedBridgeSwapRewires(t *testing.T) {
	oldB, newB := &fakeBridge{}, &fakeBridge{}
	sb := newSharedBridge(oldB)
	sb.swap(newB)
	if newB.hooks != 1 || newB.reverse != 1 {
		t.Fatalf("换桥后钩子未装到新桥: hooks=%d reverse=%d", newB.hooks, newB.reverse)
	}
	if _, err := sb.ListSessions(context.Background()); err != nil {
		t.Fatalf("透传调用失败: %v", err)
	}
	if newB.listed != 1 || oldB.listed != 0 {
		t.Fatalf("透传没有走新桥: new=%d old=%d", newB.listed, oldB.listed)
	}
}

// TestFleetReplaceBridge pins 车队换桥的门槛：带桥车队换到新桥（内层
// 替换、钩子装好），惰性车队（--no-dispatch 形态，无桥）拒绝而不是
// 假装成功。
func TestFleetReplaceBridge(t *testing.T) {
	if f := inertFleet(t); f.ReplaceBridge(&fakeBridge{}) {
		t.Fatal("惰性车队无桥可换却回了 true")
	}

	f := inertFleet(t) // stores 搭好后带桥重启（无活动项目：零调度器）
	f.bridge = newSharedBridge(&fakeBridge{})
	nb := &fakeBridge{}
	if !f.ReplaceBridge(nb) {
		t.Fatal("带桥车队换桥应成功")
	}
	sb, ok := f.bridge.(*sharedBridge)
	if !ok || sb.Bridge.(*fakeBridge) != nb {
		t.Fatal("车队还攥着旧桥")
	}
	if nb.hooks != 1 {
		t.Fatalf("新桥没装钩子: %d", nb.hooks)
	}
}

// mcpCapableBridge 是带 per-session MCP 舰队替换动词的内桥（生产行为
// 的 *zcode.Client 面）：记录收到的替换集。
type mcpCapableBridge struct {
	fakeBridge
	servers []zcode.MCPServerSpec
	calls   int
}

func (b *mcpCapableBridge) CreateSessionMCP(ctx context.Context, workspace, mode string, servers []zcode.MCPServerSpec) (string, error) {
	b.calls++
	b.servers = servers
	return "sess-override", nil
}

// plainCountBridge 数普通 create 次数（无替换动词的内桥——降级面）。
type plainCountBridge struct {
	fakeBridge
	creates int
}

func (b *plainCountBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	b.creates++
	return "sess-plain", nil
}

// TestSharedBridgeMCPOverridePassthrough pins 装饰器对 CreateSessionMCP
// 的显式透传：内嵌 Bridge 接口只会提升接口自己的方法，不补这一手，
// 消费者在 driver 上做的可选接口断言（小助手探针）就摸不到底下的
// *zcode.Client；无该动词的内桥（测试桩）降级为普通 create。
func TestSharedBridgeMCPOverridePassthrough(t *testing.T) {
	inner := &mcpCapableBridge{}
	sb := newSharedBridge(inner)
	spec := []zcode.MCPServerSpec{{
		Name: "niuma-probe-none", Type: "http", URL: "http://127.0.0.1:9/mcp",
		Headers: []zcode.MCPHeader{}, Isolation: "session", TimeoutMs: 2000,
	}}
	sid, err := sb.CreateSessionMCP(context.Background(), "/ws", "yolo", spec)
	if err != nil {
		t.Fatalf("透传失败: %v", err)
	}
	if sid != "sess-override" || inner.calls != 1 {
		t.Fatalf("透传没到内桥: sid=%q calls=%d", sid, inner.calls)
	}
	if len(inner.servers) != 1 || inner.servers[0].Name != "niuma-probe-none" {
		t.Fatalf("替换集没原样到达: %+v", inner.servers)
	}

	plain := &plainCountBridge{}
	sb2 := newSharedBridge(plain)
	sid2, err := sb2.CreateSessionMCP(context.Background(), "/ws", "yolo", spec)
	if err != nil {
		t.Fatalf("降级路径失败: %v", err)
	}
	if sid2 != "sess-plain" || plain.creates != 1 {
		t.Fatalf("无动词内桥未降级为普通 create: sid=%q creates=%d", sid2, plain.creates)
	}
}
