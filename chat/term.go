package chat

// term.go — 终端转录（r_17）：每名成员一册 append-only 的完整进出
// （~/.niuma/terminal/<房间key>/<名>.jsonl，0600，一行一条 TraceEntry
// 形状的 JSON）。工作过程（trace.go 的环形缓冲）是展示流——折叠、戴
// 帽、只在内存；终端是记录流——输入与输出同一条时间线、逐条落盘、
// 跨重启。两个面同源（都吃调度器的 flush 批）不同闸，trace 的词汇、
// 帽与帧纪律原样不动。
//
// 折叠语义与 hub 环完全同构（客户端同一套 upsert 重放）：
//   - 工具卡按 Call 原位更新——同一 call 的后续状态行复用首建 seq，
//     行内容是合并后的全量卡（append-only 日志里同 seq 的后行在重放
//     时顶掉前行，终态即所见）；
//   - think/draft 同流尾并——同一流的后续增量并进末条（复用 seq、带
//     合并后全文），流断了（回合边界/其他条目插入）才开新 seq；
//   - 其余（input/ask/answer/sys/model/turn/error/工具首建）逐条新
//     seq。seq 是房间级单调发号，boot 时从各成员文件的尾段地板起来，
//     永不回卷。
//
// 内存里保一套有界镜像（每成员 2000 条，供无 root 的内嵌与测试直读
// 与合并回填基准）；TermSnapshot 把「文件尾行」按 seq upsert 进「内存
// 行」之上——重启前的旧行与重启后的新行拼成一条连续时间线。保留期
// 与聊天记录同窗：PruneTerm 由 server/retention.go 的清扫器统一调度。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/util"
)

// 终端转录的闸与帽：输出/入参帽是防呆闸（不是展示闸——那顶帽子归
// trace 的 displayGrade 管），文本流只设一个 8MB 的病态护栏。
const (
	TermOutMax    = 256 << 10 // 单条工具产物/错误文本的帽（超帽保头尾）
	TermInMax     = 64 << 10  // 单条工具入参 JSON 的帽（超帽换尺寸注记）
	TermTextCap   = 8 << 20   // think/draft 同流合并的病态护栏
	TermCapPerMem = 2000      // 内存镜像每成员条数帽（文件不限，保留期管）
	termScanLines = 4000      // boot 扫尾行数（seq 地板与折叠基准的回看窗）
	termTailBytes = 1 << 20   // boot 扫尾字节窗（1MB，行数帽之上的硬界）
)

// TermClipOut 保头尾地裁一条产物文本（转录侧的大帽版）。
func TermClipOut(s string) string {
	if len(s) <= TermOutMax {
		return s
	}
	half := TermOutMax / 2
	return s[:half] + i18n.Sf("\n…（产物超长，保头尾；全文 %s 字）…\n", itoa(len(s))) + s[len(s)-half:]
}

// TermTrimInput keeps the tool input as raw JSON until it would balloon
// the journal; a monster becomes a size note (trace 的 trimInput 大帽版).
func TermTrimInput(v any) any {
	b, err := json.Marshal(v)
	if err != nil || len(b) <= TermInMax {
		return v
	}
	return i18n.Sf("（输入 %s 字节，超出转录帽）", itoa(len(b)))
}

// termWrite is one queued transcript batch (or barrier): the member
// file lines AppendTerm marshaled under mu, plus the FIFO-tail barrier
// FlushTerm rides.
type termWrite struct {
	lines []termFileLine
	done  chan struct{} // non-nil = barrier: close once everything before it has landed
}

// termFileLine is one marshaled jsonl line for one member file.
type termFileLine struct {
	path string
	line []byte
}

