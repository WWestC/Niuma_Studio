package chat

import (
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"

	"sort"
	"strings"
	"time"
	"unicode"
)

// The roll-call engine: @所有人 wakes with an ack window (the
// roll-call list, expiry and the acks a leaving member owes), and the
// role-group wake resolves @<岗位>组 against the live roster.

// allReq is one pending @所有人 roll-call.
type allReq struct {
	by       string
	ts       int64
	deadline time.Time
	need     map[string]bool
	got      map[string]bool
	done     bool
}

// ackRollcallsLocked credits a say from name against every pending
// @所有人 roll-call waiting on it, broadcasting a completion notice
// when the last holdout replies. Callers must hold h.mu.
func (h *Hub) ackRollcallsLocked(name string) {
	for _, req := range h.allReqs {
		if req.done || !req.need[name] || req.got[name] {
			continue
		}
		req.got[name] = true
		if len(req.got) == len(req.need) {
			req.done = true
			h.broadcastLocked(Message{
				Type: MsgSystem,
				Text: i18n.Sf("@所有人点名已全员回应（%s 发起于 %s）", req.by, time.Unix(req.ts, 0).Format("15:04")),
				TS:   util.Now(),
			})
		}
	}
}

// expireAllReq fires when a roll-call's window lapses: the room gets
// one system notice naming whoever never replied. Fully-acked or
// already-noticed roll-calls stay silent.
func (h *Hub) expireAllReq(req *allReq) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.done {
		return
	}
	req.done = true
	holdouts := make([]string, 0, len(req.need))
	for n := range req.need {
		if !req.got[n] {
			holdouts = append(holdouts, n)
		}
	}
	if len(holdouts) == 0 {
		return
	}
	sort.Strings(holdouts)
	h.broadcastLocked(Message{
		Type: MsgSystem,
		Text: i18n.Sf("@所有人点名（%s 发起于 %s）超时，未回应：%s", req.by, time.Unix(req.ts, 0).Format("15:04"), strings.Join(holdouts, "、")),
		TS:   util.Now(),
	})
}

// dropFromRollcallsLocked removes a departing member from every
// pending @所有人 roll-call: the gone cannot be expected to reply.
// Callers must hold h.mu.
func (h *Hub) dropFromRollcallsLocked(name string) {
	for _, req := range h.allReqs {
		if req.done {
			continue
		}
		delete(req.need, name)
		delete(req.got, name)
		if len(req.need) == 0 {
			req.done = true
		}
	}
}

// groupWakeLocked scans text for "@<岗位>组" wake tokens (t_81, the
// O-5 riders: taxonomy-prefix match, never substring; broadcast-only
// semantics) and returns the matched live members. A group word that
// matches nobody broadcasts a hint listing the live stems, so a
// mis-typed group is never a silent no-op. Callers hold h.mu.
func (h *Hub) groupWakeLocked(text, sender string) []string {
	r := []rune(text)
	stems := map[string][]string{} // taxonomy stem → member names
	var order []string
	for m := range h.clients {
		n := m.member.Name
		if n == sender || n == h.owner {
			continue
		}
		s := RoleStem(m.member.Role)
		if s == "" {
			continue
		}
		if _, ok := stems[s]; !ok {
			order = append(order, s)
		}
		stems[s] = append(stems[s], n)
	}
	sort.Strings(order)
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(r); i++ {
		if r[i] != '@' || !atMentionStart(r, i) {
			continue
		}
		if i > 0 && (listPunctBefore(r[i-1]) || quoteRune(r[i-1])) {
			continue
		}
		j := i + 1
		for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j])) {
			j++
		}
		run := r[i+1 : j]
		if len(run) < 2 || run[len(run)-1] != '组' {
			continue
		}
		if j < len(r) && quoteRune(r[j]) {
			continue
		}
		stem := string(run[:len(run)-1])
		matched := false
		for _, s := range order {
			if strings.HasPrefix(s, stem) {
				matched = true
				for _, n := range stems[s] {
					if !seen[n] {
						seen[n] = true
						out = append(out, n)
					}
				}
			}
		}
		if !matched {
			h.broadcastLocked(Message{
				Type: MsgSystem,
				Text: i18n.Sf("岗位群呼 @%s组 未命中成员（当前岗位：%s）", stem, strings.Join(order, "、")),
				TS:   util.Now(),
			})
		}
	}
	return out
}

// mentionsLocked resolves the @-mentions in text against the current
// roster — live members and grace-period ghosts alike, so an @ aimed at
// a momentarily-disconnected agent still resolves: each "@" at the
// start of a word is matched to the longest name (case-insensitive)
// that prefixes what follows, so "@bot hi" and "@bot你好" both hit.
// Callers must hold h.mu.
func (h *Hub) mentionsLocked(text string) []string {
	names := make([][]rune, 0, len(h.clients)+len(h.ghosts))
	for c := range h.clients {
		names = append(names, []rune(c.member.Name))
	}
	for _, g := range h.ghosts {
		names = append(names, []rune(g.member.Name))
	}
	return ExtractMentions(text, names)
}
