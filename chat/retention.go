package chat

// retention.go — 聊天记录保留期（v2.8.2）: the studio-wide "how long
// do chat logs live" policy. retention.json under the v2 root holds it
// ({"days": N}, N=0 永久); the default is 7 — a studio that never
// touched the setting still keeps its disk honest (server/retention.go
// sweeps on that policy; this file owns the policy's storage and its
// teeth).
//
// The teeth are the prune: history frames whose ts predates the cutoff
// leave BOTH the jsonl (rewritten atomically from the FILE's kept
// lines — the in-memory ring holds only the newest historyCap frames,
// rewriting from the ring would amputate the log) and the ring. seq
// never rewinds: the newest frames always survive, lastSeq stands, and
// the monotonic counter keeps counting past the cut.

import (
	"encoding/json"
	"log"
	"os"
	"regexp"

	"github.com/WWestC/Niuma_Studio/persist"
)

// DefaultRetentionDays is the policy a fresh (or corrupt-config)
// studio runs under — 7 days, the requested default.
const DefaultRetentionDays = 7

// RetentionConfig is the studio-wide chat-log retention policy.
type RetentionConfig struct {
	Days int `json:"days"` // 0 = 永久
}

// LoadRetention reads the policy from path. A missing or unreadable
// file is the DEFAULT, never a boot error — persistence must never
// refuse the office; a negative days is corrupt, same answer.
func LoadRetention(path string) RetentionConfig {
	b, err := os.ReadFile(path)
	if err != nil {
		return RetentionConfig{Days: DefaultRetentionDays}
	}
	var cfg RetentionConfig
	if err := json.Unmarshal(b, &cfg); err != nil || cfg.Days < 0 {
		return RetentionConfig{Days: DefaultRetentionDays}
	}
	return cfg
}

// SaveRetention persists the policy atomically (persist.Save — the
// same discipline every ledger write uses). The caller validates days.
func SaveRetention(path string, days int) error {
	b, err := json.Marshal(RetentionConfig{Days: days})
	if err != nil {
		return err
	}
	return persist.Save(path, b, 0o600)
}

// PruneHistory drops every persisted frame older than keepSince
// (unix seconds) from the jsonl AND the in-memory ring, returning the
// dropped count. A frame with no ts (0) has no age to judge — it
// stays. The rewrite reads the FILE's kept lines under h.mu (appends
// take the same lock, so nothing lands mid-rewrite); lastSeq is never
// touched — the newest frame survives every sane cutoff, and an
// everything-goes prune still leaves the counter monotonic for the
// appends that follow. In-memory rooms (no history path) prune
// nothing: there is nothing persisted to keep honest.
func (h *Hub) PruneHistory(keepSince int64) int {
	if h.historyPath == "" {
		return 0
	}
	// the rewrite must see every line already said: drain the flusher
	// first, or the rewrite would amputate the pending tail. The
	// barrier rides the RETIRED queue outside h.mu (swapPendLocked —
	// the flusher 家法); lines said in the swap window park losslessly
	// in pendHold and re-enqueue after the rewrite lands, so nothing
	// is amputated and nothing blocks the room on the drain.
	h.durableDrainMu.Lock()
	h.mu.Lock()
	old := h.swapPendLocked()
	h.holding = true
	h.mu.Unlock()
	retirePend(old)
	h.durableDrainMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	kept, dropped := scanKeep(h.historyPath, keepSince)
	if dropped > 0 {
		writeHistoryFile(h.historyPath, kept)
		ring := make([]Message, 0, len(h.history))
		for _, m := range h.history {
			if m.TS > 0 && m.TS < keepSince {
				continue
			}
			ring = append(ring, m)
		}
		h.history = ring
	}
	// 泊住的行在重写落地后才入队（先入队会被重写截肢）；无删减时
	// 照常放行——两种收尾都不丢窗口期的行。
	h.releaseHoldLocked(false)
	return dropped
}

// PruneHistoryFile is PruneHistory's hub-less variant: the room has no
// live Hub (archived, or never instantiated this boot) so only the
// file needs the cut — and only the file can race nothing, appends
// belong to a Hub.
func PruneHistoryFile(path string, keepSince int64) int {
	if path == "" {
		return 0
	}
	kept, dropped := scanKeep(path, keepSince)
	if dropped == 0 {
		return 0
	}
	writeHistoryFile(path, kept)
	return dropped
}

// scanKeep streams the jsonl keeping every frame at or past keepSince
// (ts-less frames stay — no age, no verdict).
func scanKeep(path string, keepSince int64) (kept []Message, dropped int) {
	kept = make([]Message, 0, 128)
	_ = ScanHistory(path, func(m Message) bool {
		if m.TS > 0 && m.TS < keepSince {
			dropped++
			return true
		}
		kept = append(kept, m)
		return true
	})
	return kept, dropped
}

// writeHistoryFile atomically rewrites path with frames, one jsonl
// line each (persist.Save). A failed rewrite logs and leaves the old
// file standing — the live room carries on (appendHistory's
// discipline).
func writeHistoryFile(path string, frames []Message) {
	var buf []byte
	for _, m := range frames {
		line, err := json.Marshal(m)
		if err != nil {
			continue
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	if err := persist.Save(path, buf, 0o600); err != nil {
		log.Printf("chat: history rewrite: %v", err)
	}
}

// idTokenRE matches a whole r_NN/t_NN/m_NN token in free text — the
// renumber migrations' single-pass swap. One pass, never chained
// ReplaceAll: a renumber map can hold a value that is also a key
// (t_02→t_01 with t_03→t_02), and chained whole-string replaces would
// re-replace a just-written token.
var idTokenRE = regexp.MustCompile(`[rtm]_\d{1,4}`)

// SwapIDTokens rewrites every r_NN/t_NN/m_NN token of str that rides
// in mapping, in one pass. Exported for the server-side renumber
// migration (kb bodies and prose walk the same discipline).
func SwapIDTokens(str string, mapping map[string]string) string {
	if str == "" || len(mapping) == 0 {
		return str
	}
	return idTokenRE.ReplaceAllStringFunc(str, func(tok string) string {
		if nn, ok := mapping[tok]; ok {
			return nn
		}
		return tok
	})
}

// RemapHistoryIDs is PruneHistoryFile's renumber cousin (v2.11 号段
// 完全重排): it walks the room's jsonl and swaps every ledger-id
// token — frame Text and quote snips — that rides in mapping. Run at
// boot BEFORE any hub owns the file (same hub-less discipline as
// PruneHistoryFile). A file where nothing hits is left untouched on
// disk; returns how many frames were rewritten.
func RemapHistoryIDs(path string, mapping map[string]string) int {
	if path == "" || len(mapping) == 0 {
		return 0
	}
	var kept []Message
	rewritten := 0
	_ = ScanHistory(path, func(m Message) bool {
		hit := false
		if nn := SwapIDTokens(m.Text, mapping); nn != m.Text {
			m.Text = nn
			hit = true
		}
		if m.Quote != nil && m.Quote.Snip != "" {
			if nn := SwapIDTokens(m.Quote.Snip, mapping); nn != m.Quote.Snip {
				m.Quote.Snip = nn
				hit = true
			}
		}
		if hit {
			rewritten++
		}
		kept = append(kept, m)
		return true
	})
	if rewritten == 0 {
		return 0 // no rewrite without a hit — sweeps must not churn disks
	}
	writeHistoryFile(path, kept)
	return rewritten
}
