// Package chat implements the in-memory chat room core: members,
// message broadcast, history and the WebSocket-facing data types.
package chat

import (
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/media"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/wire"

	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

const (
	historyCap = 100
	sendBuffer = 128
	maxNameLen = 24
	maxRoleLen = 24
	maxTextLen = 2000

	// departedCap bounds the "recently left" list the blackboard
	// renders as offline members; the oldest name is evicted first.
	departedCap = 32

	// PresenceGrace is how long a disconnected member keeps its seat —
	// roster entry, pixel avatar, @-mention resolution — before the
	// leave is broadcast. It exists for the agent `wait` long-poll
	// cycle: the wait process exits the moment it is @-mentioned (that
	// exit is what wakes the AI), then the agent replies and returns to
	// waiting; during that gap the member must not visibly flicker out
	// of the room. A same-name join inside the grace window silently
	// reclaims the seat (no join/leave events at all).
	PresenceGrace = 10 * time.Minute

	// MentionAllToken in a say ("@所有人") addresses the whole room:
	// the message's mentions expand to every other live member, and a
	// roll-call starts — each addressed member must say something
	// within MentionAckWindow or the room gets a system notice naming
	// the holdouts. The owner (SetOwner) is exempt.
	MentionAllToken  = "所有人"
	MentionAckWindow = 10 * time.Minute
)

type Hub struct {
	mu        sync.Mutex
	clients   map[*Client]struct{}
	observers map[*Client]struct{} // seatless broadcast subscribers (v2 P1)
	history   []Message
	grace     time.Duration
	ghosts    map[string]*ghostEntry
	sweeping  bool
	lastSeq   int64 // monotonic message order, see Message.Seq

	// pend queues the room's history lines for the ordered flusher
	// goroutine (chat/history.go): the say path marshals and enqueues
	// under mu — the open/write/close IO itself left the critical
	// section. Nil until SetHistoryPath (in-memory rooms append
	// nothing). Reads that walk the FILE drain first (FlushHistory).
	pend chan historyWrite

	// ledgerSave queues the three seq-ledgers' full-file rewrites for
	// the ordered ledger flusher (the pend sibling, chat/history.go):
	// the mark paths (MarkRead/MarkAck/MarkReact) marshal under mu and
	// enqueue — the tmp+fsync+rename IO leaves the critical section
	// (these saves used to be the flusher's unfinished half: history
	// went async, the ledgers still serialized the room on disk). Nil
	// until SetHistoryPath; file readers and exit paths drain first
	// (FlushLedgers).
	// ledgerSwapMu guards the ledgerSave FIELD (the channel object is
	// queue-safe by itself): tests re-point the channel to stall this
	// flusher and the loop snapshots it per wake. The flusher never
	// takes h.mu (drainLedgersLocked holds it across blocking enqueues),
	// so the field gets its own tiny lock — order is one-way
	// h.mu → ledgerSwapMu.
	ledgerSave   chan ledgerWrite
	ledgerSwapMu sync.Mutex

	// pendSwapMu guards the pend FIELD the same way (the out-of-lock
	// drains swap the queue: producers read the field under h.mu, the
	// flusher snapshots it per wake under this tiny lock — one-way
	// order h.mu → pendSwapMu, mirroring ledgerSwapMu).
	pendSwapMu sync.Mutex
	// holding/pendHold is the drain window's lossless buffer: while a
	// durable rewrite (reset/prune) drains the OLD queue outside h.mu,
	// new history lines park here instead of racing the file swap.
	holding  bool
	pendHold []historyWrite
	// ledgerHold parks the drain window's ledger enqueues as dirty
	// (staleness-never-loss; the reset clears the flags when the new
	// world's state is in place).
	ledgerHold bool
	// durableDrainMu serializes each durable rewrite's whole
	// swap→barrier span: two interleaved swaps could retire a channel
	// its consumer never migrates through (barrier forever). One
	// rewriter at a time, each retirement fully consumed first.
	durableDrainMu sync.Mutex

	// snapshots of members who truly left (leave after grace, or kick)
	// and have not come back; the blackboard shows them as offline so a
	// member without a saved AI config does not just vanish.
	departed      map[string]Member
	departedOrder []string // insertion order, oldest first

	// LookFn serves the lifetime look layer (r_05 t_126): the staffing
	// truth projected into the chat package (the store itself stays
	// outside chat to keep the package graph clean). Nil = every member
	// renders with the legacy two-field name-hash look (兼容).
	LookFn func(name string) *Look

	// PausedFn serves the room's pause state on the welcome frame
	// (hydration for late joiners — the truth lives in the staffing
	// settings, the flip's live sync rides the "pause" broadcast).
	// Nil = never paused (the pre-pause behavior).
	PausedFn func() bool

	// owner is the exempt name for @所有人 roll-calls (the local
	// human). Empty means nobody is exempt.
	owner string

	// ownerClient is the owner's own seat (the GUI player client),
	// registered once at boot. The seatless mirror connection speaks
	// through it, so a relayed owner line carries the owner's real name
	// without the hook CLI taking a "-2" phantom seat (v0.6 M1).
	ownerClient *Client

	// ownerActiveTS is the owner's last observed activity in THIS room
	// (unix seconds; 0 = never). It is the evidence leg of the r_19
	// 修订（自动推进是缺席缺省，不是失聪声明）: owner says and owner
	// card-clicks stamp it, and the owner-gates that autopilot would
	// otherwise act on unilaterally (ask cards, proposal auto-accept)
	// defer to a fresh stamp — a mode bit never outranks the observed
	// fact that the owner is right here. In-memory on purpose, the
	// question ledger's liveness discipline: a stamp that outlived its
	// observers would advertise an owner who may be long gone, so a
	// restart reads as absent (the conservative side).
	ownerActiveTS int64

	// ackWindow is how long @所有人-addressed members have to reply
	// before the holdout notice fires (tests shrink it).
	ackWindow time.Duration
	allReqs   []*allReq

	// historyPath, when set, persists the chat history across restarts
	// (v2 P3-c): every history-recorded frame APPENDS one jsonl line to
	// the file (history/<key>.jsonl), boot loads its tail back into the
	// ring. Empty = in-memory only (tests).
	historyPath string

	// reads is the room's read-receipt ledger (飞书式已读, chat/reads.go):
	// which members have had each say line injected into their session.
	// All access under mu; persistence derives from historyPath.
	reads readLedger

	// trace is the room's 工作过程 ring (chat/trace.go): per-member
	// bounded trails of displayable session moments (think/draft/tool/
	// model/turn/error). Written by the dispatcher's tap through
	// AppendTrace, read by GET /p/{key}/trace; deliberately in-memory
	// only — a cold panel rehydrates what it can and simply starts
	// living from the next flush.
	trace    map[string][]TraceEntry
	traceSeq int64

	// term is the room's 终端转录 (r_17, chat/term.go): the per-member
	// complete I/O journal — input rows (injected lines), output rows
	// (the same tap flushes the trace ring eats, uncapped) and ask/
	// answer/sys rows, folded with the ring's semantics but append-only
	// on disk (terminal/<key>/<名>.jsonl) and bounded in memory. Read
	// by GET /p/{key}/term; the "term" frame is its live feed.
	term      map[string][]TraceEntry
	termLast  map[string]termTail
	termCalls map[string]map[string]int64
	termSeq   int64
	termDir   string // "" = memory mirror only (in-memory tests)

	// termSave queues transcript-line batches for the term flusher
	// (term.go): AppendTerm marshals under mu and enqueues — the
	// per-entry OpenFile/Write/Close left the critical section (the
	// history pend's discipline). The flusher never takes mu; barriers
	// ride the FIFO tail (FlushTerm). Nil until SetTermDir arms a
	// directory (memory-only rooms never write).
	termSave chan termWrite
	// termWriteMu serializes term FILE mutations between the flusher's
	// appends and the prune rewrites (PruneTerm's file leg) — it never
	// nests with mu (the flusher is mu-free by rule).
	termWriteMu sync.Mutex

	// queues is the room's delivery-queue projection (排队中,
	// chat/queues.go): which members currently have which say lines
	// parked on their next-turn lane — the chip's middle state between
	// 未读 and 已读. All access under mu; deliberately NOT persisted
	// (the dispatcher's inbox lanes are the durable truth; adoption
	// re-derives and re-pushes the set).
	queues queueLedger

	// acks is the room's ack-receipt ledger (飞书式收到, chat/acks.go):
	// which members have acknowledged each say line — the @-loop
	// breaker. Same discipline as reads (all access under mu;
	// persistence derives from historyPath).
	acks ackLedger

	// reacts is the room's emoji-reaction ledger (飞书式表情回应,
	// chat/reacts.go): which members reacted which emoji to each
	// say/report line — an opinion about the message, never a new chat
	// message. Same discipline as reads/acks (all access under mu;
	// persistence derives from historyPath).
	reacts reactLedger

	// questions is the room's open interactive-question set (向房主
	// 提问, chat/questions.go): the asks whose AskUserQuestion reverse
	// requests are still pending on the dispatcher. Same discipline as
	// reads/acks (all access under mu) but deliberately NOT persisted —
	// an open question cannot outlive the dispatcher waiting on it.
	questions questionLedger

	// sayFeed carries delivered say/report lines to OnSay observers
	// (v1.0 dispatcher). The push happens on the hub lock but the
	// observers run on their own pump goroutine — an observer may call
	// back into the hub (the dispatcher mirrors a reply) without
	// deadlocking on mu. Overflow drops (a stalled observer must never
	// stall the room); ordering is per-pump FIFO.
	sayFeed      chan Message
	sayObservers []func(Message)
}

func NewHub() *Hub {
	h := &Hub{
		lastSeq:   time.Now().UnixMilli(), // restart-safe floor for Seq
		clients:   make(map[*Client]struct{}),
		observers: make(map[*Client]struct{}),
		grace:     PresenceGrace,
		ghosts:    make(map[string]*ghostEntry),
		departed:  make(map[string]Member),
		ackWindow: MentionAckWindow,
		sayFeed:   make(chan Message, 256),
	}
	go h.pumpSayFeed()
	return h
}

// OnSay registers an observer for every delivered say/report line
// (system lines excluded), called asynchronously on the feed pump
// goroutine. Observers may call back into the hub. Registration after
// lines were said misses those lines — attach observers at boot.
func (h *Hub) OnSay(obs func(Message)) {
	h.mu.Lock()
	h.sayObservers = append(h.sayObservers, obs)
	h.mu.Unlock()
}

// pumpSayFeed drains the say feed into the registered observers, one
// line at a time (FIFO, no concurrency between observers). Each
// observer call rides a panic fence — the comment used to claim "an
// observer panic must not kill the pump" without a recover to back
// it; the fence makes it true (one bad observer skips its line, the
// room keeps feeding the rest).
func (h *Hub) pumpSayFeed() {
	for msg := range h.sayFeed {
		h.mu.Lock()
		obs := make([]func(Message), len(h.sayObservers))
		copy(obs, h.sayObservers)
		h.mu.Unlock()
		for _, fn := range obs {
			util.Guard("chat: say observer", func() { fn(msg) })
		}
	}
}

func (h *Hub) SetOwner(name string) {
	h.mu.Lock()
	h.owner = name
	h.mu.Unlock()
}

// SetOwnerSeat registers the owner's own seat (the GUI player client)
// and its name. The seatless mirror connection (v0.6 M1) speaks through
// this seat; call it once at boot, right after the owner joins.
func (h *Hub) SetOwnerSeat(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ownerClient = c
	h.owner = c.member.Name
}

// OwnerPresenceWindow is how long owner activity keeps the owner
// "present" for owner-gate deferral — while fresh, ask cards under
// 自动推进 fall back to the normal full window and the proposal
// auto-accept engine holds its landings. Deliberately shorter than
// askWindow (an owner who spoke 20 minutes ago may well have stepped
// away) and longer than a reading gap. A var (not a const) purely as
// the tests' seam (askWindow's discipline).
var OwnerPresenceWindow = 10 * time.Minute

// NoteOwnerActivity stamps the owner present in this room right now.
// The say leg stamps itself inside sayLike (any line spoken through
// the owner's seat, mirror included); this method is the click leg —
// Dispatcher.Answer calls it when an owner answer lands on a card.
func (h *Hub) NoteOwnerActivity() {
	h.mu.Lock()
	h.ownerActiveTS = util.Now()
	h.mu.Unlock()
}

// OwnerActiveWithin reports whether the owner was seen active in this
// room within d (never true before the first stamp — the conservative
// read: no evidence, no owner).
func (h *Hub) OwnerActiveWithin(d time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ownerActiveTS == 0 {
		return false
	}
	return util.Now()-h.ownerActiveTS < int64(d/time.Second)
}

// OwnerMember returns the owner seat's member info — the handshake echo
// for the seatless mirror connection. ok is false without a registered
// seat.
func (h *Hub) OwnerMember() (Member, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ownerClient == nil {
		return Member{}, false
	}
	return h.ownerClient.member, true
}

// shirtPalette 飞书系亮色板（与 web/ui/dom.js 的 SHIRT_PALETTE 孪生：同
// fnv1a-32 哈希、同顺序同 hex——一人一色，UI 字母头像、排期泳道与像素小
// 人衬衫处处同色）。品牌蓝/绿/橙/红＋同族亮色。
var shirtPalette = []string{
	"#3370ff", "#00a9ff", "#00b392", "#34c724", "#7ac70c",
	"#ff8800", "#f54a45", "#f75cb4", "#9e6bff", "#5856d6",
}

var hairPalette = []string{
	"#3b2f2f", "#7a4a2b", "#a8763e", "#57534e", "#8a5a3c",
	"#4a3a5c", "#6b4226", "#2f3b46",
}

// paletteFor derives stable shirt/hair colors from the name, so the
// same agent always looks the same across restarts.
func paletteFor(name string) (shirt, hair string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	n := h.Sum32()
	return shirtPalette[n%uint32(len(shirtPalette))], hairPalette[(n/7)%uint32(len(hairPalette))]
}

// MemberHair returns name's stable hair color — the hair slot the
// palette derives, the same hex a joined Member.Hair carries (r_05
// t_126: the art face's who= lookup shares this truth).
func MemberHair(name string) string {
	_, hair := paletteFor(name)
	return hair
}

// MemberColor returns name's stable member color — the shirt slot the
// palette derives, the same hex a joined Member.Color carries (v2 P4-c).
// The composition root (the server) uses it to paint non-room
// projections — the schedule view's lanes/bars — so a person is one
// color on every face, seat or no seat.
func MemberColor(name string) string {
	shirt, _ := paletteFor(name)
	return shirt
}

// sanitize trims and clamps a display name, falling back to a default.
func sanitize(name, fallback string) string {
	if name == "" {
		name = fallback
	}
	r := []rune(name)
	if len(r) > maxNameLen {
		r = r[:maxNameLen]
	}
	out := string(r)
	if out == "" {
		out = fallback
	}
	return out
}

// sanitizeRole trims and clamps a role; empty means "no role given".
func sanitizeRole(role string) string {
	r := []rune(role)
	if len(r) > maxRoleLen {
		r = r[:maxRoleLen]
	}
	return string(r)
}

// Join registers a new member without a seat token (the legacy shape:
// one-shot clients, the local UI, tests). See JoinWithToken.
func (h *Hub) Join(rawName, rawRole string, replayHistory bool) (*Client, Member) {
	return h.JoinWithToken(rawName, rawRole, replayHistory, "")
}

// JoinWithToken is Join carrying a seat token (seat-M1, v0.10). See
// JoinProject for the project-named variant (v2 P1).
func (h *Hub) JoinWithToken(rawName, rawRole string, replayHistory bool, token string) (*Client, Member) {
	return h.JoinProject(rawName, rawRole, replayHistory, token, "")
}

// JoinProject is JoinWithToken naming the room project (v2 P1): the
// hello frame's project field (default "default" by the time the server
// calls this) rides the welcome back — additive, so an empty project
// keeps the legacy welcome shape byte-identical (no project field).
func (h *Hub) JoinProject(rawName, rawRole string, replayHistory bool, token, project string) (*Client, Member) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.joinLocked(rawName, rawRole, replayHistory, token, project)
}

