package dispatch

// onreverse.go — 反向请求处理（权限/提问卡），读循环栈上的 app-server
// 请求扇出。拆自 dispatcher.go（v2.15 结构整理）。

import (
	"fmt"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// PermWindow is how long an "ask"-policy reverse request waits for
// the host before the fail-close default fires (Z-Deck measured 35s
// against the device countdown; the room keeps the same beat). A var
// (not a const) purely as the tests' throttle seam (askWindow's
// discipline).
var PermWindow = 35 * time.Second

// askWindow is the AskUserQuestion counterpart of PermWindow: a
// structured question is not a permission blip but a conversation
// the host answers when they see it, so the card stays clickable —
// and the member's turn parked — for half an hour before the
// fail-close decline. A var (not a const) purely as the tests'
// throttle seam (the recruitTick discipline).
var askWindow = 30 * time.Minute

// onReverse applies the permission policy to server→client requests.
// The zcode client answers each request on its own goroutine, so the
// ask-policy window may sleep here without stalling the wire.
func (d *Dispatcher) onReverse(method string, params map[string]any) (any, error) {
	sid, _ := params["sessionId"].(string)
	tool, _ := params["toolName"].(string)
	m := d.lookupBySession(sid)
	name := i18n.S("成员")
	if m != nil {
		name = m.name
	}
	perm := d.cfg.Perm
	switch method {
	case "interaction/requestPermission":
		// r_17 终端转录：一问一答成对落账（只记本房成员；再宣告不重记
		// ——首到已立问，等待者只回声同判）。双门（v2.9）：等权限的
		// 回合输出流停摆，问/答是抽屉里唯一的动静。
		if m != nil {
			d.emitTrace([]chat.TraceEntry{{From: name, Kind: chat.TraceAsk,
				Tool: tool, Text: clipMiddle(i18n.Sf("请求权限：%s", tool), 4096)}})
		}
		switch perm {
		case PermAllow:
			if m != nil {
				d.termAnswer(name, i18n.S("放行（策略 allow）"), true)
			}
			return map[string]any{"decision": "allow", "reason": "dispatch-policy-allow"}, nil
		case PermDeny:
			if m != nil {
				d.termAnswer(name, i18n.S("拒绝（策略 deny）"), false)
			}
			return map[string]any{"decision": "deny", "reason": "dispatch-policy-deny"}, nil
		}
		if d.autoAdvanceOn() {
			// 自动推进（r_19）：房主不介入——权限请求直接放行（PermAllow 同
			// 形，reason 标注来源），成员回合不被 35 秒窗口拖住。只开补货
			// 时房主在场，不走这条。
			if m != nil {
				d.termAnswer(name, i18n.S("放行（自动推进）"), true)
			}
			return map[string]any{"decision": "allow", "reason": "autopilot-allow"}, nil
		}
		// ZCode ≥3.14 re-announces the unanswered request under fresh
		// ids (1s→10s backoff): the first arrival owns the room line
		// and the window; every re-announcement waits beside it and
		// echoes the same verdict — one line, one countdown.
		park, first := d.beginPermPark("perm:" + sid + ":" + tool)
		if first {
			d.hub.System(i18n.Sf("[调度] %s 请求权限：%s（@房主 %s 内无处理将自动拒绝）",
				name, tool, PermWindow))
			select {
			case <-time.After(PermWindow):
			case <-d.stop:
			}
			d.endPermPark("perm:"+sid+":"+tool, park)
			if m != nil {
				d.termAnswer(name, i18n.Sf("权限窗口超时（%s），自动拒绝", PermWindow), false)
			}
			return map[string]any{"decision": "deny", "reason": "权限窗口超时，自动拒绝"}, nil
		}
		select {
		case <-park:
		case <-d.stop:
		}
		return map[string]any{"decision": "deny", "reason": "权限窗口超时，自动拒绝"}, nil
	case "interaction/requestUserInput":
		prompt, _ := params["prompt"].(string)
		switch perm {
		case PermAllow:
			if m != nil {
				d.termAskNote(name, prompt)
				d.termAnswer(name, i18n.S("放行（策略 allow，空应答）"), true)
			}
			return map[string]any{"action": "accept", "content": map[string]any{}}, nil
		case PermDeny:
			if m != nil {
				d.termAskNote(name, prompt)
				d.termAnswer(name, i18n.S("拒绝（策略 deny）"), false)
			}
			return map[string]any{"action": "deny", "reason": "dispatch-policy-deny"}, nil
		}
		// 解析与重宣告识别先行——两窗（正常全窗与自动推进代行宽限）
		// 共用同一条识别腿。ZCode ≥3.14 re-announces an unanswered ask
		// under fresh request ids (1s→10s backoff) until one is
		// answered — a re-announcement is NOT a new question: it parks
		// on the original pendingAsk (no second card, no second say
		// line, no second open entry) and echoes the same verdict.
		asks := parseQuestionAsks(params["questions"])
		sig := askSignature(sid, prompt, asks)
		if pa := d.pendingAskBySig(sig); pa != nil {
			select {
			case <-pa.done:
			case <-d.stop:
				return map[string]any{"action": "decline", "reason": "调度器关闭，提问取消"}, nil
			}
			if reply := pa.outcome; reply != nil {
				return askAcceptResult(reply.answers), nil
			}
			return map[string]any{"action": "decline", "reason": pa.expiryReason}, nil
		}
		// 自动推进（r_19 修订：代行不失聪——自动推进是缺席缺省，不是
		// 失聪声明）。房主在场证据（OwnerPresenceWindow 内的本房发言/
		// 点卡）永远优先于模式位：在场 → 落到下面的正常全窗出卡；
		// 缺席 → 照常出卡但只给代行宽限（与提案代收同一个 accept_delay
		// 旋钮、「代行等房主」全市一个宽限口径），宽限内点选答案直达
		// 成员，满窗未点才按合理假设放行＋房间一行系统留痕（r_19 的
		// 旧行为只是从「当场」移到「宽限满」）；同签名重复到达（客户端
		// 重宣告/模型反复追问同一问）在放行后的折叠窗内当场放行，不再
		// 开窗——一条一条刷屏没有信息量。
		if d.autoAdvanceOn() {
			if m == nil {
				// 会话不在调度名下：出不了卡也无人可等——维持 r_19 的
				// 当场放行（一行留痕带摘要，放行了什么要看得见）。
				if d.autoAskFirst(sig) {
					d.hub.System(i18n.Sf("[调度] 自动推进：%s 的提问%s按合理假设自动放行（房主不介入）",
						name, askAutoDigest(prompt, asks)))
				}
				return map[string]any{"action": "decline",
					"reason": "自动推进已开启：房主不介入，先按你自己的合理假设推进并注明"}, nil
			}
			if !d.hub.OwnerActiveWithin(chat.OwnerPresenceWindow) {
				if !d.autoAskFirst(sig) {
					return map[string]any{"action": "decline",
						"reason": "自动推进已开启，此问刚按合理假设放行：按该假设推进并注明，勿停工候复"}, nil
				}
				grace := d.autoAskGrace()
				q, pa := d.mintAsk(m, prompt, asks, sig, grace,
					func(q *chat.Question, own string) string { return askAutoLineText(q, own, grace) },
					fmt.Sprintf("自动推进宽限（%s）满房主未点选：按合理假设（优先推荐项）推进并注明，@房主 留一行可否决说明即可，勿停工候复", humanWindow(grace)))
				select {
				case reply := <-pa.ch:
					return d.resolveAskAnswer(name, q, pa, reply), nil
				case <-time.After(grace):
					d.takeAsk(q.ID)
					d.hub.ResolveQuestion(q.ID, "expired", "", nil)
					d.hub.System(i18n.Sf("[调度] 自动推进：%s 的提问%s宽限满未点选，按合理假设放行（房主可随时在房内纠正）",
						name, askAutoDigest(prompt, asks)))
					d.termAnswer(name, i18n.Sf("自动推进：宽限（%s）满房主未点选，按合理假设放行", humanWindow(grace)), false)
					pa.settle(nil)
					return map[string]any{"action": "decline", "reason": pa.expiryReason}, nil
				case <-d.stop:
					d.takeAsk(q.ID)
					d.hub.ResolveQuestion(q.ID, "expired", "", nil)
					d.termAnswer(name, i18n.S("调度器关闭，提问取消"), false)
					pa.settle(nil)
					return map[string]any{"action": "decline", "reason": "调度器关闭，提问取消"}, nil
				}
			}
			// 房主在场：让位正常全窗出卡（证据优先于模式位）。
		}
		if m == nil {
			d.hub.System(i18n.Sf("[调度] %s 提问：%s（该会话不在调度名下，无法出选项卡）", name, prompt))
			select {
			case <-time.After(PermWindow):
			case <-d.stop:
			}
			return map[string]any{"action": "decline", "reason": "提问窗口超时，自动取消"}, nil
		}
		// v2.5 向房主提问：the structured questions ride the request
		// params verbatim (question text, header, labeled options,
		// multiSelect — the AskUserQuestion tool's own shape, the
		// app-server maps them 1:1). The card broadcast + the member's
		// own say line (the durable transcript copy) both come from
		// the parsed form; the pending entry then parks HERE until
		// the owner clicks (Dispatcher.Answer through the fleet) or
		// askWindow expires — the tool call resolves in place either
		// way, so the member's turn continues holding the answer.
		q, pa := d.mintAsk(m, prompt, asks, sig, askWindow, askLineText,
			fmt.Sprintf("提问窗口超时（%s）房主未应答，先按你自己的合理假设推进并注明", askWindow))
		select {
		case reply := <-pa.ch:
			return d.resolveAskAnswer(name, q, pa, reply), nil
		case <-time.After(askWindow):
			d.takeAsk(q.ID)
			d.hub.ResolveQuestion(q.ID, "expired", "", nil)
			d.termAnswer(name, i18n.Sf("提问窗口超时（%s）房主未应答，按合理假设放行", askWindow), false)
			pa.settle(nil)
			return map[string]any{"action": "decline", "reason": pa.expiryReason}, nil
		case <-d.stop:
			d.takeAsk(q.ID)
			d.hub.ResolveQuestion(q.ID, "expired", "", nil)
			d.termAnswer(name, i18n.S("调度器关闭，提问取消"), false)
			pa.settle(nil)
			return map[string]any{"action": "decline", "reason": "调度器关闭，提问取消"}, nil
		}
	case "interaction/requestProviderRuntimeHeaders":
		// Account-plan models (providerId "account:…") carry no static
		// key in the app-server child's config — before EVERY model
		// request the child asks its host to supply the plan's auth.
		// Niuma answers from the desktop's own persisted keys
		// (~/.zcode/v2/config.json); without one the turn is refused
		// with the reason on the nose (headersApplied:false → -32031)
		// instead of the client's opaque fail-close error. Personal
		// providers self-contain their key and never ask — keep the
		// fail-close path for them and anything else.
		providerID, _ := params["providerId"].(string)
		if !strings.HasPrefix(providerID, "account:") {
			return nil, nil
		}
		if key := zcode.AccountAPIKey(providerID); key != "" {
			return map[string]any{
				"headersApplied": true,
				"requestAuth":    map[string]any{"apiKey": key},
			}, nil
		}
		return map[string]any{
			"headersApplied": false,
			"errorMessage":   "本机没有 " + providerID + " 的可用钥匙——请先在 ZCode 桌面端登录/连接该套餐",
		}, nil
	}
	return nil, nil // fail-close defaults in the client
}