// SetTermDir enables the terminal transcript on this hub (terminal/
// <key>/<名>.jsonl under the v2 root): the tail of every member file is
// scanned once to floor the seq counter and rebuild the fold basis
// (call→seq、同流末条), so a restarted instance continues the journal
// without seq collisions. "" (the default, in-memory tests) keeps
// AppendTerm working on the memory mirror only — no file, no boot scan.
// Call before the first AppendTerm (the registry wires it at hub
// construction, the same slot SetHistoryPath owns). Arming a directory
// also starts the term flusher (termSave): file appends run on their
// own goroutine from here on — the room's critical section marshals
// and enqueues only.
func (h *Hub) SetTermDir(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.term == nil {
		h.term = map[string][]TraceEntry{}
	}
	if h.termLast == nil {
		h.termLast = map[string]termTail{}
	}
	if h.termCalls == nil {
		h.termCalls = map[string]map[string]int64{}
	}
	h.termDir = dir
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("chat: term dir %s unusable (%v); transcript stays memory-only", dir, err)
		h.termDir = ""
		return
	}
	if h.termSave == nil {
		h.termSave = make(chan termWrite, 128)
		go h.flushTermLoop()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // unreadable: start from zero, the journal continues on a fresh foot
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		member := strings.TrimSuffix(name, ".jsonl")
		rows := termScanTail(filepath.Join(dir, name))
		if len(rows) == 0 {
			continue
		}
		ring := h.term[member]
		if ring == nil {
			ring = []TraceEntry{}
		}
		ring = append(ring, rows...)
		h.term[member] = ring
		for _, r := range rows {
			if r.Seq > h.termSeq {
				h.termSeq = r.Seq
			}
			if r.Kind == TraceTool && r.Call != "" {
				calls := h.termCalls[member]
				if calls == nil {
					calls = map[string]int64{}
					h.termCalls[member] = calls
				}
				calls[r.Call] = r.Seq
			}
		}
		h.termFloorLocked(member)
	}
}

// termFloorLocked trims a member's mirrored ring to the memory cap and
// records the tail's stream shape (the think/draft merge basis).
// Callers hold h.mu.
func (h *Hub) termFloorLocked(member string) {
	ring := h.term[member]
	if len(ring) > TermCapPerMem {
		ring = ring[len(ring)-TermCapPerMem:]
		h.term[member] = ring
	}
	t := termTail{}
	if n := len(ring); n > 0 {
		last := ring[n-1]
		if last.Kind == TraceThink || last.Kind == TraceDraft {
			t = termTail{seq: last.Seq, kind: last.Kind, agent: last.Agent, agentID: last.AgentID}
		}
	}
	h.termLast[member] = t
}

// termTail is the member's current text-stream merge basis: the last
// ring entry is a think/draft block that a same-stream delta may fold
// into (seq 0 = no open stream).
type termTail struct {
	seq     int64
	kind    string
	agent   bool
	agentID string
}

// AppendTerm lands one flush-batch of transcript entries: seq/TS are
// hub-stamped (the caller never invents them), fold rules mirror
// AppendTrace (tool in-place by Call, text tail-merge by stream), every
// step hands one marshaled jsonl line to the term flusher (the file
// appends left the critical section — the history pend's discipline),
// and the resulting rows ride a reminder-only "term" frame — the
// terminal board's live feed. Input rows (kind input/ask/answer/sys)
// never fold; each is its own line. Safe from any goroutine; must not
// be called with h.mu held.
func (h *Hub) AppendTerm(entries []TraceEntry) {
	if len(entries) == 0 {
		return
	}
	h.mu.Lock()
	if h.term == nil {
		h.term = map[string][]TraceEntry{}
		h.termLast = map[string]termTail{}
		h.termCalls = map[string]map[string]int64{}
	}
	var batch termWrite
	out := make([]TraceEntry, 0, len(entries))
	for _, e := range entries {
		if e.From == "" {
			continue
		}
		e.TS = util.Now()
		ring := h.term[e.From]
		if e.Kind == TraceTool && e.Call != "" {
			if seq, ok := h.termCalls[e.From][e.Call]; ok {
				e.Seq = seq
				if i := termFindSeq(ring, seq); i >= 0 {
					ring[i] = MergeTraceEntry(ring[i], e)
					e = ring[i]
				}
				h.term[e.From] = ring
				h.collectTermLineLocked(&batch, e.From, e)
				out = append(out, e)
				continue
			}
			h.termSeq++
			e.Seq = h.termSeq
			calls := h.termCalls[e.From]
			if calls == nil {
				calls = map[string]int64{}
				h.termCalls[e.From] = calls
			}
			calls[e.Call] = e.Seq
			if len(calls) > 4096 { // 病态护栏：日积月累的旧 call 键清台
				h.termCalls[e.From] = map[string]int64{e.Call: e.Seq}
			}
			ring = append(ring, e)
			h.term[e.From] = ring
			h.termFloorLocked(e.From)
			h.collectTermLineLocked(&batch, e.From, e)
			out = append(out, e)
			continue
		}
		if e.Kind == TraceThink || e.Kind == TraceDraft {
			if t := h.termLast[e.From]; t.seq != 0 && t.kind == e.Kind &&
				t.agent == e.Agent && t.agentID == e.AgentID && len(ring) > 0 {
				tail := &ring[len(ring)-1]
				if tail.Seq == t.seq {
					tail.Text = traceTextMergeCap(tail.Text, e.Text, TermTextCap)
					h.term[e.From] = ring
					h.collectTermLineLocked(&batch, e.From, *tail)
					out = append(out, *tail)
					continue
				}
			}
		}
		h.termSeq++
		e.Seq = h.termSeq
		ring = append(ring, e)
		h.term[e.From] = ring
		h.termFloorLocked(e.From)
		h.collectTermLineLocked(&batch, e.From, e)
		out = append(out, e)
	}
	if len(out) > 0 {
		h.broadcastLocked(Message{Type: MsgTerm, From: out[0].From, Trace: out})
	}
	h.mu.Unlock()
	if len(batch.lines) > 0 {
		h.enqueueTermWrite(batch)
	}
}

