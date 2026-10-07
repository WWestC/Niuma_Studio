package sqlstore

// helpers_test.go — the shared throwaway-database opener: a FRESH
// database per call (every Open hands its caller an empty store);
// reopen constructs a new handle over the same file — the crash-and-
// restart simulation WAL guarantees to honor.

import (
	"path/filepath"
	"testing"
)

func openDB(t *testing.T) (*DB, func(t *testing.T) *DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "studio.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	reopen := func(t *testing.T) *DB {
		t.Helper()
		d2, err := Open(path)
		if err != nil {
			t.Fatalf("reopen %s: %v", path, err)
		}
		return d2
	}
	return d, reopen
}
