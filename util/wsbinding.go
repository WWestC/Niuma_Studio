package util

// The workspace→project binding table (~/.niuma/ws-projects.json): the
// session-scope backbone of project isolation (v2.9). AI members are
// ZCode sessions whose shells run the niuma CLI with cwd pinned to
// their birth workspace (the project's shared folder or the member's
// isolated worktree) — there is no per-session env channel to stamp
// (every session rides one shared app-server child), so the CLI
// resolves its default project scope from WHERE it runs instead: this
// table maps each workspace path to its office key, the dispatcher
// writes it at room start and worktree creation, and the CLI probes
// cwd and its ancestors against it. The host — running from anywhere
// unbound — stays studio-wide by design; 小助手 shares the lobby's
// workspace and reads the lobby+public shelf, matching its 大厅 seat.
//
// The table is machine-local state under ~/.niuma (like the port
// file): a stale binding (workspace moved, project deleted) is only a
// wrong DEFAULT for reads — every enforcement that matters (doc
// writes, plan/task assignment) is identity-checked server-side.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/WWestC/Niuma_Studio/persist"
)

var wsBindMu sync.Mutex

// wsProjectsPath is the table's file: ~/.niuma/ws-projects.json.
func wsProjectsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma", "ws-projects.json"), nil
}

// ReadWorkspaceProjects loads the binding table; a missing file is an
// empty table, never an error (the un-scoped default).
func ReadWorkspaceProjects() (map[string]string, error) {
	p, err := wsProjectsPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// BindWorkspaceProject records one binding (idempotent upsert,
// read-modify-write under the process mutex — the fleet is the sole
// writer and serializes its dispatchers anyway). A key of "" REMOVES
// the binding (project deletion keeps the table honest). Paths are
// cleaned on write; the CLI probes with cleaned paths too.
func BindWorkspaceProject(workspace, key string) error {
	ws := filepath.Clean(workspace)
	if ws == "" || ws == "." || ws == string(filepath.Separator) {
		return nil // never bind a meaningless root
	}
	wsBindMu.Lock()
	defer wsBindMu.Unlock()
	m, err := ReadWorkspaceProjects()
	if err != nil {
		return err
	}
	if key == "" {
		delete(m, ws)
	} else if m[ws] == key {
		return nil
	} else {
		m[ws] = key
	}
	p, err := wsProjectsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return persist.Save(p, append(b, '\n'), 0o644)
}

// ProjectOfWorkspace answers the binding for one exact path (no
// ancestor walk — that policy lives with the CLI, which owns cwd).
func ProjectOfWorkspace(workspace string) (string, bool) {
	m, err := ReadWorkspaceProjects()
	if err != nil {
		return "", false
	}
	key, ok := m[filepath.Clean(workspace)]
	return key, ok
}