// JoinFree is JoinProject that REFUSES instead of dedup-renaming when
// a live seat already holds the exact name under a different token
// (grace ghosts still reclaim by name — a no-token dial's right).
// ok=false: the name is live-held and not supersedeable by this
// token; NO seat was created, so the caller can never spawn a "-2"
// twin through this path. The dispatcher's re-seat and mirror paths
// ride on this guarantee; the same-token holder case falls through to
// joinLocked's supersede branch and swaps transports cleanly.
func (h *Hub) JoinFree(rawName, rawRole string, replayHistory bool, token string) (*Client, Member, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	name := sanitize(rawName, "guest")
	for old := range h.clients {
		if old.member.Name != name {
			continue
		}
		if token != "" && old.seatToken != "" && old.seatToken == token {
			continue // provably ours: joinLocked supersedes below
		}
		return nil, Member{}, false
	}
	c, m := h.joinLocked(rawName, rawRole, replayHistory, token, "")
	return c, m, true
}

func (h *Hub) joinLocked(rawName, rawRole string, replayHistory bool, token, project string) (*Client, Member) {

	name := sanitize(rawName, "guest")
	role := sanitizeRole(rawRole)
	var reclaimed bool

	// same-token takeover of a LIVE seat: detach the old connection
	// without Leave — no ghost handoff, no broadcast, the seat simply
	// changes transport
	if token != "" {
		for old := range h.clients {
			if old.member.Name == name && old.seatToken != "" && old.seatToken == token {
				delete(h.clients, old)
				old.closeReason = CloseReasonSuperseded
				close(old.done)
				// the seat stays occupied by the same member: no join
				// broadcast either — to the room nobody ever left
				reclaimed = true
				break
			}
		}
	}

	// reclaim a grace-period seat: same name AND (when the caller
	// carries a token) the same token — the ghost is provably theirs.
	// A mismatched token leaves the ghost in place; the newcomer's
	// name then collides with it and dedups to "-2" below.
	var reclaimToken string
	if g, ok := h.ghosts[name]; ok {
		if token == "" || g.token == "" || g.token == token {
			if role == "" {
				role = g.member.Role
			}
			reclaimToken = g.token // credential continuity across reclaims
			delete(h.ghosts, name)
			reclaimed = true
		}
	}
	// resolve collisions among live members — and, under seat-M1,
	// among un-reclaimed ghosts too (a stranger may not squat a seat
	// its owner's ghost still holds): Alice -> Alice-2 -> ...
	taken := make(map[string]struct{}, len(h.clients))
	for c := range h.clients {
		taken[c.member.Name] = struct{}{}
	}
	if _, stillGhost := h.ghosts[name]; stillGhost {
		taken[name] = struct{}{}
	}
	requested := name
	if _, dup := taken[name]; dup {
		for i := 2; ; i++ {
			cand := fmt.Sprintf("%s-%d", name, i)
			if g, ok := h.ghosts[cand]; ok {
				// a ghost holds the candidate: same-name reclaim right,
				// same rule as the original name — the member's own
				// retry (same credential, or the historical no-token
				// dial) takes its own ghost back instead of shadowing
				// it (never a live twin AND a same-name ghost at once,
				// the roster's "小狐-2(宽限)" residue); a mismatched
				// stranger moves on to the next suffix.
				if token == "" || g.token == "" || g.token == token {
					if token == "" && g.token != "" {
						reclaimToken = g.token // credential continuity
					}
					delete(h.ghosts, cand)
					name = cand
					break
				}
				continue
			}
			if _, dup := taken[cand]; !dup {
				name = cand
				break
			}
		}
	}

	shirt, hair := paletteFor(name)
	h.forgetDepartedLocked(name) // back in the room: no longer "left"
	m := Member{Name: name, Color: shirt, Hair: hair, Role: role}
	if h.LookFn != nil {
		if look := h.LookFn(name); look != nil {
			m.Look = look // 终身层投影（r_05）：staffing 档案说了算
		}
	}
	// seat credential: the caller's token when it presented one (the
	// member proving continuity); when a NO-token dial reclaimed a
	// grace ghost, the ghost's credential CONTINUES — a one-shot say or
	// report taking its seat back for a moment must not rekey it: the
	// member's persistent client still holds, and must still match, the
	// old token. Otherwise a fresh one. Welcome always carries the
	// seat's token so a persistent client can save it for its next
	// reconnect.
	if token == "" {
		token = reclaimToken
		if token == "" {
			token = newSeatToken()
		}
	}
	c := &Client{
		member:    m,
		send:      make(chan Message, sendBuffer),
		done:      make(chan struct{}),
		seatToken: token,
	}
	h.clients[c] = struct{}{}

	// newcomer's stream: welcome, optional history, then its own join
	c.trySend(Message{Type: MsgWelcome, You: &m, Members: h.membersLocked(), Token: token, Project: project, Paused: h.pausedNow(), Proto: wire.ProtoVersion, TS: util.Now()})
	if replayHistory {
		for _, hm := range h.history {
			c.trySend(hm)
		}
	}
	// A collision dedup renamed the join: make it room-visible and
	// record it in history, so a "-2" appearing (tonight's ghost/race
	// incidents) leaves a trail instead of a silent seat swap. Emitted
	// before the join broadcast; the renamed client itself already
	// learned its final name from the welcome frame.
	if name != requested {
		h.lastSeq++
		ren := Message{
			Type: MsgRename,
			From: requested,
			To:   name,
			Text: i18n.Sf("「%s」与在线成员同名，已以「%s」加入", requested, name),
			TS:   util.Now(),
			Seq:  h.lastSeq,
		}
		h.history = append(h.history, ren)
		if len(h.history) > historyCap {
			h.history = h.history[len(h.history)-historyCap:]
		}
		h.appendHistoryLocked(ren)
		h.broadcastLocked(ren)
	}
	if !reclaimed {
		h.broadcastLocked(Message{Type: MsgJoin, From: name, Member: &m, TS: util.Now()})
	}
	return c, m
}

