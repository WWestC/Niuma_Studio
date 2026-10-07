package chat

import (
	"github.com/WWestC/Niuma_Studio/util"

	"sort"
	"time"
)

// Presence: who is in the room right now, who is in the 10-minute
// grace window (ghosts), and the departed roster — plus the sweeper
// that walks ghosts to departed.

// ghostEntry is a seat held for a disconnected member during the
// presence grace window.
type ghostEntry struct {
	member   Member
	deadline time.Time
	token    string // the seat credential, so the member can prove the ghost is theirs
}

// Hub owns the room state. All methods are safe for concurrent use.

// HasLiveMember reports whether name currently holds a live seat
// (grace-period ghosts don't count). The server's hello handler uses it
// to route an owner-named join: seat free → a normal join lands on the
// owner's name; seat held → the seatless mirror connection instead.
func (h *Hub) HasLiveMember(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.member.Name == name {
			return true
		}
	}
	return false
}

// SetPresenceGrace adjusts how long disconnected members keep their
// seat (0 = leave takes effect immediately). Call it before members
// join; it exists for tests and deliberate strictness.
func (h *Hub) SetPresenceGrace(d time.Duration) {
	h.mu.Lock()
	h.grace = d
	h.mu.Unlock()
}

// GraceNames returns the names currently inside the presence grace
// window — seats held for disconnected members (v0.9 ME1): no live
// connection behind them, though the seat and any doing work persist.
// The world marks those nameplates with a trailing … ; the blackboard
// people page and CLI say 在线(宽限). Sorted for stable consumption.
func (h *Hub) GraceNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.ghosts))
	for n := range h.ghosts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Departed returns bounded snapshots of members who truly left the
// room — leave after the presence grace, or a kick — and have not come
// back. The blackboard merges this with the roster so a member without
// a saved AI config still shows as offline instead of vanishing.
func (h *Hub) Departed() []Member {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Member, 0, len(h.departed))
	for _, name := range h.departedOrder {
		out = append(out, h.departed[name])
	}
	return out
}

// departLocked records a leaving member's snapshot (newest wins),
// evicting the oldest name past departedCap. Callers must hold h.mu.
func (h *Hub) departLocked(m Member) {
	if _, ok := h.departed[m.Name]; !ok {
		h.departedOrder = append(h.departedOrder, m.Name)
		if over := len(h.departedOrder) - departedCap; over > 0 {
			for _, name := range h.departedOrder[:over] {
				delete(h.departed, name)
			}
			h.departedOrder = h.departedOrder[over:]
		}
	}
	h.departed[m.Name] = m
}

// forgetDepartedLocked drops a name from the departed list once the
// member is back. Callers must hold h.mu.
func (h *Hub) forgetDepartedLocked(name string) {
	if _, ok := h.departed[name]; !ok {
		return
	}
	delete(h.departed, name)
	for i, n := range h.departedOrder {
		if n == name {
			h.departedOrder = append(h.departedOrder[:i], h.departedOrder[i+1:]...)
			break
		}
	}
}

// History returns a copy of the retained chat history.

func (h *Hub) membersLocked() []Member {
	out := make([]Member, 0, len(h.clients)+len(h.ghosts))
	for c := range h.clients {
		out = append(out, c.member)
	}
	for _, g := range h.ghosts {
		out = append(out, g.member)
	}
	return out
}

// startSweeperLocked launches (once) the goroutine that expires grace
// ghosts: when a seat is not reclaimed before its deadline, the leave
// finally broadcasts. Callers must hold h.mu.
//
// 房间暂停（房间暂停开关）冻结宽限钟：暂停的语义是「恢复前一切
// 保持现状」——小人定格，座位也定格。一个重启后 attach 暂时失败的
// 成员靠快照 ghost 撑着座位；若暂停期宽限钟照走，ghost 到期被清、
// leave 广播，办公室里的小人就凭空消失——而暂停房连看护的自愈
// （缺岗自动补员召回）都被整房跳过，没有人能把座位找回来（v2.13
// 实录：暂停→重启→小人不见了）。暂停期每一拍把 deadline 顺延回
// 整个宽限窗，恢复后照常倒计时。
func (h *Hub) startSweeperLocked() {
	if h.sweeping {
		return
	}
	h.sweeping = true
	tick := h.grace / 8
	if tick < 50*time.Millisecond {
		tick = 50 * time.Millisecond
	}
	if tick > time.Second {
		tick = time.Second
	}
	go func() {
		for {
			time.Sleep(tick)
			if !h.sweepGhostsOnce() {
				return
			}
		}
	}()
}

// sweepGhostsOnce is the sweeper's one beat, extracted for two fences:
// the panic fence (a fenced beat retries the next tick — a dead
// sweeper is ghosts that never expire, silently) and the lock fence
// (defer-unlock: a panic mid-sweep releases the room instead of
// wedging every later h.mu taker). keep=false 收摊：没有幽灵要看了
// （含暂停且无幽灵的定格拍）。Callers: the loop above only.
func (h *Hub) sweepGhostsOnce() (keep bool) {
	keep = true // a fenced (panicked) beat keeps the watcher alive
	util.Guard("chat: presence sweeper", func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.pausedNow() {
			// 冻结：每拍顺延，不清座、不广播。没有 ghost 时照常收摊。
			if len(h.ghosts) == 0 {
				h.sweeping = false
				keep = false
				return
			}
			for _, g := range h.ghosts {
				g.deadline = time.Now().Add(h.grace)
			}
			return
		}
		var expired []string
		for name, g := range h.ghosts {
			if time.Now().After(g.deadline) {
				expired = append(expired, name)
				delete(h.ghosts, name)
				h.departLocked(g.member)
			}
		}
		for _, name := range expired {
			h.broadcastLocked(Message{Type: MsgLeave, From: name, TS: util.Now()})
		}
		if len(h.ghosts) == 0 {
			h.sweeping = false
			keep = false
			return
		}
	})
	return keep
}
