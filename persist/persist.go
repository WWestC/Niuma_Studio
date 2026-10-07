// Package persist owns the studio's on-disk durability invariant: every
// store save is one atomic commit — temp file in the target's own
// directory (same filesystem, so the swap cannot tear), fsync before
// the rename, directory fsync after it. Until this package existed the
// tmp+rename dance was copied per package (a dozen hand-rolled
// saveLocked variants, none fsyncing); the copy count was the bug
// surface — a discipline that lives in one place cannot drift per
// package. Callers keep their own logging/message discipline at the
// call site ("an error is an ambient note, never a refusal; the
// in-memory state stays ahead until the next save").
package persist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// saveLocks serializes concurrent Saves per target path. POSIX rename
// atomically replaces an open destination; Windows MoveFileEx refuses
// with Access Denied the moment another handle holds the target — two
// racing saves to one credential/ledger file fail on Windows without
// this. Per-path (not global): different rooms' ledgers keep saving in
// parallel.
var saveLocks sync.Map // abs path → *sync.Mutex

// Save commits data to path atomically. Mode is the final permission
// bits (0o644 data, 0o600 credentials); the temp file is created 0600
// and never widened until after its bytes are on disk, so a credential
// save has no world-readable window at any point. Parent directories
// are created as needed. A crash mid-save leaves the previous contents
// intact plus one orphan temp file; the next Save does not reuse it.
func Save(path string, data []byte, mode os.FileMode) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	muAny, _ := saveLocks.LoadOrStore(abs, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename has committed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	// The rename is durable only once the directory entry itself is on
	// disk. Windows cannot fsync an open directory — the rename is
	// still atomic there, and the next save re-commits the same bytes.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// SaveJSON marshals v with the house two-space indent and commits it
// with Save. A marshal error surfaces before any file is touched, so a
// serialization bug can never truncate the previous contents.
func SaveJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return Save(path, b, mode)
}
