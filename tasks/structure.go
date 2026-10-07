// v2 structural validation (PRD §4.2 / model §4.1): pure headless
// checks over the ten-field Task extension. Nothing here touches the
// Engine, the wire or persistence — P4 wires these into the task
// write paths; until then callers (tests, later the engine) hand the
// task set over explicitly.
package tasks

import (
	"errors"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// MaxTreeDepth is the task-tree depth cap (★B3): 模块(L1) → 工单(L2) →
// 子票(L3). A fourth level is a modeling error, not a scheduling one.
const MaxTreeDepth = 3

// depVisit states for the three-color cycle walk.
const (
	depWhite = iota
	depGray  // on the current walk's stack
	depBlack // fully walked, proven acyclic
)

// CheckStructure validates the ten-field invariants across a whole
// task set:
//
//   - every id non-empty and unique (callers normally get this from
//     the engine's counter; the check keeps hand-built sets honest);
//   - project_key, when set, must be a known project (empty is the v1
//     shape — unattached, lobby by read-side convention — and stays
//     legal even when knownProjects is empty);
//   - parent must resolve in-set, stay inside one project, keep the
//     tree acyclic and no deeper than MaxTreeDepth;
//   - start_ts ≤ end_ts whenever both are set (Unix seconds);
//   - deps must resolve in-set, never self-reference, never cycle,
//     and stay inside one project (v2.10 isolation: ids are
//     per-project, so a cross-shelf dep token is ambiguous — projects
//     coordinate through the host, not through each other's ledgers);
//   - progress_pct within 0–100.
//
// knownProjects lists the project keys the set may attach to (the
// wiring builds it from the projects store).
func CheckStructure(list []Task, knownProjects []string) error {
	known := make(map[string]struct{}, len(knownProjects))
	for _, k := range knownProjects {
		known[k] = struct{}{}
	}
	byID := make(map[string]*Task, len(list))
	for i := range list {
		t := &list[i]
		if t.ID == "" {
			return errors.New(i18n.S("任务 id 不能为空"))
		}
		if _, dup := byID[t.ID]; dup {
			return errors.New(i18n.Sf("任务 id 重复: %s", t.ID))
		}
		byID[t.ID] = t
	}

	for i := range list {
		t := &list[i]
		if t.ProjectKey != "" {
			if _, ok := known[t.ProjectKey]; !ok {
				return errors.New(i18n.Sf("任务 %s 挂靠未知项目 %q", t.ID, t.ProjectKey))
			}
		}
		if t.StartTS > 0 && t.EndTS > 0 && t.StartTS > t.EndTS {
			return errors.New(i18n.Sf("任务 %s 的排期起点晚于终点", t.ID))
		}
		if t.ProgressPct < 0 || t.ProgressPct > 100 {
			return errors.New(i18n.Sf("任务 %s 的 progress_pct 越界（0–100）: %d", t.ID, t.ProgressPct))
		}
		if t.Parent == t.ID {
			return errors.New(i18n.Sf("任务 %s 的 parent 指向自己", t.ID))
		}
		if t.Parent != "" {
			if _, ok := byID[t.Parent]; !ok {
				return errors.New(i18n.Sf("任务 %s 的 parent 不存在: %s", t.ID, t.Parent))
			}
			if byID[t.Parent].ProjectKey != t.ProjectKey {
				return errors.New(i18n.Sf("任务 %s 的 parent 跨项目（%q → %q）", t.ID,
					t.ProjectKey, byID[t.Parent].ProjectKey))
			}
		}
		for _, d := range t.Deps {
			if d == t.ID {
				return errors.New(i18n.Sf("任务 %s 的 deps 含自指", t.ID))
			}
			dep, ok := byID[d]
			if !ok {
				return errors.New(i18n.Sf("任务 %s 的依赖不存在: %s", t.ID, d))
			}
			// v2.10 isolation: deps stay inside one project. Ids are
			// per-project shelves now, so a bare cross-project dep token
			// is not just leaky — it is ambiguous (two projects' t_01 are
			// strangers). Projects that need each other's work go through
			// the host, like every other cross-room resource.
			if SlotKey(dep.ProjectKey) != SlotKey(t.ProjectKey) {
				return errors.New(i18n.Sf("任务 %s 的依赖跨项目（%s 属于 %s；项目间隔离，跨房依赖请走房主协调）",
					t.ID, d, dep.ProjectKey))
			}
		}
	}

	// Parent chains: acyclic and no deeper than MaxTreeDepth. Walking
	// from every task with a parent covers every tree edge; each walk
	// is bounded by the nodes it visits, so a cycle anywhere on the
	// chain revisits a node.
	for i := range list {
		t := &list[i]
		if t.Parent == "" {
			continue
		}
		seen := map[string]struct{}{t.ID: {}}
		cur := t
		for nodes := 1; cur.Parent != ""; nodes++ {
			cur = byID[cur.Parent]
			if _, ok := seen[cur.ID]; ok {
				return errors.New(i18n.Sf("任务树的 parent 链成环（涉及 %s）", cur.ID))
			}
			seen[cur.ID] = struct{}{}
			if nodes+1 > MaxTreeDepth {
				return errors.New(i18n.Sf("任务 %s 超过 %d 层（模块→工单→子票）", t.ID, MaxTreeDepth))
			}
		}
	}

	// Deps cycles: three-color DFS in list order (deterministic).
	state := make(map[string]int, len(byID))
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case depGray:
			return errors.New(i18n.Sf("依赖成环（涉及 %s）", id))
		case depBlack:
			return nil
		}
		state[id] = depGray
		for _, d := range byID[id].Deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = depBlack
		return nil
	}
	for i := range list {
		if err := visit(list[i].ID); err != nil {
			return err
		}
	}
	return nil
}
