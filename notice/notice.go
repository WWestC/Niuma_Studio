// Package notice is the per-room group announcement (飞书式群公告):
// one current notice per room — the lobby plus every project room —
// published by the room owner from the chat face. The wire contract
// mirrors the task/kb frame families: conditional writes over
// expect_rev, a reminder-only broadcast on change, and a recorded
// system line so late joiners meet the notice in the chat history.
//
// The sqlite promotion moved the backing into the single database
// (notice_rows): the STORE IS THE SOURCE OF TRUTH — every read reads
// the row, every write rewrites it, and a failed write DENIES the
// publish (a silent skip would resurrect the old notice on the next
// read — the A-族 reverse this package has always kept). The
// in-memory form (a nil seam) serves embeds and tests.
package notice

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
)

// LobbyKey re-exports the reserved lobby key (the all-hands room whose
// notice is the company-wide announcement).
const LobbyKey = projects.LobbyKey

// MaxContentRunes is the notice body cap, house style with agent
// prompts: long enough for a real directive, short enough to inject
// into a member session wholesale.
const MaxContentRunes = 4000

// Notice is one room's current announcement. Rev is the
// optimistic-concurrency counter the chat publish path conditions on
// (expect_rev): 1 on publish, bumped on every update, removed with
// Clear.
type Notice struct {
	Content string `json:"content"`
	By      string `json:"by,omitempty"`
	TS      int64  `json:"ts,omitempty"`
	Rev     int    `json:"rev"`
}

// RevConflictError is SetExpect/Clear's optimistic-lock rejection (the
// kb docs' expect_rev contract): the notice moved between the caller's
// read and this write. CurrentRev is what the room actually holds now,
// so the caller can re-read, merge and retry.
type RevConflictError struct {
	Key        string
	ExpectRev  int
	CurrentRev int
	UpdatedBy  string
}

func (e *RevConflictError) Error() string {
	return i18n.Sf("rev 冲突：你期望 %d，当前已是 %d（%s 发布）",
		e.ExpectRev, e.CurrentRev, e.UpdatedBy)
}

// Engine is the per-room notice set. A non-nil rows seam puts the
// store's truth on the seam (the single database's notice rows): Get
// reads through it EVERY time (no in-memory cache — a cached copy is
// a second source of truth), SetExpect/Clear validate and write under
// one lock and REFUSE on write failure (the publish is denied, the
// old notice stands). A nil seam is the in-memory form (embeds,
// tests). The engine binds ONE storage subject at open (the seam
// calls carry it; the face does not — every face call serves the
// bound studio).
type Engine struct {
	mu      sync.Mutex
	rows    RowStore
	subject string
	mem     map[string]Notice // the nil-seam form's only backing
}

// NewMemory builds the in-memory engine (single-user subject "").
func NewMemory() *Engine {
	return &Engine{mem: map[string]Notice{}}
}

// OpenStore builds the engine over a row seam, bound to one storage
// subject ("" = the single-user historical shape, non-empty = the
// multi-user studio owner account).
func OpenStore(rows RowStore, subject string) *Engine {
	return &Engine{rows: rows, subject: subject}
}

func validKey(key string) error {
	if !projects.ValidKey(key) {
		return errors.New(i18n.Sf("非法项目 key %q（[a-z0-9_-]{1,32}）", key))
	}
	return nil
}

// Get reads key's current notice; ok is false when absent (never
// published, or cleared).
func (s *Engine) Get(key string) (Notice, bool) {
	if err := validKey(key); err != nil {
		return Notice{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok, err := s.loadLocked(key)
	if err != nil {
		return Notice{}, false // an unreadable row reads as absent (the file era's discipline)
	}
	return n, ok
}

// SetExpect writes key's notice under the kb expect_rev contract:
// expectRev > 0 must equal the current rev, 0 means "must not exist
// yet", negative (or the unconditional -1) is last-writer-wins. An
// empty/blank content refuses — taking a notice down is Clear, not a
// blank write. Returns the stored notice (rev bumped). A failed write
// DENIES the publish — the store is this domain's source of truth.
func (s *Engine) SetExpect(key, content, by string, expectRev int) (Notice, error) {
	if err := validKey(key); err != nil {
		return Notice{}, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Notice{}, errors.New(i18n.S("公告内容不能为空（撤下请用清空操作）"))
	}
	if n := len([]rune(content)); n > MaxContentRunes {
		return Notice{}, errors.New(i18n.Sf("公告超长：%d 字（上限 %d）", n, MaxContentRunes))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, exists, err := s.loadLocked(key)
	if err != nil {
		return Notice{}, err
	}
	conflict := false
	switch {
	case exists && expectRev > 0: // conditional update: must match
		conflict = expectRev != cur.Rev
	case exists && expectRev == 0: // "must not exist yet"
		conflict = true
	case !exists && expectRev > 0: // expected a live notice, room holds none
		conflict = true
	}
	if conflict {
		return Notice{}, &RevConflictError{Key: key, ExpectRev: expectRev,
			CurrentRev: cur.Rev, UpdatedBy: cur.By}
	}
	n := Notice{Content: content, By: by, TS: time.Now().Unix(), Rev: cur.Rev + 1}
	if err := s.saveLocked(key, n); err != nil {
		return Notice{}, err
	}
	return n, nil
}

// Clear takes key's notice down (rev checked the same way). Clearing
// an absent notice is a no-op success — the caller broadcasts the
// cleared event idempotently.
func (s *Engine) Clear(key, by string, expectRev int) error {
	if err := validKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, exists, err := s.loadLocked(key)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if expectRev > 0 && expectRev != cur.Rev {
		return &RevConflictError{Key: key, ExpectRev: expectRev,
			CurrentRev: cur.Rev, UpdatedBy: cur.By}
	}
	return s.dropLocked(key)
}

// loadLocked reads the row as the truth (every read re-reads; the
// in-memory map exists only in the nil-seam form).
func (s *Engine) loadLocked(key string) (Notice, bool, error) {
	if s.rows == nil {
		n, ok := s.mem[key]
		return n, ok, nil
	}
	return s.rows.Load(s.subject, key)
}

// saveLocked writes the row — a failed write denies the publish, the
// old notice stands.
func (s *Engine) saveLocked(key string, n Notice) error {
	if s.rows == nil {
		s.mem[key] = n
		return nil
	}
	return s.rows.Save(s.subject, key, n)
}

func (s *Engine) dropLocked(key string) error {
	if s.rows == nil {
		delete(s.mem, key)
		return nil
	}
	return s.rows.Remove(s.subject, key)
}

// ImportLegacyBlackboard is the one-shot retirement move for the old
// global notice (~/.niuma_blackboard.md, the retired blackboard's
// notice page): when the file holds a notice and the lobby has none
// yet, it becomes the lobby's first announcement. The old file stays
// untouched (forensics, house style). Returns whether an import
// happened.
func (s *Engine) ImportLegacyBlackboard(by string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(home, ".niuma_blackboard.md"))
	if err != nil {
		return false
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		return false
	}
	if _, ok := s.Get(LobbyKey); ok {
		return false
	}
	if _, err := s.SetExpect(LobbyKey, text, by, -1); err != nil {
		log.Printf("notice: 旧黑板公告导入失败：%v（旧文件保留）", err)
		return false
	}
	return true
}