// AttachObserver registers a seatless read-only connection (v2 P1, the
// Web workbench window's dial): no seat, no roster entry, no join/leave
// broadcast — it only receives everything the room broadcasts, plus the
// optional history replay. The handshake echo carries the roster (no
// "you": an observer holds no identity) and echoes the project back.
// Leave detaches it without a trace (no ghost, no broadcast).
// AttachObserver seats a seatless read-only subscriber. replayLimit
// (0 = 全量) caps how much history rides the welcome — the one live
// caller (the workbench observer dial) passes visitorReplay (50) for
// EVERY observer, the owner's own windows included (r_19 t_191's 收敛
// converged here): the cold window backfills the rest over HTTP
// /history, so the cap is a cold-open weight saving, not a read limit.
func (h *Hub) AttachObserver(project string, replayHistory bool, replayLimit int) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := &Client{
		send: make(chan Message, sendBuffer),
		done: make(chan struct{}),
	}
	h.observers[c] = struct{}{}
	c.trySend(Message{Type: MsgWelcome, Members: h.membersLocked(), Project: project, Paused: h.pausedNow(), Proto: wire.ProtoVersion, TS: util.Now()})
	if replayHistory {
		hist := h.history
		if replayLimit > 0 && len(hist) > replayLimit {
			hist = hist[len(hist)-replayLimit:] // 最近 N 条（旧的不补——外面收敛）
		}
		for _, hm := range hist {
			c.trySend(hm)
		}
	}
	return c
}

