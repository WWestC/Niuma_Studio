package zcode

import "encoding/json"

// Event is one parsed "session/event" notification.
//
// Terminal shapes (probe-verified against CLI 3.14 + 房主实录
// 2026-10-03)：一个回合的工具循环里，**每次模型请求**流式收尾都会发
// 一条无 kind 的信封（querySource=="main_turn" + stopReason +
// content）——stopReason=="stop" 是请求边界帧（模型常在工具调用后把
// 待发的结束语原样重说，逐帧镜像就是复读：小苗在房里连落三条同义回
// 复即此）；其余 stopReason 是失败终点。回合唯一权威的成功终点是**类
// 型化 "turn.completed" 通知**：每回合恰一次，payload.response 带最
// 终回复全文。TurnStopped 键边界帧，TurnDone 键失败信封，
// TurnCompleted 键真终点。
type Event struct {
	SessionID string
	// Kind is payload.kind ("" for no-kind envelopes).
	Kind string
	// Type is the notification envelope's event type — the projection's
	// camelCase vocabulary ("model.streaming", "tool.updated",
	// "turn.started", "turn.completed", "session.updated", …; probe-
	// verified against CLI 3.14). The trace tap classifies on it; every
	// pre-trace consumer keyed on Kind alone is untouched.
	Type string
	// Payload is the raw payload object.
	Payload map[string]any
}

// parseEvent lifts a session/event notification params blob into an
// Event. Fields may sit at payload level or params level on no-kind
// envelopes — both spots are checked (Z-Deck lesson).
func parseEvent(params json.RawMessage) (Event, bool) {
	var p struct {
		SessionID string          `json:"sessionId"`
		Payload   json.RawMessage `json:"payload"`
		EventID   string          `json:"eventId"`
		Type      string          `json:"type"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.SessionID == "" {
		return Event{}, false
	}
	ev := Event{SessionID: p.SessionID, Type: p.Type}
	if len(p.Payload) > 0 {
		_ = json.Unmarshal(p.Payload, &ev.Payload)
	}
	if ev.Payload == nil {
		// no-kind envelope: fields may live directly on params
		_ = json.Unmarshal(params, &ev.Payload)
	}
	if k, _ := ev.Payload["kind"].(string); k != "" {
		ev.Kind = k
	}
	return ev, true
}

// mainTurnFrame reports whether the event is a no-kind main_turn
// envelope with a decided stop reason, handing back its content and
// reason. M1 spike G: "tool-calls" is an intermediate stop — the model
// paused to call tools and the engine keeps driving the turn — and the
// empty reason is no decision at all; both yield false here.
func (e Event) mainTurnFrame() (content, stopReason string, ok bool) {
	if e.Kind != "" {
		return "", "", false
	}
	if qsrc, _ := e.Payload["querySource"].(string); qsrc != "main_turn" {
		return "", "", false
	}
	sr, _ := e.Payload["stopReason"].(string)
	if sr == "" || sr == "tool-calls" {
		return "", "", false
	}
	c, _ := e.Payload["content"].(string)
	return c, sr, true
}

// TurnDone reports whether the event is a turn's FAILURE terminal
// envelope — a main_turn frame with a stop reason that is neither
// "stop" (a per-request boundary, see TurnStopped) nor "tool-calls".
// Such a turn never reaches turn.completed's success face; the failure
// line and bookkeeping fire here.
func (e Event) TurnDone() (content, stopReason string, ok bool) {
	content, stopReason, ok = e.mainTurnFrame()
	if ok && stopReason == "stop" {
		return "", "", false
	}
	return content, stopReason, ok
}

// TurnStopped reports a clean per-model-request boundary frame
// (stopReason "stop"): the model wrote text and the loop may keep
// driving (tool call, next request). Its content is that request's own
// closing words — worth stashing as the turn's pending reply (the
// turn.completed response 缺形时的兜底)， never worth mirroring: the
// typed terminal owns the mirror.
func (e Event) TurnStopped() (content string, ok bool) {
	content, stopReason, ok := e.mainTurnFrame()
	if !ok || stopReason != "stop" {
		return "", false
	}
	return content, true
}

// TurnCompleted reports the typed "turn.completed" notification — the
// projection's one-per-turn success terminal, carrying the full final
// reply text (response) and how the turn concluded (resultType,
// "success" observed).
func (e Event) TurnCompleted() (content, resultType string, ok bool) {
	if e.Type != "turn.completed" {
		return "", "", false
	}
	c, _ := e.Payload["response"].(string)
	rt, _ := e.Payload["resultType"].(string)
	return c, rt, true
}

// TextDelta returns the streamed text fragment when Kind=="text_delta".
func (e Event) TextDelta() (string, bool) {
	if e.Kind != "text_delta" {
		return "", false
	}
	d, _ := e.Payload["delta"].(string)
	return d, true
}

// ToolCall returns (toolName, toolCallID) when Kind=="tool_call".
func (e Event) ToolCall() (toolName, toolCallID string, ok bool) {
	if e.Kind != "tool_call" {
		return "", "", false
	}
	t, _ := e.Payload["toolName"].(string)
	id, _ := e.Payload["toolCallId"].(string)
	return t, id, true
}
