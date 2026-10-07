package persist

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestSaveRoundTripAndOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "data.json")
	if err := Save(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := Save(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != "second" {
		t.Fatalf("contents = %q, want %q", b, "second")
	}
}

func TestSaveLeavesNoTempResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := Save(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "data.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir holds %v, want exactly [data.json]", names)
	}
}

func TestSaveModeHonored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := Save(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Windows 的 os.Chmod 只认只读位——常规文件一律显示 0666，私有性
	// 由用户目录的 ACL 承担（凭据全部落在 %USERPROFILE% 下）。POSIX
	// 面仍钉死 0600：凭证保存不许有世界可读窗口。
	if perm := info.Mode().Perm(); perm != 0o600 && runtime.GOOS != "windows" {
		t.Fatalf("perm = %o, want 600 (the temp file is 0600 from birth; a credential save must never be wider)", perm)
	}
}

func TestSaveMarshalErrorTouchesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	if err := SaveJSON(path, map[string]any{"ok": 1}, 0o644); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	before, _ := os.ReadFile(path)
	if err := SaveJSON(path, map[string]any{"bad": make(chan int)}, 0o644); err == nil {
		t.Fatal("marshal of a channel must error")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("failed marshal mutated the file: %q -> %q", before, after)
	}
}

// Concurrent saves to one path must always leave a fully-formed payload
// from ONE writer — the atomicity contract every store's saveLocked
// leans on (a torn file would make the next Open refuse to boot).
func TestSaveConcurrentAtomicity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := make([]byte, 4096)
			for j := range b {
				b[j] = byte('a' + i%26)
			}
			if err := Save(path, b, 0o644); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	first := b[0]
	for i, c := range b {
		if c != first {
			t.Fatalf("byte %d differs (%q vs %q): torn write — two writers' bytes interleaved", i, c, first)
		}
	}
}