// collectTermLineLocked marshals one journal line under the lock
// (cheap, CPU-bound — appendHistoryLocked's discipline). A full flush
// queue drops the batch's lines with the history discipline: a lost
// persisted line must never break the live room (the memory mirror
// still serves the live view; the loss surfaces only on restart).
// Callers hold h.mu.
func (h *Hub) collectTermLineLocked(batch *termWrite, member string, e TraceEntry) {
	if h.termDir == "" || h.termSave == nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	batch.lines = append(batch.lines, termFileLine{
		path: filepath.Join(h.termDir, termFileLeaf(member)),
		line: append(line, '\n'),
	})
}

// enqueueTermWrite hands one marshaled batch to the flusher — the
// room's lock is already released here. Overflow drops the batch
// wholesale (FIFO head must stay intact; a partial write would tear
// the journal's fold basis at boot).
func (h *Hub) enqueueTermWrite(batch termWrite) {
	select {
	case h.termSave <- batch:
	default:
		log.Printf("chat: term flush queue full — 一批转录行未落盘（磁盘是瓶颈；房间照常）")
	}
}

// flushTermLoop is the transcript flusher goroutine: batches drain in
// FIFO order, each file opened once per batch. Never takes h.mu (the
// house flusher rule — callers may be enqueueing under it).
func (h *Hub) flushTermLoop() {
	for w := range h.termSave {
		h.flushTermBatch(w)
	}
}

// flushTermBatch is one queued batch (or barrier), extracted for its
// panic fence: the fence carries a debt — the batch's barrier must
// still close when the write panics (a hung FlushTerm waiter wedges
// the exit path), so close(w.done) rides the defer.
func (h *Hub) flushTermBatch(w termWrite) {
	defer func() {
		if r := recover(); r != nil {
			util.LogPanic("chat: term flusher", r)
		}
		if w.done != nil {
			close(w.done)
		}
	}()
	files := map[string]*os.File{}
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	h.termWriteMu.Lock()
	defer h.termWriteMu.Unlock()
	for _, l := range w.lines {
		f := files[l.path]
		if f == nil {
			nf, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				log.Printf("chat: term file %s unusable (%v); line dropped", l.path, err)
				continue
			}
			f = nf
			files[l.path] = f
		}
		if _, err := f.Write(l.line); err != nil {
			log.Printf("chat: term append %s: %v", l.path, err)
		}
	}
}

// FlushTerm drains the room's pending transcript writes: the barrier
// rides the queue's FIFO tail, so every batch enqueued before this
// call has landed when it returns. The prune rewrite, the file
// download face and every exit path drain first — the memory mirror is
// the live truth, the file is the read model. Never takes h.mu
// (callers may hold it).
func (h *Hub) FlushTerm() {
	if h.termSave == nil {
		return
	}
	bar := make(chan struct{})
	h.termSave <- termWrite{done: bar}
	<-bar
}

