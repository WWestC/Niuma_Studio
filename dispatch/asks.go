package dispatch

// asks.go — the dispatcher's half of 向房主提问 (v2.5): the pending
// AskUserQuestion reverse requests. The room-facing projection lives
// in chat/questions.go (the hub's open set + the question frames);
// this file owns the waiting side — parsing the request's structured
// questions, parking the onReverse goroutine until the owner answers
// (the workbench's answer verb, routed through the fleet) or the
// window expires, and handing the chosen answers back in exactly the
// shape the app-server's ask-question broker normalizes into the
// tool result (content.answers keyed by question text).

import (
	"fmt"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// pendingAsk is one registered question: the parsed asks (validation
// + the say line already spoken) and the single-slot reply channel
// the parked onReverse goroutine selects on. done/outcome carry the
// verdict to the ask's RE-ANNOUNCEMENT waiters: ZCode ≥3.14's
// app-server re-sends an unanswered requestUserInput under fresh
// request ids (1s→10s backoff, withInteractionRequestRecovery) until
// any one id is answered — each re-announcement parks on the same
// pendingAsk instead of minting a new card, and every waiter returns
// the same verdict (the first reply resolves the server entry; the
// rest land on an already-cleaned id, harmlessly ignored).
type pendingAsk struct {
	asks []chat.QuestionAsk
	sig  string
	ch   chan askReply
	// done closes exactly once, after outcome is fixed — the original
	// waiter's goroutine settles on whichever select branch fires.
	done    chan struct{}
	outcome *askReply
	// expiryReason is the decline reason this pa's window burns with —
	// set at mint, naming THIS window (full askWindow or the 自动推进
	// 代行宽限), so a re-announcement waiter parked on a grace ask
	// never hears a full-window timeout text for a two-minute grace.
	expiryReason string
}

// settle fixes the ask's verdict (nil = expired or withdrawn) and
// wakes every re-announcement waiter. Called exactly once, from the
// original waiter's goroutine, before its own return.
func (pa *pendingAsk) settle(reply *askReply) {
	pa.outcome = reply
	close(pa.done)
}

// askSignature fingerprints one reverse request: session + prompt +
// the parsed questions. The app-server re-announces with the SAME
// params verbatim, so an unchanged signature while the ask is still
// pending means "this is the re-announcement of that ask", never "a
// coincidentally identical second question" (a session holds at most
// one requestUserInput open at a time — the tool call blocks the
// turn).
func askSignature(sessionID, prompt string, asks []chat.QuestionAsk) string {
	var b strings.Builder
	b.WriteString(sessionID)
	b.WriteByte(0)
	b.WriteString(prompt)
	for _, a := range asks {
		b.WriteByte(1)
		b.WriteString(a.Question)
		b.WriteByte(2)
		b.WriteString(a.Header)
		if a.Multi {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
		for _, o := range a.Options {
			b.WriteByte(3)
			b.WriteString(o.Label)
			b.WriteByte(4)
			b.WriteString(o.Desc)
		}
	}
	return b.String()
}

// pendingAskBySig finds the still-pending ask with this signature —
// the re-announcement detector.
func (d *Dispatcher) pendingAskBySig(sig string) *pendingAsk {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, pa := range d.asks {
		if pa.sig == sig {
			return pa
		}
	}
	return nil
}

// askAcceptResult is the answered ask's wire verdict, shared by the
// original waiter and every re-announcement waiter.
func askAcceptResult(answers map[string]string) map[string]any {
	return map[string]any{"action": "accept",
		"content": map[string]any{"answers": answers}}
}

// beginPermPark registers the session+tool permission park: the first
// caller gets first=true and owns the room line and the window;
// re-announcements of the same still-open request get the original's
// done channel and wait for the same verdict.
func (d *Dispatcher) beginPermPark(sig string) (ch chan struct{}, first bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cur, ok := d.permParks[sig]; ok {
		return cur, false
	}
	ch = make(chan struct{})
	d.permParks[sig] = ch
	return ch, true
}

// endPermPark retires the owning park and wakes its re-announcement
// waiters (keyed delete first: a request arriving after the window
// closed is a genuinely new ask and deserves its own line).
func (d *Dispatcher) endPermPark(sig string, ch chan struct{}) {
	d.mu.Lock()
	if cur, ok := d.permParks[sig]; ok && cur == ch {
		delete(d.permParks, sig)
	}
	d.mu.Unlock()
	close(ch)
}

// autoAskFold is the 自动推进 ask line's repeat-fold window (seconds):
// the same signature inside it is a repeat — a re-announcement of the
// request or the model re-asking what was just let through — not a new
// question. Repeats refresh the stamp, so a persistent re-ask loop
// stays folded as long as it keeps re-asking.
const autoAskFold = 10 * 60

// autoAskFirst records this ask signature and reports whether it is a
// first sighting inside the fold window — the caller owns the room
// line and the term pair; repeats ride silently. Prunes stamps past
// the window while it holds the lock (resumeMark's lazy-prune shape).
func (d *Dispatcher) autoAskFirst(sig string) bool {
	now := util.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for s, at := range d.autoAskSeen {
		if now-at > autoAskFold {
			delete(d.autoAskSeen, s)
		}
	}
	_, seen := d.autoAskSeen[sig]
	d.autoAskSeen[sig] = now
	return !seen
}

// askReply is the owner's landed answer: who clicked and the chosen
// answers keyed by question text.
type askReply struct {
	by      string
	answers map[string]string
}

// sanitize keeps only the answers that actually match one of the
// pending questions, trimmed and non-empty — an empty result means
// the click carried nothing answerable and must not consume the ask.
// An unstructured ask (no parsed questions, the tool-shape fallback)
// accepts any non-empty entry.
func (pa *pendingAsk) sanitize(answers map[string]string) map[string]string {
	clean := map[string]string{}
	if len(pa.asks) == 0 {
		for k, v := range answers {
			if k = strings.TrimSpace(k); k != "" {
				if v = strings.TrimSpace(v); v != "" {
					clean[k] = v
				}
			}
		}
		return clean
	}
	for _, ask := range pa.asks {
		if v := strings.TrimSpace(answers[ask.Question]); v != "" {
			clean[ask.Question] = v
		}
	}
	return clean
}

// Answer lands the owner's choice on a pending question: validated
// against the registered asks, first-come takes the entry (a second
// click or a late click after expiry finds nothing), and the parked
// onReverse goroutine wakes holding the answers. false = no such
// open question, or the payload answered nothing (the card stays
// clickable — a misclick with an empty 其他 box must not burn it).
func (d *Dispatcher) Answer(qid, by string, answers map[string]string) bool {
	d.mu.Lock()
	pa := d.asks[qid]
	if pa == nil {
		d.mu.Unlock()
		return false
	}
	clean := pa.sanitize(answers)
	if len(clean) == 0 {
		d.mu.Unlock()
		return false
	}
	delete(d.asks, qid)
	d.mu.Unlock()
	// r_19 修订：点卡即在场证据——同一房里的代行宽限与提案代收在
	// 下一拍让位于房主。
	d.hub.NoteOwnerActivity()
	pa.ch <- askReply{by: by, answers: clean}
	return true
}

// takeAsk removes and returns a pending ask (the timeout/stop paths'
// cleanup — a racing Answer that already took it reads as nil here).
func (d *Dispatcher) takeAsk(qid string) *pendingAsk {
	d.mu.Lock()
	defer d.mu.Unlock()
	pa := d.asks[qid]
	delete(d.asks, qid)
	return pa
}

// parseQuestionAsks maps the request params' questions array — the
// app-server forwards the AskUserQuestion tool's own shape (YZa:
// {question, header, multiSelect, options:[{label, description,
// preview, value}]}) — into the card payload. Unparseable input
// yields nil and the card degrades to a free-text field.
func parseQuestionAsks(v any) []chat.QuestionAsk {
	raw, ok := v.([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	asks := make([]chat.QuestionAsk, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ask := chat.QuestionAsk{
			Question: str(m["question"]),
			Header:   str(m["header"]),
			Multi:    truthy(m["multiSelect"]),
		}
		if ask.Question == "" {
			continue
		}
		if opts, ok := m["options"].([]any); ok {
			for _, o := range opts {
				om, ok := o.(map[string]any)
				if !ok {
					continue
				}
				op := chat.QuestionOption{Label: str(om["label"]), Desc: str(om["description"])}
				if op.Label == "" {
					op.Label = str(om["value"])
				}
				if op.Label != "" {
					ask.Options = append(ask.Options, op)
				}
			}
		}
		asks = append(asks, ask)
	}
	if len(asks) == 0 {
		return nil
	}
	return asks
}

// str / truthy are the tolerant map-value readers parseQuestionAsks
// shares with the wider reverse-params handling.
func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

// askLineText is the member's own say line beside the card — the
// transcript's durable copy (question frames never enter history, so
// without this line a reload would leave the answered ask invisible
// and the follow-up reply contextless). One compact line: the first
// question, its option labels, and where to click. owner (the room's
// owner name, "" when unset) turns the lead into a real @-mention so
// the room switcher's 有人@我 beacon lights while the card waits.
func askLineText(q *chat.Question, owner string) string {
	return askLineW(q, owner, askWindow, "")
}

// askAutoLineText is the 代行宽限 variant — the same say line, but the
// window is the grace and the tail says what autopilot does when it
// burns. r_19 修订 makes the beacon honest again: the card self-expires
// on a countdown the owner can beat (a present owner answers in
// seconds, an absent one loses only the grace), so lighting it no
// longer demands an owner the mode declared away.
func askAutoLineText(q *chat.Question, owner string, grace time.Duration) string {
	return askLineW(q, owner, grace, i18n.S("；自动推进中，逾期按合理假设放行"))
}

// askLineW is askLineText's shared core: the window renders through
// humanWindow, tail ("" for the plain line) rides before the closing
// paren.
func askLineW(q *chat.Question, owner string, window time.Duration, tail string) string {
	hw := humanWindow(window)
	salut := i18n.S("向房主提问")
	if owner != "" {
		salut = i18n.Sf("@%s 需要你确认", owner)
	}
	if len(q.Asks) == 0 {
		lead := q.Prompt
		if lead == "" {
			lead = i18n.S("（见问题卡）")
		}
		return i18n.Sf("（%s：%s——在问题卡上直接填写，%s内有效%s）", salut, lead, hw, tail)
	}
	first := q.Asks[0]
	lead := first.Question
	if len(q.Asks) > 1 {
		lead = i18n.Sf("共%d问，首问「%s」", len(q.Asks), first.Question)
	}
	if len(first.Options) > 0 {
		labels := make([]string, 0, len(first.Options))
		for _, o := range first.Options {
			labels = append(labels, o.Label)
		}
		return i18n.Sf("（%s：%s——可选：%s；点问题卡上的选项即可，%s内有效%s）",
			salut, lead, strings.Join(labels, " / "), hw, tail)
	}
	return i18n.Sf("（%s：%s——点问题卡直接填写，%s内有效%s）", salut, lead, hw, tail)
}

// askAutoDigest is the question's carry-on for the 自动推进 room line:
// the notice says WHAT was let through — the first question (or the
// bare prompt when unstructured), its option labels along, "等 N 问"
// when the card held more. Rune-capped so a monster prompt can't
// balloon the system line (the full text lives in the term's ask row).
// Empty when the request carried nothing readable.
func askAutoDigest(prompt string, asks []chat.QuestionAsk) string {
	lead := strings.TrimSpace(prompt)
	if len(asks) > 0 {
		lead = asks[0].Question
	}
	if lead == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("「")
	b.WriteString(clipAskRunes(lead, askDigestRunes))
	b.WriteString("」")
	if len(asks) > 1 {
		b.WriteString(i18n.Sf("等 %d 问", len(asks)))
	}
	if len(asks) > 0 && len(asks[0].Options) > 0 {
		labels := make([]string, 0, len(asks[0].Options))
		for _, o := range asks[0].Options {
			labels = append(labels, o.Label)
		}
		b.WriteString(i18n.Sf("（可选：%s）", strings.Join(labels, " / ")))
	}
	return b.String()
}

// askDigestRunes caps the digest's question text.
const askDigestRunes = 80

// clipAskRunes keeps the first n runes plus an ellipsis — a byte cut
// would dice a Chinese sentence in half mid-character.
func clipAskRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// humanWindow renders a duration the say line can quote ("30 分钟",
// "45 秒") — the label follows askWindow, so tests that shrink the
// window stay readable too.
func humanWindow(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return i18n.Sf("%d 小时", int(d/time.Hour))
	case d >= time.Minute && d%time.Minute == 0:
		return i18n.Sf("%d 分钟", int(d/time.Minute))
	default:
		return i18n.Sf("%d 秒", int(d/time.Second))
	}
}

// mintAsk registers the card, the ask say line and the pendingAsk —
// the shared mint leg of both ask windows (the normal 30-minute window
// and the 自动推进 代行宽限). expiryReason is the decline reason both
// the window-burnt branch and every re-announcement waiter return; it
// names THIS pa's window so a grace expiry never masquerades as a
// full-window timeout. lineFor sees the minted question (its asks) so
// the caller picks the plain or the 代行 line. The caller owns the
// wait select and the window-burnt theater.
func (d *Dispatcher) mintAsk(m *member, prompt string, asks []chat.QuestionAsk, sig string,
	window time.Duration, lineFor func(q *chat.Question, owner string) string, expiryReason string) (*chat.Question, *pendingAsk) {
	qid := fmt.Sprintf("q_%d", time.Now().UnixNano())
	now := util.Now()
	q := &chat.Question{ID: qid, From: m.name, TS: now,
		Due:    now + int64(window/time.Second),
		Prompt: prompt, Asks: asks}
	pa := &pendingAsk{asks: asks, sig: sig, expiryReason: expiryReason,
		ch: make(chan askReply, 1), done: make(chan struct{})}
	// Register the pendingAsk BEFORE the card becomes visible: a
	// re-announcement that arrives the instant the card does must find
	// the sig entry and park, never fall through to the fold decline.
	d.mu.Lock()
	d.asks[qid] = pa
	d.mu.Unlock()
	d.hub.AskQuestion(q)
	// r_17：卡出面即落 ask 行（再宣告不重记——上面的 sig 检查已挡）。
	d.termAskNote(m.name, prompt)
	// 提问行的称呼用真 @（房主名）：房间切换栏的「有人@我」信标随
	// 卡亮起，别的房里等的提问不会被错过
	own := ""
	if om, ok := d.hub.OwnerMember(); ok {
		own = om.Name
	}
	d.sayThrough(m, lineFor(q, own), nil)
	d.stampRowUnread(m.name, d.sessionOf(m))
	return q, pa
}

// resolveAskAnswer is the answered branch both windows share: the card
// closes as answered, the member's terminal gets the digest, every
// waiter settles on the same verdict, and the tool result carries the
// answers keyed by question text.
func (d *Dispatcher) resolveAskAnswer(name string, q *chat.Question, pa *pendingAsk, reply askReply) map[string]any {
	d.hub.ResolveQuestion(q.ID, "answered", reply.by, reply.answers)
	// accept.content.answers keyed by question text is exactly what
	// the app-server's ask-question broker normalizes back into the
	// tool result (normalizeAskUserQuestion Answers reads
	// content.answers first).
	d.termAnswer(name, i18n.Sf("房主应答：%s", askAnswerDigest(reply.answers)), true)
	pa.settle(&reply)
	return askAcceptResult(reply.answers)
}
