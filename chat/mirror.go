package chat

import "github.com/WWestC/Niuma_Studio/util"

// The owner's mirror path: lines quoted from the owner's mirrored
// session (origin=mirror) and the dispatcher's bridged turn replies
// (origin=bridge) — quoted, not spoken (MS4).

// SayMirror broadcasts c's line as an owner mirror message (v0.6 M1):
// the full say path plus the origin="mirror" marker — including FULL
// summons semantics (an @所有人 roll-call opens the reply window; the
// 房主 ruling 「会话里点名也算数」, manual §镜像消息 — an earlier
// draft degraded mirrors to broadcast; that clause is void). Only the
// owner's name earns the marker; anyone else calling this lands in a
// plain Say — the server-side downgrade, which simply means the wire
// omits origin for them.
func (h *Hub) SayMirror(c *Client, text string) {
	h.mu.Lock()
	owner := h.owner
	h.mu.Unlock()
	if owner == "" || c.Name() != owner {
		h.Say(c, text)
		return
	}
	h.sayLike(c, text, MsgSay, false, OriginMirror, nil, nil)
}

// SetWorkState stamps a member's turn-in-flight tell on their live
// seat and broadcasts the member_work reminder frame (no history, no
// badge, no @ — the read/ack reminder family). No live seat under the
// name is a no-op: ghosts never work anywhere, and a departed member's
// flag must not linger on the roster faces.
func (h *Hub) SetWorkState(name string, working bool, since int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.member.Name != name {
			continue
		}
		c.member.Working = working
		c.member.WorkingSince = since
		h.broadcastLocked(Message{Type: MsgMemberWork, From: name,
			Working: working, Since: since, TS: util.Now()})
		return
	}
}

// SayBridged (v1.0) relays a member's ZCode-session turn reply through
// their dispatcher seat: the full say path plus the origin="bridge"
// marker. Unlike SayMirror there is no owner-name gate — the caller is
// the dispatcher holding the member's own in-process seat.
func (h *Hub) SayBridged(c *Client, text string) {
	h.sayLike(c, text, MsgSay, false, OriginBridge, nil, nil)
}

// SayBridgedQuoted is SayBridged with a structured reply-quote — the
// dispatcher's stale-reply auto-quote (迟到回答点名它回答的那条线) rides
// as fields, so the mirrored text stays clean (复制/检索不再带引用头).
// 引用即点名 is quote.At's business: the hub resolves the wake by field
// (sayLike), the body carries no @.
func (h *Hub) SayBridgedQuoted(c *Client, text string, q *Quote) {
	h.sayLike(c, text, MsgSay, false, OriginBridge, nil, q)
}

// SayMirrorAsOwner speaks a mirror line through the owner's registered
// seat — the path for the seatless mirror connection (the hook CLI
// dials under the owner's name while the GUI player already holds that
// seat, so no new seat may be taken). It returns the delivered message
// for echoing back to the relay connection; ok is false when no live
// owner seat is registered.
func (h *Hub) SayMirrorAsOwner(text string) (msg Message, ok bool) {
	h.mu.Lock()
	c := h.ownerClient
	_, live := h.clients[c]
	h.mu.Unlock()
	if c == nil || !live {
		return Message{}, false
	}
	delivered := h.sayLike(c, text, MsgSay, false, OriginMirror, nil, nil)
	return delivered, delivered.Type != ""
}

// SayAsOwner speaks a plain say from the owner's real seat — history
// record, @-resolution, no origin marker. It backs the seatless
// management channel's non-mirror frames (offboard's departure notice,
// t_64): same shape as SayMirrorAsOwner minus the mirror flag.