// Say broadcasts a chat line from c and records it in history. It
// returns the delivered message and whether it landed (the SayAsOwner
// shape) — the say-feed observers' tests and the delivery faces read
// the seq/mentions off it.
func (h *Hub) Say(c *Client, text string) (Message, bool) {
	delivered := h.sayLike(c, text, MsgSay, false, "", nil, nil)
	return delivered, delivered.Type != ""
}

// SayImages is Say with picture attachments (输入图片): images ride the
// line as media-warehouse references (rendered by clients via
// GET /media/{id}, injected into @-addressed members' turns as session
// attachments by the dispatcher). Text may be empty — an image-only
// say is a legal Feishu-style line.
func (h *Hub) SayImages(c *Client, text string, images []wire.Image) (Message, bool) {
	delivered := h.sayLike(c, text, MsgSay, false, "", images, nil)
	return delivered, delivered.Type != ""
}

// SayQuoted is the say write-face for a structured reply-quote (引用
// 回复)： the quote rides the message as fields — the text stays clean,
// so 复制/转发 never carry the引用头 along. The quote is normalized
// (normQuote) and, when At marks the reply-mention, the quoted name
// joins Mentions — 引用即点名 resolved by field, no @ needed in the
// body. images（输入图片） rides along exactly like SayImages.
func (h *Hub) SayQuoted(c *Client, text string, q *Quote, images []wire.Image) (Message, bool) {
	delivered := h.sayLike(c, text, MsgSay, false, "", images, normQuote(q))
	return delivered, delivered.Type != ""
}

