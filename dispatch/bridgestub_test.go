package dispatch

// bridgestub_test.go — 假桥基桩：每个测试假桥嵌入 baseBridge，只覆写
// 自己关心的方法。下面这组惰性默认曾是每个假桥手工复制的样板（每加
// 一个 Bridge 方法要改 19 处假桥——ProbeSession 就是一次）；现在接口
// 长新方法只改这一处，嵌入了别的假桥的（gateBridge{errBridge} 一族）
// 经继承链同样落在这里。默认值即既有样板的字节等值：Send 归 nil ack
// （生产路径只看 err），SetHooks/SetReverse 静默丢弃。

import (
	"context"

	"github.com/WWestC/Niuma_Studio/zcode"
)

type baseBridge struct{}

func (baseBridge) ListSessions(ctx context.Context) ([]zcode.SessionInfo, error) {
	return nil, nil
}
func (baseBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	return "", nil
}
func (baseBridge) SetModel(ctx context.Context, sessionID string, sel zcode.ModelSelection) error {
	return nil
}
func (baseBridge) ResumeSession(ctx context.Context, sessionID string) (*zcode.ResumeResult, error) {
	return nil, nil
}
func (baseBridge) Subscribe(ctx context.Context, sessionID string) (*zcode.SubscribeResult, error) {
	return nil, nil
}
func (baseBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	return nil, nil
}
func (baseBridge) Steer(ctx context.Context, sessionID, content string) error { return nil }
func (baseBridge) ProbeSession(ctx context.Context, sessionID string) error   { return nil }
func (baseBridge) SetHooks(h zcode.Hooks)                                     {}
func (baseBridge) SetReverse(fn func(method string, params map[string]any) (any, error)) {
}

// 编译期钉：基桩必须始终满足 Bridge——接口长新方法而基桩没跟，这里
// 先红（比 19 个假桥各自的缺口先现形）。
var _ Bridge = baseBridge{}
