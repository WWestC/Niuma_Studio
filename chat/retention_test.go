package chat

// retention.go's contract: the policy file (default 7, 0=永久, corrupt
// degrades to default) and the prune itself — old frames leave the
// jsonl AND the ring, newest frames and lastSeq survive, appends keep
// counting past the cut, and a no-op sweep never rewrites the disk.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestRetentionConfigLoadSave: round-trip, the 0=永久 write, and the
// missing/corrupt/negative file all answering the DEFAULT (7).
func TestRetentionConfigLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.json")

	if cfg := LoadRetention(path); cfg.Days != DefaultRetentionDays {
		t.Fatalf("missing file = %d days, want the default 7", cfg.Days)
	}
	if err := SaveRetention(path, 30); err != nil {
		t.Fatalf("save: %v", err)
	}
	if cfg := LoadRetention(path); cfg.Days != 30 {
		t.Fatalf("round-trip = %d days, want 30", cfg.Days)
	}
	if err := SaveRetention(path, 0); err != nil {
		t.Fatalf("save 永久: %v", err)
	}
	if cfg := LoadRetention(path); cfg.Days != 0 {
		t.Fatalf("永久 round-trip = %d days, want 0", cfg.Days)
	}

	writeLines(t, path, "{not json")
	if cfg := LoadRetention(path); cfg.Days != 7 {
		t.Fatalf("corrupt file = %d days, want the default 7", cfg.Days)
	}
	writeLines(t, path, `{"days":-3}`)
	if cfg := LoadRetention(path); cfg.Days != 7 {
		t.Fatalf("negative days = %d, want the default 7", cfg.Days)
	}
}

// TestHubPruneHistoryCutsBothCopies: frames older than the cutoff leave
// the jsonl and the in-memory ring; newer frames and the monotonic
// counter survive; the append after the prune lands above the counter.
func TestHubPruneHistoryCutsBothCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history", "r.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Now().Unix()
	old, mid, newF := now-30*86400, now-3*86400, now-60
	writeLines(t, path,
		`{"type":"say","from":"甲","text":"old","ts":`+itoa64(old)+`,"seq":1}`,
		`{"type":"say","from":"乙","text":"mid","ts":`+itoa64(mid)+`,"seq":2}`,
		`{"type":"say","from":"甲","text":"new","ts":`+itoa64(newF)+`,"seq":3}`,
	)

	h := NewHub()
	h.SetHistoryPath(path) // loads the 3 frames
	seat, _ := h.Join("房主", "boss", false)
	h.SetOwnerSeat(seat)
	seqFloor := h.LastSeq() // the ms-seeded counter (≥ every restored seq)

	dropped := h.PruneHistory(now - 7*86400) // 7-day cutoff: only "old" goes
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if lines := readLines(t, path); len(lines) != 2 || !strings.Contains(lines[0], `"seq":2`) {
		t.Fatalf("jsonl after prune = %v, want seq 2..3 only", lines)
	}
	for _, m := range h.History() {
		if m.Seq == 1 {
			t.Fatalf("ring still holds the pruned frame")
		}
	}
	if h.LastSeq() != seqFloor {
		t.Fatalf("lastSeq = %d, want %d (the counter never rewinds)", h.LastSeq(), seqFloor)
	}

	// the append after the prune continues past the cut. The flusher is
	// asynchronous now (the say path left the critical section): a FILE
	// reader drains first — FlushHistory is the read-your-write door.
	h.SayAsOwner("post-prune")
	h.FlushHistory()
	lines := readLines(t, path)
	if len(lines) != 3 || !strings.Contains(lines[2], "post-prune") {
		t.Fatalf("append after prune = %v", lines)
	}
	if h.LastSeq() <= seqFloor {
		t.Fatalf("lastSeq after append = %d, want above the floor %d", h.LastSeq(), seqFloor)
	}
}

// TestHubPruneHistoryNoCutNoChurn: a sweep with nothing to drop must
// not touch the file (mtime-level churn would be a rewrite).
func TestHubPruneHistoryNoCutNoChurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.json")
	writeLines(t, path,
		`{"type":"say","from":"甲","text":"fresh","ts":`+itoa64(time.Now().Unix())+`,"seq":1}`)

	h := NewHub()
	h.SetHistoryPath(path)
	if dropped := h.PruneHistory(time.Now().Unix() - 7*86400); dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(readLines(t, path)) != 1 {
		t.Fatalf("file rewritten on a no-op sweep")
	}
}

// TestPruneHistoryFileAllOld: the hub-less cut empties a fully stale
// file; ts-less frames (no age) always stay.
func TestPruneHistoryFileAllOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.json")
	old := time.Now().Unix() - 400*86400
	writeLines(t, path,
		`{"type":"say","from":"甲","text":"ancient","ts":`+itoa64(old)+`,"seq":1}`,
		`{"type":"say","from":"乙","text":"no-ts","seq":2}`) // TS 0: no verdict
	if dropped := PruneHistoryFile(path, time.Now().Unix()-7*86400); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	lines := readLines(t, path)
	if len(lines) != 1 || !strings.Contains(lines[0], "no-ts") {
		t.Fatalf("kept = %v, want the ts-less frame only", lines)
	}
}

// TestRemapHistoryIDs: the v2.11 renumber migration's chat face —
// Text and quote snips ride along in ONE pass (a map whose value is
// also a key must not chain), and a no-hit file is not rewritten.
// The mapping here is Plan-shaped (keys and values disjoint, like a
// real RenumberPlan) so the re-sweep converges to zero — the boot
// discipline re-derives the plan from compact shelves.
func TestRemapHistoryIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.json")
	writeLines(t, path,
		`{"type":"say","from":"甲","text":"t_99 干完了，等 r_33 评审","ts":100,"seq":1}`,
		`{"type":"say","from":"乙","text":"没号","ts":101,"seq":2}`,
		`{"type":"say","from":"丙","text":"引用","quote":{"from":"甲","snip":"t_99 干完了"},"ts":102,"seq":3}`)
	mapping := map[string]string{"t_99": "t_01", "r_33": "r_02"}

	if n := RemapHistoryIDs(path, mapping); n != 2 {
		t.Fatalf("rewritten = %d, want 2", n)
	}
	lines := readLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("frames lost: %v", lines)
	}
	if !strings.Contains(lines[0], "t_01 干完了，等 r_02 评审") {
		t.Fatalf("frame 1 未换到位: %s", lines[0])
	}
	if !strings.Contains(lines[2], `"snip":"t_01 干完了"`) {
		t.Fatalf("quote snip 未换: %s", lines[2])
	}

	// no-hit 文件不动盘（重扫零改写）
	if n := RemapHistoryIDs(path, mapping); n != 0 {
		t.Fatalf("re-sweep rewrote %d frames, want 0", n)
	}
}

// TestSwapIDTokensSinglePass: 交错的键值（值同时是键——t_03→t_02 与
// t_02→t_01 同表）必须单遍各归各位，不链式踩踏。
func TestSwapIDTokensSinglePass(t *testing.T) {
	got := SwapIDTokens("看 t_03 与 t_02",
		map[string]string{"t_03": "t_02", "t_02": "t_01"})
	if got != "看 t_02 与 t_01" {
		t.Fatalf("单遍整词换应各归各位：得 %q", got)
	}
	if SwapIDTokens("", map[string]string{"t_01": "t_02"}) != "" {
		t.Fatal("空串原样")
	}
	if SwapIDTokens("无号", nil) != "无号" {
		t.Fatal("空映射原样")
	}
}
