package storetest

// sqlite.go — the single-database backing's shared harness openers:
// one fresh studio.db per test backing, engines bound per subject
// over it. Used by sqlstore's contract runs and by any package's
// persistence-shaped tests (reopen = a brand-new sqlstore.Open over
// the same file — the restart simulation).

import (
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/sqlstore"
	"github.com/WWestC/Niuma_Studio/tasks"
)

func openDB(t *testing.T, path string) *sqlstore.DB {
	t.Helper()
	db, err := sqlstore.Open(path)
	if err != nil {
		t.Fatalf("sqlstore.Open: %v", err)
	}
	// POSIX 删得动打开中的库文件，Windows 删不动——不关库时
	// t.TempDir 的清理在 Windows 整族炸「being used by another
	// process」，三平台 CI 走同一份测试就得像生产一样关库。
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ReqSQLiteBacking builds the requirements harness over a fresh db.
func ReqSQLiteBacking(t *testing.T) (open func(string) requirements.Store, reopen func(string) func(*testing.T) requirements.Store) {
	path := filepath.Join(t.TempDir(), "studio.db")
	openDB(t, path)
	open = func(subject string) requirements.Store {
		s, err := requirements.OpenStore(openDB(t, path).ReqShelves(), subject)
		if err != nil {
			t.Fatalf("requirements.OpenStore: %v", err)
		}
		return s
	}
	reopen = func(subject string) func(*testing.T) requirements.Store {
		return func(t *testing.T) requirements.Store {
			s, err := requirements.OpenStore(openDB(t, path).ReqShelves(), subject)
			if err != nil {
				t.Fatalf("requirements reopen: %v", err)
			}
			return s
		}
	}
	return open, reopen
}

// PlanSQLiteBacking builds the plan harness over a fresh db.
func PlanSQLiteBacking(t *testing.T) (open func(string) plan.Store, reopen func(string) func(*testing.T) plan.Store) {
	path := filepath.Join(t.TempDir(), "studio.db")
	openDB(t, path)
	open = func(subject string) plan.Store {
		s, err := plan.OpenStore(openDB(t, path).PlanSlots(), subject)
		if err != nil {
			t.Fatalf("plan.OpenStore: %v", err)
		}
		return s
	}
	reopen = func(subject string) func(*testing.T) plan.Store {
		return func(t *testing.T) plan.Store {
			s, err := plan.OpenStore(openDB(t, path).PlanSlots(), subject)
			if err != nil {
				t.Fatalf("plan reopen: %v", err)
			}
			return s
		}
	}
	return open, reopen
}

// MergeSQLiteBacking builds the merge harness over a fresh db.
func MergeSQLiteBacking(t *testing.T) (open func(string) merge.Store, reopen func(string) func(*testing.T) merge.Store) {
	path := filepath.Join(t.TempDir(), "studio.db")
	openDB(t, path)
	open = func(subject string) merge.Store {
		s, err := merge.OpenStore(openDB(t, path).MergeSlots(), subject)
		if err != nil {
			t.Fatalf("merge.OpenStore: %v", err)
		}
		return s
	}
	reopen = func(subject string) func(*testing.T) merge.Store {
		return func(t *testing.T) merge.Store {
			s, err := merge.OpenStore(openDB(t, path).MergeSlots(), subject)
			if err != nil {
				t.Fatalf("merge reopen: %v", err)
			}
			return s
		}
	}
	return open, reopen
}

// MeetingSQLiteBacking builds the meeting harness over a fresh db
// (subject-bound; the suite's "" calls ride the "" binding).
func MeetingSQLiteBacking(t *testing.T) (open func(string) *meeting.Engine, reopen func(string) func(*testing.T) *meeting.Engine) {
	path := filepath.Join(t.TempDir(), "studio.db")
	openDB(t, path)
	open = func(subject string) *meeting.Engine {
		s, err := meeting.OpenStore(openDB(t, path).MeetingShelves(), subject)
		if err != nil {
			t.Fatalf("meeting.OpenStore: %v", err)
		}
		return s
	}
	reopen = func(subject string) func(*testing.T) *meeting.Engine {
		return func(t *testing.T) *meeting.Engine {
			s, err := meeting.OpenStore(openDB(t, path).MeetingShelves(), subject)
			if err != nil {
				t.Fatalf("meeting reopen: %v", err)
			}
			return s
		}
	}
	return open, reopen
}

// NoticeSQLiteBacking builds the notice harness over a fresh db.
func NoticeSQLiteBacking(t *testing.T) (open func(string) *notice.Engine, reopen func(string) func(*testing.T) *notice.Engine) {
	path := filepath.Join(t.TempDir(), "studio.db")
	openDB(t, path)
	open = func(subject string) *notice.Engine {
		return notice.OpenStore(openDB(t, path).NoticeRows(), subject)
	}
	reopen = func(subject string) func(*testing.T) *notice.Engine {
		return func(t *testing.T) *notice.Engine {
			return notice.OpenStore(openDB(t, path).NoticeRows(), subject)
		}
	}
	return open, reopen
}

// TaskSQLiteBacking builds the tasks harness over a fresh db.
func TaskSQLiteBacking(t *testing.T) (open func(string) *tasks.Engine, reopen func(string) func(*testing.T) *tasks.Engine) {
	path := filepath.Join(t.TempDir(), "studio.db")
	openDB(t, path)
	open = func(subject string) *tasks.Engine {
		e, err := tasks.OpenStoreNS(openDB(t, path).Tasks(), subject, "房主")
		if err != nil {
			t.Fatalf("tasks.OpenStoreNS: %v", err)
		}
		return e
	}
	reopen = func(subject string) func(*testing.T) *tasks.Engine {
		return func(t *testing.T) *tasks.Engine {
			e, err := tasks.OpenStoreNS(openDB(t, path).Tasks(), subject, "房主")
			if err != nil {
				t.Fatalf("tasks reopen: %v", err)
			}
			return e
		}
	}
	return open, reopen
}