// quoteSnipCap bounds a structured quote's snip — the renderer's legacy
// prefix parser parses snips up to this length, so a longer one would
// fall out of the legacy face if it were ever re-embedded; it also
// keeps a hostile client from smuggling an unbounded string past the
// quote-block UI.
const quoteSnipCap = 200

// normQuote validates a client-supplied quote: a quote without a From
// (the name the block shows and wakes) is not a quote, and the free-text
// slots are rune-capped. Nil-clean: nil in, nil out.
func normQuote(q *Quote) *Quote {
	if q == nil || q.From == "" {
		return nil
	}
	n := *q
	if n.Seq < 0 {
		n.Seq = 0
	}
	r := []rune(n.Snip)
	if len(r) > quoteSnipCap {
		r = r[:quoteSnipCap]
	}
	n.Snip = string(r)
	r = []rune(n.Via)
	if len(r) > 60 {
		r = r[:60]
	}
	n.Via = string(r)
	return &n
}

func (h *Hub) SayAsOwner(text string) (msg Message, ok bool) {
	return h.sayAsOwnerLike(text, nil)
}

// SayAsOwnerImages is SayAsOwner with picture attachments (输入图片) —
// the seatless owner write face's image-carrying say (the workbench's
// OwnerChannel; the CLI mirror line stays text-only).
func (h *Hub) SayAsOwnerImages(text string, images []wire.Image) (msg Message, ok bool) {
	return h.sayAsOwnerLike(text, images)
}

