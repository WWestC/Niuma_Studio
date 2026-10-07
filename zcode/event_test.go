package zcode

// event_test.go — 回合终点的三种形状（复读根治，2026-10-03 book 房实
// 录：一个回合内每次模型请求收尾都有一条 stopReason=="stop" 的边界
// 帧，模型在工具调用后会把待发的结束语原样重说；真终点只有类型化
// turn.completed，每回合恰一次）。
import "testing"

func TestTurnStoppedKeysRequestBoundaryFrames(t *testing.T) {
	ev := Event{Payload: map[string]any{
		"querySource": "main_turn", "stopReason": "stop", "content": "收尾话"}}
	content, ok := ev.TurnStopped()
	if !ok || content != "收尾话" {
		t.Fatalf("边界帧应被 TurnStopped 认领，得 %q %v", content, ok)
	}
	if _, _, done := ev.TurnDone(); done {
		t.Fatal("边界帧不是失败终点，TurnDone 不得认领")
	}
	if _, _, done := ev.TurnCompleted(); done {
		t.Fatal("边界帧不是类型化真终点")
	}
}

func TestTurnDoneKeysOnlyFailureReasons(t *testing.T) {
	for _, reason := range []string{"error", "max_tokens", "aborted"} {
		ev := Event{Payload: map[string]any{
			"querySource": "main_turn", "stopReason": reason, "content": "半截话"}}
		content, stopReason, ok := ev.TurnDone()
		if !ok || stopReason != reason || content != "半截话" {
			t.Fatalf("%q 应是失败终点，得 %q %q %v", reason, content, stopReason, ok)
		}
		if _, ok := ev.TurnStopped(); ok {
			t.Fatalf("%q 不是边界帧", reason)
		}
	}
	for name, payload := range map[string]map[string]any{
		"tool-calls 中途停": {"querySource": "main_turn", "stopReason": "tool-calls", "content": "x"},
		"空 reason":       {"querySource": "main_turn", "stopReason": "", "content": "x"},
		"非 main_turn":    {"querySource": "steer", "stopReason": "error", "content": "x"},
	} {
		ev := Event{Payload: payload}
		if _, _, ok := ev.TurnDone(); ok {
			t.Fatalf("%s 不得被 TurnDone 认领", name)
		}
	}
	// 带 kind 的流片段（parseEvent 提升 Kind）也不是终态信封。
	if _, _, ok := (Event{Kind: "text_delta", Payload: map[string]any{
		"querySource": "main_turn", "stopReason": "error"}}).TurnDone(); ok {
		t.Fatal("带 kind 不得被 TurnDone 认领")
	}
}

func TestTurnCompletedKeysTypedTerminal(t *testing.T) {
	ev := Event{Type: "turn.completed", Payload: map[string]any{
		"response": "最终全文", "resultType": "success", "duration": 18311.0}}
	content, resultType, ok := ev.TurnCompleted()
	if !ok || content != "最终全文" || resultType != "success" {
		t.Fatalf("turn.completed 应带 response/resultType，得 %q %q %v", content, resultType, ok)
	}
	if _, _, done := ev.TurnDone(); done {
		t.Fatal("类型化真终点不是失败信封")
	}
	if _, ok := ev.TurnStopped(); ok {
		t.Fatal("类型化真终点不是边界帧")
	}
	// 缺 resultType（旧投影形）也要认，response 允许为空（调度器以边界
	// 帧口信兜底）。
	if _, rt, ok := (Event{Type: "turn.completed", Payload: map[string]any{}}).TurnCompleted(); !ok || rt != "" {
		t.Fatalf("空 payload 的 turn.completed 应被认领，得 %q %v", rt, ok)
	}
}