// TermFileLeaf is the exported fold for the read face (the /term/file
// hander must build the exact leaf the writer used — a local Base()
// guess would miss names carrying slashes).
func TermFileLeaf(member string) string { return termFileLeaf(member) }

// termFileLeaf folds a member name into a safe file leaf (slashes and
// friends become '_'; a name that folds empty falls back to '_' — the
// journal prefers a shared file over a lost one).
func termFileLeaf(member string) string {
	if member == "" {
		return "_.jsonl"
	}
	var b strings.Builder
	for _, r := range member {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '\x00' || r == '\n' || r == '\r':
			b.WriteByte('_')
		case r == '.' && (b.Len() == 0 || strings.HasSuffix(b.String(), ".")):
			b.WriteByte('_') // 无点开头、无点结尾：历史/.history 的同款顾虑
		default:
			b.WriteRune(r)
		}
	}
	leaf := b.String()
	if leaf == "" || utf8.RuneCountInString(leaf) > 96 {
		leaf = "_"
	}
	return leaf + ".jsonl"
}

// termFindSeq finds a row's index by seq (tail-first; -1 = miss).
func termFindSeq(ring []TraceEntry, seq int64) int {
	for i := len(ring) - 1; i >= 0; i-- {
		if ring[i].Seq == seq {
			return i
		}
	}
	return -1
}

// traceTextMergeCap is traceTextMerge with a parameterized cap (the
// term mirror folds under its own guard, not the display cap).
func traceTextMergeCap(prev, delta string, cap int) string {
	if prev == "" {
		if len(delta) > cap {
			return traceTextTrimTo(delta, cap)
		}
		return delta
	}
	out := prev + delta
	if len(out) > cap {
		return traceTextTrimTo(out, cap)
	}
	return out
}

// traceTextTrimTo is traceTextTrim with a parameterized cap.
func traceTextTrimTo(s string, cap int) string {
	half := cap / 2
	return s[:half] + i18n.Sf("\n…（中段省略，共 %s 字）…\n", itoa(len(s))) + s[len(s)-half:]
}

// termScanTail reads a transcript file's tail (the smaller of the last
// termTailBytes window and everything) and returns its parsed rows in
// file order — the boot basis for seq flooring and fold state.
func termScanTail(path string) []TraceEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var offset int64
	if st, err := f.Stat(); err == nil && st.Size() > termTailBytes {
		offset = st.Size() - termTailBytes
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil
	}
	br := bufio.NewReader(f)
	if offset > 0 {
		_, _ = br.ReadString('\n') // 窗口首行是半行的可能：丢弃
	}
	rows := make([]TraceEntry, 0, 256)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e TraceEntry
			if json.Unmarshal(line, &e) == nil && e.From != "" && e.Seq > 0 {
				rows = append(rows, e)
			}
		}
		if err != nil {
			break
		}
	}
	if len(rows) > termScanLines {
		rows = rows[len(rows)-termScanLines:]
	}
	return rows
}