func (h *Hub) sayAsOwnerLike(text string, images []wire.Image) (msg Message, ok bool) {
	h.mu.Lock()
	c := h.ownerClient
	_, live := h.clients[c]
	h.mu.Unlock()
	if c == nil || !live {
		return Message{}, false
	}
	delivered := h.sayLike(c, text, MsgSay, false, "", images, nil)
	return delivered, delivered.Type != ""
}

// WhisperAsOwner speaks the owner's private line to one member (私信,
// manual v0.10 M4 占位落地): clamp + stamp ONLY — no broadcast, no
// history ring, no say-feed, no summons family. The whisper frame
// echoes to the owner's own client alone (the workbench's 🔒 receipt;
// observers and visitor dials never see it); delivery to the member
// is NOT this hub's business — the server routes it through its
// whisper sink (the Fleet lane) before echoing, so a refused delivery
// leaves no ghost receipt. ok=false = owner seat unavailable (the
// caller reports it, never fakes success).
func (h *Hub) WhisperAsOwner(to, text string) (Message, bool) {
	r := []rune(text)
	if len(r) > maxTextLen {
		r = r[:maxTextLen]
	}
	text = string(r)
	if text == "" || to == "" {
		return Message{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.ownerClient
	if c == nil {
		return Message{}, false
	}
	if _, live := h.clients[c]; !live {
		return Message{}, false
	}
	msg := Message{Type: MsgSay, Origin: OriginWhisper,
		From: c.member.Name, To: to, Text: text, TS: util.Now()}
	c.trySend(msg)
	return msg, true
}

// Report broadcasts a periodic task report from c, records it in
// history and stamps it as the member's latest status.
func (h *Hub) Report(c *Client, text string) {
	h.sayLike(c, text, MsgReport, true, "", nil, nil)
}

// sayLike is the one broadcast path behind say/report/mirror/quote:
// clamp, stamp, resolve mentions, record history, publish. It returns
// the delivered message (zero Message when the text was empty or the
// speaker holds no seat). images (输入图片) stamp onto the line and
// into history; a textless but image-carrying say passes — an
// image-only message is speakable, the clamp is on TEXT, not the
// line. quote (引用回复) rides as structured fields — the hub adds the
// quoted name to Mentions when At marks the reply-mention (引用即点
// 名), the text itself stays clean.
func (h *Hub) sayLike(c *Client, text, msgType string, isReport bool, origin string, images []wire.Image, quote *Quote) Message {
	r := []rune(text)
	// 截断回执（crowd-hint 的兄弟纪律「静默修剪不留 receipt」）：超长
	// 行被齐头截断会悄悄改变语义（断在半句与断在半词是两种话），必须
	// 让说话人知道尾巴没了——下行广播后给发送者本人一条私有系统提示。
	origLen := len(r)
	clamped := origLen > maxTextLen
	if clamped {
		r = r[:maxTextLen]
	}
	text = string(r)
	if text == "" && len(images) == 0 {
		return Message{}
	}
	if len(images) > media.MaxPerSay {
		images = images[:media.MaxPerSay]
	}
	quote = normQuote(quote)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return Message{}
	}
	ts := util.Now()
	// Owner presence stamp (r_19 修订's say leg): a line spoken through
	// the owner's seat is the cheapest honest evidence the owner is
	// here — member lines and reports never stamp.
	if h.owner != "" && c.member.Name == h.owner {
		h.ownerActiveTS = ts
	}
	if isReport {
		c.member.LastReport = text
		c.member.LastReportTS = ts
	}
	// Quote-prefix snip masking rides ahead of every text scan below:
	// the prefix's own @from still summons (引用即点名), @-names quoted
	// INSIDE the snip don't (they are the quoted person's words, not the
	// speaker's address — maskQuoteSnip). Legacy defense: structured
	// quotes (Message.Quote) no longer put the header into the text, but
	// a model echoing the injected quote face, or an old mirrored line,
	// still can.
	scan := maskQuoteSnip(text)
	mentions := h.mentionsLocked(scan)
	// 引用即点名（structured）： a reply-mention quote wakes the quoted
	// member by FIELD — the body carries no @, the wake needs no text
	// parsing. Per-name semantics: it rides through the summons gate
	// exactly like a literal @ in the body (单名点名不受回显豁免 — a
	// bridged reply quoting its line still delivers to the quoted peer).
	if quote != nil && quote.At && quote.From != "" {
		mentions = appendMention(mentions, quote.From)
	}
	// Summons gating: the wake/obligation family — all-address
	// expansion, the roll-call window, role-group wake — belongs to
	// LIVING input only. A bridged turn reply (origin=bridge) is a
	// deliverable, not a new instruction (the v1.0 ruling, manual
	// §bridge 回显): it keeps per-name mention resolution above (the
	// frontend highlight, the grace-seat refresh, wait-wakes on the
	// peers it literally @names) but an "@所有人"/"@全员"/"@岗位组"
	// token it QUOTES must not summon the room again — otherwise every
	// ack that echoes the roll-call word opens a phantom roll-call of
	// its own (the reply-storm's second engine). The owner's mirror
	// lines keep full summons semantics — the 房主 ruling 「会话里
	// 点名也算数」.
	summons := !isReport && origin != OriginBridge
	// All-address tokens (ARB-4 dual semantics): "@所有人" is a
	// roll-call — it expands the mentions to every other live member
	// (each wait/watchdog client wakes on its own name, zero client
	// changes) AND opens the 10-minute reply window; "@全员" is a
	// broadcast — same wake expansion, no reply obligation. Reports
	// never all-address.
	var rollcall []string
	if kind := allMentionKind(scan); summons && kind != allNone {
		for m := range h.clients {
			n := m.member.Name
			if n == c.member.Name || n == h.owner {
				continue
			}
			if kind == allRollCall {
				rollcall = append(rollcall, n)
			}
			mentions = appendMention(mentions, n)
		}
	}
	// Role-group wake (t_81/O-5): "@<岗位>组" wakes every live member
	// whose role taxonomy starts with the stem — broadcast semantics,
	// same family as "@全员", no reply window. Bridged replies are
	// exempt with the rest of the summons family (see above).
	if summons {
		for _, n := range h.groupWakeLocked(scan, c.member.Name) {
			mentions = appendMention(mentions, n)
		}
	}
	h.lastSeq++
	msg := Message{
		Type:     msgType,
		From:     c.member.Name,
		Text:     text,
		TS:       ts,
		Seq:      h.lastSeq,
		Mentions: mentions,
		Images:   images,
		Quote:    quote,
	}
	msg.Origin = origin
	// Someone is talking TO a grace-period member: refresh that seat's
	// deadline, so an active conversation keeps the (momentarily away)
	// member visible and @-addressable for as long as it is being
	// talked to — the AI-to-AI case where the peer is mid-processing.
	for _, name := range msg.Mentions {
		if g, ok := h.ghosts[name]; ok {
			g.deadline = time.Now().Add(h.grace)
		}
	}
	h.history = append(h.history, msg)
	if len(h.history) > historyCap {
		h.history = h.history[len(h.history)-historyCap:]
	}
	h.appendHistoryLocked(msg)
	h.broadcastLocked(msg)
	if msgType == MsgSay || msgType == MsgReport {
		select {
		case h.sayFeed <- msg:
		default: // overflow drops: a stalled observer never stalls the room
		}
	}

	// The truncation receipt (the clamp at entry): tell the speaker —
	// and only the speaker — that the tail is gone, before the room
	// reads the cut line as the speaker's complete thought. Every
	// origin rides the receipt: a human paste, a CLI novel, and a
	// bridged member reply that ran long all deserve to know.
	if clamped {
		c.trySend(Message{Type: MsgSystem, TS: util.Now(),
			Text: i18n.Sf("这条发言 %d 字超过 %d 字上限，尾部已被截断送达——过长的内容请拆分发送或写入知识库", origLen, maxTextLen)})
	}

	// The crowd-word miss nudge — the 岗位群呼未命中 hint's sibling
	// (「打错的呼唤绝不做静默 no-op」): a say by the owner that carries a
	// plain crowd word (大家/各位/全部人…) but resolved ZERO mentions woke
	// nobody — no token, no expansion, the line sank into background
	// lanes (房主实录: 「全部人都去查看」met total silence). Tell the
	// speaker, and only the speaker, before the silence reads as the
	// room ignoring the instruction. Owner-only: a member's no-@ lines
	// are the dispatcher's 补 @ discipline's domain, and a bridged
	// deliverable is never a summons (the bridge exemption family).
	if msgType == MsgSay && origin != OriginBridge && len(mentions) == 0 &&
		h.owner != "" && c.member.Name == h.owner && crowdAddressMiss(scan) {
		c.trySend(Message{Type: MsgSystem, TS: util.Now(),
			Text: i18n.S("这条发言没有 @ 到任何人，不会唤醒任何成员——不带 @ 的消息只进成员的背景车道，下次点名才捎带读到。全员点名用 @所有人，仅广播用 @全员。")})
	}

	if len(rollcall) > 0 {
		req := &allReq{
			by:       c.member.Name,
			ts:       ts,
			deadline: time.Now().Add(h.ackWindow),
			need:     make(map[string]bool, len(rollcall)),
			got:      make(map[string]bool, len(rollcall)),
		}
		for _, n := range rollcall {
			req.need[n] = true
		}
		// keep the pending list bounded: drop finished roll-calls
		kept := h.allReqs[:0]
		for _, old := range h.allReqs {
			if !old.done {
				kept = append(kept, old)
			}
		}
		h.allReqs = append(kept, req)
		h.broadcastLocked(Message{
			Type: MsgSystem,
			Text: i18n.Sf("@所有人点名已发起（%s）：等待 %d 人回应，窗口 %s", req.by, len(req.need), h.ackWindow),
			TS:   util.Now(),
		})
		time.AfterFunc(h.ackWindow, func() { h.expireAllReq(req) })
	}
	if !isReport {
		h.ackRollcallsLocked(c.member.Name)
	}
	return msg
}

const (
	allNone allKind = iota
	allBroadcast
	allRollCall
)

// System broadcasts an operator (non-member) line. The text rides
// through i18n.S: static Chinese lines are dictionary keys, so the
// announcement family flips language with the UI setting without
// per-call edits (composed lines migrate at their call sites with
// Sf). Member say-text NEVER passes through here — only system lines.
func (h *Hub) System(text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(Message{Type: MsgSystem, Text: i18n.S(text), TS: util.Now()})
}

// SystemRecorded broadcasts an operator message AND records it in
// history — for room-level events that outlive the moment (v0.6's
// restart notice: a member reconnecting after the takeover replays it
// and understands why the room blinked). Same shape as the rename
// trail: seq-stamped so cursors move past it.
func (h *Hub) SystemRecorded(text string) {
	h.SystemRecordedEvent("", text)
}

// SystemRecordedEvent is SystemRecorded with an event marker riding the
// frame ("notice", or legacy EventAgg lines already in lobby history).
// The marker is additive wire vocabulary: it persists into history so a
// replaying client sees the same badge-exempt semantics a live one does.
func (h *Hub) SystemRecordedEvent(event, text string) {
	h.recorded(Message{Type: MsgSystem, Event: event, Text: i18n.S(text)})
}

// SystemInterrupted records the restart's unfinished-work roll call
// (EventInterrupted): the seats the farewell snapshot caught mid-turn,
// riding the frame so the workbench renders a continue card — the
// plain Text stays readable for CLI and old clients.
func (h *Hub) SystemInterrupted(seats []InterruptedSeat, text string) {
	h.recorded(Message{Type: MsgSystem, Event: EventInterrupted, Interrupted: seats, Text: text})
}

// recorded is the SystemRecorded tail: seq-stamp, history append,
// disk trail, broadcast — one path for every recorded operator line.
func (h *Hub) recorded(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastSeq++
	msg.TS = util.Now()
	msg.Seq = h.lastSeq
	h.history = append(h.history, msg)
	if len(h.history) > historyCap {
		h.history = h.history[len(h.history)-historyCap:]
	}
	h.appendHistoryLocked(msg)
	h.broadcastLocked(msg)
}

// Broadcast delivers msg to every client without touching history —
// the reminder path for task events, which must stay out of the 100
// line chat context.
func (h *Hub) Broadcast(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(msg)
}

// SendTo delivers msg to one client (dropped when it is gone or
// stalled) — the private denial path for task operations, and the
// observer heartbeat's lane (serveObserver's ping rides it; an observer
// lives in h.observers, so a clients-only check dropped it on arrival).
func (h *Hub) SendTo(c *Client, msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		if _, ok := h.observers[c]; !ok {
			return
		}
	}
	c.trySend(msg)
}

