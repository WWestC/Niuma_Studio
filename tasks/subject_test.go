package tasks

// subject_test.go — the memory backing's one-subject door: the
// in-memory shape has one engine, not one per namespace, so a
// non-empty storage subject must refuse loudly (ErrSingleSubject,
// the multi-user mode requires-sqlite backstop) instead of silently
// sharing one subject's shelves with another.

import (
	"testing"
)

func TestLocalStoreRefusesSubject(t *testing.T) {
	l := NewMemStore()
	if _, _, err := l.OpenShelves("alice"); err == nil || err.Error() != ErrSingleSubject("alice").Error() {
		t.Fatalf("OpenShelves 非空主体应拒（逐字节钉构造器）: %v", err)
	}
	if err := l.SaveShelf("alice", "demo", &Shelf{Next: 1}); err == nil || err.Error() != ErrSingleSubject("alice").Error() {
		t.Fatalf("SaveShelf 非空主体应拒: %v", err)
	}
	if err := l.DeleteShelf("alice", "demo"); err == nil {
		t.Fatal("DeleteShelf 非空主体应拒")
	}
	if err := l.SaveRanks("alice", map[string]int{"小明": 5}); err == nil {
		t.Fatal("SaveRanks 非空主体应拒")
	}
	// OpenStoreNS 走到同一扇门。
	if _, err := OpenStoreNS(l, "alice", "房主"); err == nil || err.Error() != ErrSingleSubject("alice").Error() {
		t.Fatalf("OpenStoreNS 非空主体应拒: %v", err)
	}
	// 空主体（单用户历史形状）不受影响。
	if _, err := OpenStoreNS(l, "", "房主"); err != nil {
		t.Fatalf("空主体应照常开: %v", err)
	}
}