// TermSnapshot serves the transcript's current view: for every member
// the file tail's rows upserted over by the memory mirror (restarted
// rows and live rows in one continuous timeline), each member's ring
// folded to its final display state (same upsert semantics the live
// frames carry — tool cards resolved, text streams whole) and cut to
// the last `tail` rows. name != "" narrows to one member ("*" shape of
// the trace face). Members appear roster-order-agnostic: whoever has a
// file or a mirror row shows up (departed members keep their trail).
// The rings are value-copied under mu and the FILE reads run outside
// it — a transcript scan must never hold the room hostage to disk.
func (h *Hub) TermSnapshot(name string, tail int) map[string][]TraceEntry {
	if tail <= 0 {
		tail = 500
	}
	h.mu.Lock()
	mirror := make(map[string][]TraceEntry, len(h.term))
	for member, ring := range h.term {
		if name != "" && member != name {
			continue
		}
		cp := make([]TraceEntry, len(ring))
		for i := range ring {
			cp[i] = ring[i] // value copy: later in-place folds must not race this read
		}
		mirror[member] = cp
	}
	termDir := h.termDir
	h.mu.Unlock()

	bySeq := map[string]map[int64]TraceEntry{}
	for member := range mirror {
		bySeq[member] = map[int64]TraceEntry{}
	}
	if termDir != "" {
		if entries, err := os.ReadDir(termDir); err == nil {
			for _, ent := range entries {
				leaf := ent.Name()
				if ent.IsDir() || !strings.HasSuffix(leaf, ".jsonl") {
					continue
				}
				member := strings.TrimSuffix(leaf, ".jsonl")
				if name != "" && member != name {
					continue
				}
				if _, ok := bySeq[member]; !ok {
					bySeq[member] = map[int64]TraceEntry{}
				}
				for _, r := range termScanTail(filepath.Join(termDir, leaf)) {
					bySeq[member][r.Seq] = r
				}
			}
		}
	}
	for member, ring := range mirror {
		if bySeq[member] == nil {
			bySeq[member] = map[int64]TraceEntry{}
		}
		for _, r := range ring {
			bySeq[member][r.Seq] = r // 内存行更新鲜：同 seq 全量覆盖文件行
		}
	}
	out := map[string][]TraceEntry{}
	for member, m := range bySeq {
		if len(m) == 0 && name == "" {
			continue // 空成员不占行（聚焦查询保留空行给 404 语义判断）
		}
		rows := make([]TraceEntry, 0, len(m))
		for _, r := range m {
			rows = append(rows, r)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
		if len(rows) > tail {
			rows = rows[len(rows)-tail:]
		}
		out[member] = rows
	}
	return out
}

// PruneTerm drops transcript rows older than keepSince from every
// member's file and the memory mirror (the retention sweep's terminal
// leg — same window the chat history rides). The memory leg runs
// under mu; the file rewrites run outside it, serialized against the
// flusher's appends by termWriteMu and preceded by a FlushTerm
// barrier so no in-flight batch re-adds what the rewrite just dropped.
// Returns dropped rows.
func (h *Hub) PruneTerm(keepSince int64) int {
	h.mu.Lock()
	dropped := 0
	for member, ring := range h.term {
		kept := ring[:0]
		for _, e := range ring {
			if e.TS > 0 && e.TS < keepSince {
				dropped++
				continue
			}
			kept = append(kept, e)
		}
		if len(kept) == 0 {
			delete(h.term, member)
			delete(h.termLast, member)
			delete(h.termCalls, member)
		} else {
			h.term[member] = kept
			h.termFloorLocked(member)
		}
	}
	termDir := h.termDir
	h.mu.Unlock()
	if termDir == "" {
		return dropped
	}
	h.FlushTerm() // 先排干在途批：重写后的文件不再吃进待写旧行
	h.termWriteMu.Lock()
	defer h.termWriteMu.Unlock()
	if entries, err := os.ReadDir(termDir); err == nil {
		for _, ent := range entries {
			leaf := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(leaf, ".jsonl") {
				continue
			}
			dropped += PruneTermFile(filepath.Join(termDir, leaf), keepSince)
		}
	}
	return dropped
}

// PruneTermFile rewrites one transcript file keeping rows at or past
// keepSince (tmp + rename, writeHistoryFile's discipline). Hub-less
// rooms' files go through here directly from the retention sweep.
func PruneTermFile(path string, keepSince int64) int {
	if path == "" {
		return 0
	}
	rows := termScanTailAll(path)
	kept := make([]TraceEntry, 0, len(rows))
	dropped := 0
	for _, e := range rows {
		if e.TS > 0 && e.TS < keepSince {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	if dropped == 0 {
		return 0
	}
	var b bytes.Buffer
	for _, e := range kept {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := persist.Save(path, b.Bytes(), 0o600); err != nil {
		log.Printf("chat: term prune rewrite %s failed (%v); old file stands", path, err)
		return 0
	}
	return dropped
}

// termScanTailAll streams the whole file (prune must see every row,
// not the tail window).
func termScanTailAll(path string) []TraceEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	rows := make([]TraceEntry, 0, 256)
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e TraceEntry
			if json.Unmarshal(line, &e) == nil && e.From != "" && e.Seq > 0 {
				rows = append(rows, e)
			}
		}
		if err != nil {
			break
		}
	}
	return rows
}
