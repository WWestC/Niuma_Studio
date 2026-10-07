// importlegacy.go — the one-shot v3 migration: the legacy ~/.niuma_kb
// markdown tree (one .md per key, .history/*.jsonl chains, an
// .index.json metadata index) folds into the new log-structured
// store under ~/.niuma/kb. Bodies, titles, owners, revs and the
// 20-entry history tails all cross over; the tree is then retired to
// a .imported-<ts> backup beside its old home (factory reset's wipe
// list cleans those). Runs before OpenDocs at boot — the store itself
// never looks at the legacy layout.
package kb

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ImportLegacy imports the legacy markdown tree at legacyDir into a
// fresh store at newDir (which must not exist yet). Returns how many
// docs crossed over and the backup path the tree was retired to;
// imported=0 with a nil error means the tree held nothing importable
// and was left untouched.
func ImportLegacy(newDir, legacyDir string) (imported int, backup string, err error) {
	if _, serr := os.Stat(newDir); serr == nil {
		return 0, "", fmt.Errorf("目标库已存在，拒绝导入：%s", newDir)
	}
	legacy, err := readLegacyTree(legacyDir)
	if err != nil {
		return 0, "", err
	}
	if len(legacy) == 0 {
		return 0, "", nil
	}
	s := &DocsStore{dir: newDir, docs: legacy}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return 0, "", err
	}
	// the imported state lands as one snapshot (applied=0, empty log)
	// — replay reproduces nothing because nothing was lost
	if err := s.compactLocked(); err != nil {
		return 0, "", err
	}
	backup = fmt.Sprintf("%s.imported-%d", legacyDir, time.Now().Unix())
	if err := os.Rename(legacyDir, backup); err != nil {
		return imported, "", err
	}
	return len(legacy), backup, nil
}

