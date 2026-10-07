// db.go — the log-structured engine behind DocsStore: one
// write-ahead log (log.jsonl, one JSON event per line, O_APPEND)
// plus a full-state checkpoint (snapshot.json, temp+rename atomic).
// Every mutation is one event appended to the WAL FIRST and applied
// to the in-memory map second — a single mutex serializes writers,
// so replaying snapshot + log tail at boot reproduces the exact
// state a running room held. A torn last line (crash mid-append) is
// skipped, chat history's rule; a snapshot whose applied-seq already
// covers a log line makes that line a no-op, so the compact window
// (snapshot written, log not yet truncated) is crash-safe too.
//
// The v0.5 read/write contract is preserved verbatim — Get(rev)
// still reads "content at rev N = history entry N+1", DiffAround
// still returns the (replaced, current) pair under one lock,
// RevConflictError still guards optimistic writes. What the engine
// adds is lifecycle: archive/unarchive/delete events, ledger
// rollover volumes, and an explicit BodyLimitError where the file
// store used to clamp bodies silently.
package kb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

const (
	dbLogFile      = "log.jsonl"
	dbSnapshotFile = "snapshot.json"
	dbSnapshotVer  = 1
	// dbCompactEvery folds the log into the snapshot once this many
	// events stand in the file — checked at boot and after every
	// append; 2000 events ≈ a few hundred KB of log.
	dbCompactEvery = 2000
)

// event ops. Body ops (write/append/seed) share applyWrite; append
// carries the appended LINE in Body and replay re-joins it; roll
// carries the new line plus the volume doc it spills into.
const (
	evWrite     = "write"
	evAppend    = "append"
	evArchive   = "archive"
	evUnarchive = "unarchive"
	evDelete    = "delete"
	evRoll      = "roll"
	evSeed      = "seed"
)

// event is one WAL record. Rev is the revision the op produced
// (audit; the authoritative rev lives in state).
type event struct {
	Seq   int64  `json:"seq"`
	Op    string `json:"op"`
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	By    string `json:"by"`
	TS    int64  `json:"ts"`
	Rev   int    `json:"rev,omitempty"`
	ToKey string `json:"to,omitempty"` // roll: the volume doc the live body landed in
}

// docState is one doc's full state: metadata, current body and the
// history tail (oldest first, ≤ DocHistoryTail).
type docState struct {
	Meta    DocMeta        `json:"meta"`
	Body    string         `json:"body"`
	History []HistoryEntry `json:"history,omitempty"`
}

// snapshot is the compacted full state. Applied is the last event
// seq already folded in — replay applies log events with a greater
// seq only, which makes snapshot/log truncation ordering free.
type snapshot struct {
	Version int                  `json:"version"`
	Applied int64                `json:"applied"`
	Docs    map[string]*docState `json:"docs"`
}

// DocsStore is the room's manual store. All methods are safe for
// concurrent use.
type DocsStore struct {
	dir  string
	mu   sync.Mutex
	docs map[string]*docState
	seq  int64 // last applied event seq
	logN int   // events currently standing in the log file
}