// Leave removes the client and hands its seat to the presence grace:
// within the grace window the member stays in the roster (and keeps
// resolving @-mentions) and no leave is broadcast — a same-name Join
// reclaims the seat silently. Only when the grace expires does the
// sweeper broadcast the leave. Idempotent; grace 0 means immediate.
// An observer detaches here directly: no ghost, no broadcast — it
// never held a seat to hand over.
func (h *Hub) Leave(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.observers[c]; ok {
		delete(h.observers, c)
		close(c.done)
		return
	}
	if _, ok := h.clients[c]; !ok {
		return
	}
	m := c.member
	delete(h.clients, c)
	close(c.done)
	h.dropFromRollcallsLocked(m.Name)
	if h.grace > 0 {
		h.ghosts[m.Name] = &ghostEntry{member: m, deadline: time.Now().Add(h.grace), token: c.seatToken}
		h.startSweeperLocked()
		return
	}
	h.departLocked(m)
	h.broadcastLocked(Message{Type: MsgLeave, From: m.Name, TS: util.Now()})
}

// Kick removes the member with that display name (the operator-facing
// remove), bypassing the presence grace: the leave broadcasts at once
// whether the name is a live member or a ghost. The kicked client's
// Done channel closes so its transport can shut down.
func (h *Hub) Kick(name string) (Member, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var victim *Client
	for c := range h.clients {
		if c.member.Name == name {
			victim = c
			break
		}
	}
	if victim != nil {
		m := victim.member
		delete(h.clients, victim)
		close(victim.done)
		h.dropFromRollcallsLocked(m.Name)
		h.departLocked(m)
		h.broadcastLocked(Message{Type: MsgLeave, From: m.Name, TS: util.Now()})
		return m, true
	}
	if g, ok := h.ghosts[name]; ok {
		delete(h.ghosts, name)
		h.departLocked(g.member)
		h.broadcastLocked(Message{Type: MsgLeave, From: name, TS: util.Now()})
		return g.member, true
	}
	return Member{}, false
}

// Members returns the current roster, grace-period ghosts included
// (a ghost is still "in the room" as far as everyone else can see).
func (h *Hub) Members() []Member {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.membersLocked()
}

// pausedNow reports the room's pause state for the welcome frame (the
// staffing settings' truth through PausedFn; nil = never paused).
func (h *Hub) pausedNow() bool {
	return h.PausedFn != nil && h.PausedFn()
}

func (h *Hub) broadcastLocked(msg Message) {
	for c := range h.clients {
		c.trySend(msg)
	}
	// Observers ride every broadcast but nothing else: no roster, no
	// @-resolution, no roll-calls — those loops walk h.clients only.
	for c := range h.observers {
		c.trySend(msg)
	}
}