// ReconcileLegacy merges a legacy tree into a STANDING store — the
// accidental-seed window's repair: an app that relaunched mid-upgrade
// (new storage engine wired, boot migration not yet) seeded a fresh
// store while the real tree still waited in ~/.niuma_kb. Rules per
// legacy doc:
//
//   - key absent from the store → imported whole (rev + history tail);
//   - store doc still byte-equal to its seed render → legacy wins
//     (the seed was the accident, the tree is the truth);
//   - store doc grew past its seed prefix (a ledger line landed in
//     the window) → legacy body + the post-seed tail — neither side
//     drops a line;
//   - anything else the store holds (docs the window itself authored,
//     edits beyond a seed prefix) stays untouched — those writes are
//     as real as any.
//
// Boot-time only (the caller serializes before the store serves);
// the merged state persists as one snapshot. Retiring the tree to
// its backup is the caller's (a successful reconcile has earned it).
func ReconcileLegacy(s *DocsStore, legacyDir string) (imported, merged, kept int, err error) {
	legacy, err := readLegacyTree(legacyDir)
	if err != nil || len(legacy) == 0 {
		return 0, 0, 0, err
	}
	seeds := map[string]string{}
	for _, seed := range seedDocs() {
		seeds[seed.key] = seed.body
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, l := range legacy {
		st := s.docs[key]
		if st == nil {
			s.docs[key] = l
			imported++
			continue
		}
		seed, isSeed := seeds[key]
		switch {
		case isSeed && st.Body == seed:
			// pristine seed: the tree's history replaces the accident
			s.docs[key] = l
			imported++
		case isSeed && strings.HasPrefix(st.Body, seed):
			// a ledger that kept appending through the window: the
			// tree's full history + the window's own tail
			tail := st.Body[len(seed):]
			nb := l.Body
			if nb != "" && !strings.HasSuffix(nb, "\n") {
				nb += "\n"
			}
			l.Body = nb + tail
			l.Meta.Size = len(l.Body)
			if st.Meta.Rev > l.Meta.Rev {
				l.Meta.Rev = st.Meta.Rev // the counter never rewinds
			}
			if st.Meta.UpdatedTS > l.Meta.UpdatedTS {
				l.Meta.UpdatedTS = st.Meta.UpdatedTS
				l.Meta.UpdatedBy = st.Meta.UpdatedBy
			}
			s.docs[key] = l
			merged++
		default:
			// the window's own writes stand; the tree keeps its copy
			// in the backup
			kept++
		}
	}
	if err := s.compactLocked(); err != nil {
		return imported, merged, kept, err
	}
	return imported, merged, kept, nil
}

// MigrateLegacyIfNeeded is the boot-time hook. Three postures: no
// legacy tree → nothing to do (fresh install seeds as usual); no
// store → one-shot full import; store AND tree (the accidental-seed
// window — a mid-upgrade relaunch seeded fresh before this wiring
// existed) → ReconcileLegacy's merge, which keeps the window's own
// writes and retires the tree. Returns the backup path when a
// migration ran, "" when there was nothing to do.
func MigrateLegacyIfNeeded() (string, error) {
	newDir, err := DefaultDocsDir()
	if err != nil {
		return "", err
	}
	legacyDir, err := LegacyDocsDir()
	if err != nil {
		return "", err
	}
	if _, serr := os.Stat(legacyDir); serr != nil {
		return "", nil // no legacy tree — fresh install or already migrated
	}
	if _, serr := os.Stat(newDir); os.IsNotExist(serr) {
		_, backup, err := ImportLegacy(newDir, legacyDir)
		return backup, err
	}
	// the standing store + waiting tree: the accidental-seed window
	s, err := OpenDocs(newDir)
	if err != nil {
		return "", fmt.Errorf("旧黑板文档树在场但目标库打不开，跳过合并: %w", err)
	}
	imported, merged, kept, err := ReconcileLegacy(s, legacyDir)
	if err != nil {
		return "", err
	}
	log.Printf("docs: 旧黑板文档树并入新库——导入 %d 篇、并卷 %d 篇、保留新库版 %d 篇", imported, merged, kept)
	backup := fmt.Sprintf("%s.imported-%d", legacyDir, time.Now().Unix())
	if err := os.Rename(legacyDir, backup); err != nil {
		return "", err
	}
	return backup, nil
}

// readLegacyTree walks the legacy markdown tree into full docStates:
// bodies from the .md files, titles/owners/revs from .index.json when
// present (hand-created files fall back to synthesized metadata),
// history tails from the .history/*.jsonl chains — the exact state
// the file store would have loaded.
func readLegacyTree(legacyDir string) (map[string]*docState, error) {
	out := map[string]*docState{}
	idx := map[string]DocMeta{}
	if b, rerr := os.ReadFile(filepath.Join(legacyDir, docsIndexFile)); rerr == nil {
		_ = json.Unmarshal(b, &idx)
	}
	werr := filepath.WalkDir(legacyDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(legacyDir, path)
		if rerr != nil || !strings.HasSuffix(rel, ".md") || strings.HasPrefix(rel, ".") {
			return nil
		}
		key := strings.ReplaceAll(strings.TrimSuffix(rel, ".md"), string(filepath.Separator), "/")
		if ValidateKey(key) != nil {
			return nil
		}
		info, serr := d.Info()
		if serr != nil {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		meta := idx[key]
		st := &docState{Body: string(body), History: readLegacyHistory(legacyDir, key)}
		st.Meta = DocMeta{
			Key: key, Title: meta.Title, Owner: meta.Owner,
			UpdatedBy: meta.UpdatedBy, UpdatedTS: meta.UpdatedTS,
			Rev: meta.Rev, Size: len(body),
		}
		if st.Meta.Title == "" {
			st.Meta.Title = titleFromBody(st.Body, key)
		}
		if st.Meta.Owner == "" {
			st.Meta.Owner = "(导入)"
		}
		if st.Meta.UpdatedBy == "" {
			st.Meta.UpdatedBy = "(磁盘直编)"
		}
		if st.Meta.UpdatedTS == 0 {
			st.Meta.UpdatedTS = info.ModTime().Unix()
		}
		if st.Meta.Rev <= 0 {
			// rev continuation: a hand-deleted-and-recreated file picks
			// up after its own history chain's maximum (nextRevLocked's
			// old discipline)
			st.Meta.Rev = 1
			for _, h := range st.History {
				if h.Rev >= st.Meta.Rev {
					st.Meta.Rev = h.Rev + 1
				}
			}
		}
		out[key] = st
		return nil
	})
	if werr != nil {
		return nil, werr
	}
	return out, nil
}

// readLegacyHistory reads one legacy chain file (.history/<key with
// / → __>.jsonl), same record shape as HistoryEntry, trimmed to the
// tail the old store kept.
func readLegacyHistory(legacyDir, key string) []HistoryEntry {
	name := strings.ReplaceAll(key, "/", "__") + ".jsonl"
	b, err := os.ReadFile(filepath.Join(legacyDir, docsHistoryDir, name))
	if err != nil {
		return nil
	}
	var out []HistoryEntry
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		var h HistoryEntry
		if json.Unmarshal([]byte(line), &h) == nil {
			out = append(out, h)
		}
	}
	if over := len(out) - DocHistoryTail; over > 0 {
		out = out[over:]
	}
	return out
}