// OpenDocs opens (or starts) the store under dir. A missing directory
// is created and seeded once with the v2.2 starter manuals; an
// existing one loads snapshot + log tail and is never reseeded — even
// if every seed was deleted. (The legacy ~/.niuma_kb markdown tree is
// NOT looked at here: main.go runs the one-shot import before the
// store opens.)
func OpenDocs(dir string) (*DocsStore, error) {
	s := &DocsStore{dir: dir, docs: map[string]*docState{}}
	_, statErr := os.Stat(dir)
	if errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		for _, seed := range seedDocs() {
			if err := s.walAppend(&event{Op: evSeed, Key: seed.key,
				Title: seed.title, Body: seed.body, By: "seed"}); err != nil {
				return nil, err
			}
		}
		// checkpoint immediately: a fresh room boots straight off the
		// snapshot and the log starts empty
		if err := s.compactLocked(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if statErr != nil {
		return nil, statErr
	}
	s.load()
	if s.logN >= dbCompactEvery {
		if err := s.compactLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// --- engine: load / replay / compact ---------------------------------------

// load restores state from snapshot.json then replays log.jsonl's
// tail. A torn last line (crash mid-append, no trailing newline) is
// truncated away BEFORE anything else appends — otherwise the next
// event would glue onto the fragment and die with it as one
// unparseable line. Already-folded lines (seq ≤ snapshot applied —
// the compact crash window) skip silently.
func (s *DocsStore) load() {
	if b, err := os.ReadFile(s.snapPath()); err == nil {
		var snap snapshot
		if json.Unmarshal(b, &snap) == nil && snap.Docs != nil {
			for k, st := range snap.Docs {
				// 加载侧重验证（安全核查修复）：snapshot 是本目录里最像
				// 状态真相的文件，但 key 若被手改/毒化成 '../x' 一类，导出
				// 面会把它写出目标目录——与全仓其他 store 的"外来文件跳过"
				// 纪律同款，非法行不进状态。
				if ValidateKey(k) != nil {
					continue
				}
				s.docs[k] = st
			}
			s.seq = snap.Applied
		}
	}
	if b, err := os.ReadFile(s.logPath()); err == nil {
		if n := bytes.LastIndexByte(b, '\n'); n >= 0 && n+1 < len(b) {
			// torn tail: repair the log at the last clean line break
			_ = os.Truncate(s.logPath(), int64(n+1))
			b = b[:n+1]
		} else if n < 0 && len(b) > 0 {
			// the whole file is one tear — empty it
			_ = os.Truncate(s.logPath(), 0)
			b = nil
		}
		bad := 0
		for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if line == "" {
				continue
			}
			var ev event
			if json.Unmarshal([]byte(line), &ev) != nil || ev.Seq <= s.seq {
				bad++
				continue
			}
			s.applyEvent(&ev)
			s.seq = ev.Seq
			s.logN++
		}
		if bad > 0 {
			log.Printf("kb: 日志 %s: 跳过 %d 行（已折叠或不可解析）", s.logPath(), bad)
		}
	}
}

// applyEvent is the single state transition — runtime ops and boot
// replay run the exact same code, so the log is the one truth of how
// state moves. Callers hold s.mu. The key gate is the load-side rail
// （安全核查修复）: 运行时入键在上游验过，回放这条腿以前没验——毒化
// 的 log 行进不了状态。
func (s *DocsStore) applyEvent(ev *event) {
	if ValidateKey(ev.Key) != nil || (ev.Op == evRoll && ValidateKey(ev.ToKey) != nil) {
		return
	}
	switch ev.Op {
	case evWrite, evAppend, evSeed:
		s.applyWrite(ev)
	case evArchive:
		if st := s.docs[ev.Key]; st != nil {
			st.Meta.Archived = true
		}
	case evUnarchive:
		if st := s.docs[ev.Key]; st != nil {
			st.Meta.Archived = false
		}
	case evDelete:
		delete(s.docs, ev.Key)
	case evRoll:
		s.applyRoll(ev)
	}
}

// applyWrite folds one body replacement: the replaced body archives
// into the history tail under the rev THIS write produced (the v0.5
// contract — content at rev N is entry N+1's body), then the new
// body lands. An empty replaced body archives nothing, exactly like
// the file store did. Append events carry the appended line and
// re-join it onto the current body. Callers hold s.mu.
func (s *DocsStore) applyWrite(ev *event) {
	st := s.docs[ev.Key]
	next := 1
	if st != nil {
		next = st.Meta.Rev + 1
		if st.Body != "" {
			st.History = append(st.History, HistoryEntry{
				Rev: next, By: ev.By, TS: ev.TS, Title: ev.Title, Body: st.Body,
			})
			if over := len(st.History) - DocHistoryTail; over > 0 {
				st.History = st.History[over:]
			}
		}
	} else {
		st = &docState{}
		s.docs[ev.Key] = st
		st.Meta.Key = ev.Key
		st.Meta.Owner = ev.By
	}
	body := ev.Body
	if ev.Op == evAppend {
		body = joinAppend(st.Body, ev.Body)
	}
	st.Meta.Title = ev.Title
	st.Meta.UpdatedBy = ev.By
	st.Meta.UpdatedTS = ev.TS
	st.Meta.Rev = next
	st.Meta.Size = len(body)
	st.Body = body
	ev.Rev = next
}

// applyRoll folds one ledger rollover: the live body lands whole in a
// fresh volume doc (born archived, rev 1), and the live doc restarts
// with the appended line. The live doc's rev keeps climbing —
// expect_rev anchors never rewind — but no history entry is cut: the
// replaced body lives on as the volume doc, not as a tail row.
// Callers hold s.mu.
func (s *DocsStore) applyRoll(ev *event) {
	st := s.docs[ev.Key]
	if st == nil || s.docs[ev.ToKey] != nil {
		return
	}
	vol := &docState{Body: st.Body}
	vol.Meta = DocMeta{
		Key: ev.ToKey, Title: ev.Title, Owner: ev.By,
		UpdatedBy: ev.By, UpdatedTS: ev.TS, Rev: 1,
		Size: len(st.Body), Archived: true,
	}
	s.docs[ev.ToKey] = vol
	st.Meta.Rev++
	st.Meta.UpdatedBy = ev.By
	st.Meta.UpdatedTS = ev.TS
	st.Body = joinAppend("", ev.Body)
	st.Meta.Size = len(st.Body)
	ev.Rev = st.Meta.Rev
}

// walAppend writes one event to the log FIRST (write-ahead), then
// applies it in memory. A failed log write refuses the op — state
// never moves ahead of the WAL. Compaction fires past the threshold;
// its failure is logged, never propagated: the log is still the
// truth. Callers hold s.mu.
func (s *DocsStore) walAppend(ev *event) error {
	ev.Seq = s.seq + 1
	if ev.By == "" {
		ev.By = "unknown"
	}
	if ev.TS == 0 {
		ev.TS = time.Now().Unix()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.seq = ev.Seq
	s.logN++
	s.applyEvent(ev)
	if s.logN >= dbCompactEvery {
		if err := s.compactLocked(); err != nil {
			log.Printf("kb: 压缩失败（日志仍在，不影响数据）: %v", err)
		}
	}
	return nil
}

// compactLocked folds the whole state into snapshot.json and restarts
// the log empty. Crash-safe in both orders: the snapshot's applied-seq
// makes pre-truncation log lines no-ops at replay. Callers hold s.mu.
func (s *DocsStore) compactLocked() error {
	snap := snapshot{Version: dbSnapshotVer, Applied: s.seq, Docs: s.docs}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(s.snapPath(), b); err != nil {
		return err
	}
	s.logN = 0
	return atomicWriteFile(s.logPath(), nil)
}

func (s *DocsStore) logPath() string  { return filepath.Join(s.dir, dbLogFile) }
func (s *DocsStore) snapPath() string { return filepath.Join(s.dir, dbSnapshotFile) }

// --- reads -----------------------------------------------------------------

// List returns the active-doc index, most recently updated first.
func (s *DocsStore) List() []DocMeta { return s.ListOpt(ListOptions{}) }

// ListOptions filters the docs index. Zero value = the default active
// face (List()). Archived flips to the archive shelf; Prefix filters
// by key prefix; Q is a case-insensitive substring over key + title;
// Project scopes the shelf to one office (公共＋p/<project>/…，the
// isolation read face — "" keeps the studio-wide index).
type ListOptions struct {
	Archived bool
	Prefix   string
	Q        string
	Project  string
}

// ListOpt is List with filters.
func (s *DocsStore) ListOpt(o ListOptions) []DocMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.ToLower(o.Q)
	out := make([]DocMeta, 0, len(s.docs))
	for _, st := range s.docs {
		m := st.Meta
		if m.Archived != o.Archived {
			continue
		}
		if o.Project != "" && !KeyInScope(m.Key, o.Project) {
			continue
		}
		if o.Prefix != "" && !strings.HasPrefix(m.Key, o.Prefix) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.Key+" "+m.Title), q) {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedTS != out[j].UpdatedTS {
			return out[i].UpdatedTS > out[j].UpdatedTS
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Count returns the number of active docs (archived ones are off the
// shelf and off the cap).
func (s *DocsStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeLocked()
}

func (s *DocsStore) activeLocked() int {
	n := 0
	for _, st := range s.docs {
		if !st.Meta.Archived {
			n++
		}
	}
	return n
}

// Get returns the doc at revision rev: 0 (or the current rev) reads
// the live state; older revs come from the history tail.
func (s *DocsStore) Get(key string, rev int) (Doc, error) {
	if err := ValidateKey(key); err != nil {
		return Doc{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.docs[key]
	if !ok {
		return Doc{}, os.ErrNotExist
	}
	m := st.Meta
	if rev <= 0 || rev == m.Rev {
		return Doc{DocMeta: m, Body: st.Body}, nil
	}
	for _, h := range st.History {
		if h.Rev == rev+1 {
			mm := m
			mm.Rev = rev
			mm.UpdatedBy = h.By
			mm.UpdatedTS = h.TS
			if h.Title != "" {
				mm.Title = h.Title
			}
			mm.Size = len(h.Body)
			return Doc{DocMeta: mm, Body: h.Body}, nil
		}
	}
	// a readable denial beats os.ErrNotExist's file-level wording: the
	// key exists, the revision doesn't (pruned beyond the tail, or
	// never archived) — surface which one is wrong (e2e P3)
	return Doc{}, errors.New(i18n.Sf("未知版本 rev=%d（当前 rev=%d，历史保留最近 %d 版）", rev, m.Rev, DocHistoryTail))
}

// History returns the doc's version chain, oldest first, tail ≤20.
func (s *DocsStore) History(key string) ([]HistoryEntry, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.docs[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]HistoryEntry(nil), st.History...), nil
}

// DiffAround returns the bodies surrounding one write: the body that
// write replaced and the doc's current body — the pair the kb
// broadcast turns into the chat card's red/green diff. The pre-state
// is the history entry at exactly that rev (the archive and the state
// swap are one locked step in applyWrite, so the entry IS what the
// write replaced — no read race), the post-state the live body, both
// under one lock. A brand-new doc has no entry, so old reads "". A
// rev the store no longer sits at (a newer write already landed)
// refuses — the diff would lie about the wrong write.
func (s *DocsStore) DiffAround(key string, rev int) (old, cur string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.docs[key]
	if !ok {
		return "", "", os.ErrNotExist
	}
	if rev != st.Meta.Rev {
		return "", "", errors.New(i18n.Sf("rev %d 已被后续写入覆盖（当前 rev=%d）", rev, st.Meta.Rev))
	}
	for _, h := range st.History {
		if h.Rev == rev {
			old = h.Body
			break
		}
	}
	return old, st.Body, nil
}

// --- writes ----------------------------------------------------------------

// Write upserts a doc: a new key is created (rev 1, owner = by); an
// existing one is replaced whole, archiving the current body into
// the history chain. A body past DocMaxBody refuses with
// *BodyLimitError — the silent clamp retired with the file store.
func (s *DocsStore) Write(key, title, body, by string) (DocMeta, error) {
	if err := ValidateKey(key); err != nil {
		return DocMeta{}, err
	}
	title = clampRunesTo(strings.TrimSpace(title), DocMaxTitle)
	if len(body) > DocMaxBody {
		return DocMeta{}, &BodyLimitError{Key: key, Size: len(body), Max: DocMaxBody}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(key, titleOrKey(title, key), body, by, false)
}

// WriteExpect is the conditional write behind kb_write's expect_rev
// (v0.6 §3.3): expectRev > 0 must equal the doc's current rev;
// expectRev == 0 means "must not exist yet". Any mismatch refuses with
// a *RevConflictError and the state stays untouched. The unconditional
// Write remains the last-writer-wins default for callers that don't
// care.
func (s *DocsStore) WriteExpect(key, title, body, by string, expectRev int) (DocMeta, error) {
	if err := ValidateKey(key); err != nil {
		return DocMeta{}, err
	}
	title = clampRunesTo(strings.TrimSpace(title), DocMaxTitle)
	if len(body) > DocMaxBody {
		return DocMeta{}, &BodyLimitError{Key: key, Size: len(body), Max: DocMaxBody}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, exists := s.docs[key]
	if exists && expectRev == st.Meta.Rev {
		// fast path: the expectation holds
	} else {
		cur, by2 := 0, ""
		if exists {
			cur, by2 = st.Meta.Rev, st.Meta.UpdatedBy
		} else if expectRev == 0 {
			// a brand-new doc: nothing to conflict with
			return s.writeLocked(key, titleOrKey(title, key), body, by, false)
		}
		return DocMeta{}, &RevConflictError{Key: key, ExpectRev: expectRev,
			CurrentRev: cur, UpdatedBy: by2}
	}
	return s.writeLocked(key, titleOrKey(title, key), body, by, false)
}

// Append adds one line to an existing doc (missing keys are an error:
// establish the doc with Write first, per the v0.5 spec). A joined
// body past DocMaxBody refuses with *BodyLimitError — ledgers that
// legitimately grow past the cap should ride AppendRollover.
func (s *DocsStore) Append(key, text, by string) (DocMeta, error) {
	if err := ValidateKey(key); err != nil {
		return DocMeta{}, err
	}
	text = clampRunesTo(strings.TrimSpace(text), DocMaxAppend)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(key, text, by)
}

// appendLocked commits one evAppend event: the WAL line carries the
// appended LINE (not the joined body) so replay re-joins it onto
// whatever state it finds — the runtime and replay paths stay
// byte-identical. Callers hold s.mu; key and text are validated.
func (s *DocsStore) appendLocked(key, text, by string) (DocMeta, error) {
	st, ok := s.docs[key]
	if !ok {
		return DocMeta{}, errors.New(i18n.Sf("文档 %s 不存在，请先 kb write 建立", key))
	}
	if len(joinAppend(st.Body, text)) > DocMaxBody {
		return DocMeta{}, &BodyLimitError{Key: key, Size: len(joinAppend(st.Body, text)), Max: DocMaxBody}
	}
	ev := &event{Op: evAppend, Key: key, Title: st.Meta.Title, Body: text, By: by}
	if err := s.walAppend(ev); err != nil {
		return DocMeta{}, err
	}
	return s.docs[key].Meta, nil
}

// Restore writes the content of an old revision back as a new one;
// the chain keeps growing, so nothing is ever lost.
func (s *DocsStore) Restore(key string, rev int, by string) (DocMeta, error) {
	old, err := s.Get(key, rev)
	if err != nil {
		return DocMeta{}, err
	}
	return s.Write(key, old.Title, old.Body, by)
}

// writeLocked applies one upsert as a WAL write event (op=evWrite;
// seeds ride evSeed). Callers hold s.mu and have validated key,
// clamped title and body size.
func (s *DocsStore) writeLocked(key, title, body, by string, seed bool) (DocMeta, error) {
	if st := s.docs[key]; st == nil {
		if !seed && s.activeLocked() >= DocMaxCount {
			return DocMeta{}, errors.New(i18n.Sf("文档数已达上限 %d 篇，请归档或合并现有手册", DocMaxCount))
		}
	}
	op := evWrite
	if seed {
		op = evSeed
	}
	ev := &event{Op: op, Key: key, Title: title, Body: body, By: by}
	if err := s.walAppend(ev); err != nil {
		return DocMeta{}, err
	}
	return s.docs[key].Meta, nil
}

// --- lifecycle (v3 governance) ---------------------------------------------

// Archive shelves a doc: hidden from the default list, free of the
// doc cap, still readable at its key. Rev and body are untouched —
// archive is a library move, not a content edit, so expect_rev
// anchors and the history chain stay exactly as they were.
func (s *DocsStore) Archive(key, by string) (DocMeta, error) {
	if err := ValidateKey(key); err != nil {
		return DocMeta{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.docs[key]
	if !ok {
		return DocMeta{}, os.ErrNotExist
	}
	if err := s.walAppend(&event{Op: evArchive, Key: key, By: by}); err != nil {
		return DocMeta{}, err
	}
	return st.Meta, nil
}

// Unarchive returns a shelved doc to the active list. Idempotent;
// refuses keys that don't exist. The doc cap is checked — an archive
// shelf cannot smuggle the room past DocMaxCount.
func (s *DocsStore) Unarchive(key, by string) (DocMeta, error) {
	if err := ValidateKey(key); err != nil {
		return DocMeta{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.docs[key]
	if !ok {
		return DocMeta{}, os.ErrNotExist
	}
	if st.Meta.Archived && s.activeLocked() >= DocMaxCount {
		return DocMeta{}, errors.New(i18n.Sf("文档数已达上限 %d 篇，无法恢复归档文档", DocMaxCount))
	}
	if err := s.walAppend(&event{Op: evUnarchive, Key: key, By: by}); err != nil {
		return DocMeta{}, err
	}
	return st.Meta, nil
}

// Delete removes a doc from the store entirely — body, history tail
// and all. The WAL keeps the event (audit), but no face can read the
// doc afterwards; a same-key Write starts fresh at rev 1. Host-only
// at the protocol layer precisely because it is the one way to lose
// content on purpose.
func (s *DocsStore) Delete(key, by string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.docs[key]; !ok {
		return os.ErrNotExist
	}
	return s.walAppend(&event{Op: evDelete, Key: key, By: by})
}

// AppendRollover appends one ledger line with a volume policy: while
// the joined body fits maxBytes (default DocVolMax, capped at
// DocMaxBody) it is a plain append; past it, the live body lands
// whole in a fresh archived volume doc ("<key>-vol-NNN") and the
// live doc restarts with the new line — no line is ever lost, no
// cliff is ever hit, the ledger grows as a volume set. chronicle and
// room-log ride this.
func (s *DocsStore) AppendRollover(key, text, by string, maxBytes int) (DocMeta, error) {
	if err := ValidateKey(key); err != nil {
		return DocMeta{}, err
	}
	text = clampRunesTo(strings.TrimSpace(text), DocMaxAppend)
	if maxBytes <= 0 || maxBytes > DocMaxBody {
		maxBytes = DocVolMax
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.docs[key]
	if !ok {
		return DocMeta{}, errors.New(i18n.Sf("文档 %s 不存在，请先 kb write 建立", key))
	}
	if len(st.Body)+len(text)+2 <= maxBytes {
		return s.appendLocked(key, text, by)
	}
	volKey, n := s.nextVolumeLocked(key)
	if ValidateKey(volKey) != nil {
		// a pathological key leaves no room for the volume suffix —
		// fall through to the plain append's cap error instead of
		// writing an unaddressable volume
		return DocMeta{}, &BodyLimitError{Key: key, Size: len(st.Body) + len(text) + 2, Max: DocMaxBody}
	}
	ev := &event{Op: evRoll, Key: key, Title: volumeTitle(st.Meta.Title, n),
		Body: text, By: by, ToKey: volKey}
	if err := s.walAppend(ev); err != nil {
		return DocMeta{}, err
	}
	return s.docs[key].Meta, nil
}

// nextVolumeLocked picks the next free volume number for key's
// volume family (scanning archived volumes too — numbers never
// rewind). Callers hold s.mu.
func (s *DocsStore) nextVolumeLocked(key string) (string, int) {
	prefix := key + "-vol-"
	max := 0
	for k := range s.docs {
		if strings.HasPrefix(k, prefix) {
			if n, err := strconv.Atoi(strings.TrimPrefix(k, prefix)); err == nil && n > max {
				max = n
			}
		}
	}
	n := max + 1
	return fmt.Sprintf("%s%03d", prefix, n), n
}

func volumeTitle(base string, n int) string {
	return clampRunesTo(i18n.Sf("%s · 第%d卷", base, n), DocMaxTitle)
}

// Export writes the whole store (active + archived) as a markdown
// tree under dir — the human-facing read-only snapshot that replaced
// hand-editing files on disk. One .md per doc at its key path, plus
// an _index.md manifest (key/title/rev/owner/archived). Exported
// files are never read back; returns the relative paths written.
func (s *DocsStore) Export(dir string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("# 知识库导出\n\n（只读快照——导出件不回灌，真源是数据库）\n\n")
	b.WriteString("| key | 标题 | rev | 档主 | 更新人 | 已归档 |\n|---|---|---|---|---|---|\n")
	var files []string
	for _, st := range s.docs {
		m := st.Meta
		rel := m.Key + ".md"
		if err := atomicWriteFile(filepath.Join(dir, rel), []byte(st.Body)); err != nil {
			return nil, err
		}
		files = append(files, rel)
		flag := ""
		if m.Archived {
			flag = "是"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %s |\n",
			m.Key, strings.ReplaceAll(m.Title, "|", "\\|"), m.Rev, m.Owner, m.UpdatedBy, flag)
	}
	if err := atomicWriteFile(filepath.Join(dir, "_index.md"), []byte(b.String())); err != nil {
		return nil, err
	}
	files = append(files, "_index.md")
	sort.Strings(files)
	return files, nil
}

// --- small helpers ---------------------------------------------------------

// joinAppend appends one line the v0.5 way: no trailing-newline
// surgery beyond guaranteeing the previous body ends its own line.
func joinAppend(old, text string) string {
	if text == "" {
		return old
	}
	if old != "" && !strings.HasSuffix(old, "\n") {
		old += "\n"
	}
	return old + text + "\n"
}

func titleOrKey(title, key string) string {
	if title == "" {
		return key
	}
	return title
}
