// v2 P4-c schedule projection (PRD §4.2 甘特段 / orchestration §4.6
// 「投影即对象」): ScheduleView folds one project's task list plus a
// version lens into the gantt view object — lanes, bars (leaf tasks and
// non-leaf group aggregates), dependency edges, derived states, the
// view range and the unscheduled inbox. Pure and headless: no engine,
// no locks, no IO; no colors either (the composition root — the server
// — paints lanes/bars with chat's member palette, so tasks never
// imports chat) and no pixel or date formatting (times stay Unix
// seconds, pct stays 0–100 int, the O6 口径).
package tasks

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/projects"
)

// Bar kinds (orchestration §4.6): a leaf task draws as itself; a task
// with children never draws a plain bar — it becomes a group aggregate
// (title + span + progress derived from its subtree's leaves, B3/M4).
const (
	BarTask  = "task"
	BarGroup = "group"
)

// Lane is one gantt swimlane: an assignee the view draws bars for.
// Color and presence are the composition root's business (chat's member
// palette, the room roster) — the projection itself stays pure.
type Lane struct {
	Assignee string `json:"assignee"`
}

// Bar is one drawable schedule row. Leaf bars mirror the task's own
// ten-field slots; group bars carry the DERIVED span (own window ∪ the
// subtree's scheduled leaves) and the DERIVED progress (integer mean of
// the subtree leaves' progress_pct — only leaves ever hold one). Deps
// lists the resolvable in-view edges: references that have no bar in
// THIS view (an unscheduled task, another project's task) are not
// drawable and stay off. Blocked/overdue are the §3.2 derived states,
// computed here — the frontend never re-derives (A-P1).
type Bar struct {
	TaskID      string   `json:"task_id"`
	Kind        string   `json:"kind"` // task | group
	Label       string   `json:"label"`
	Assignee    string   `json:"assignee"` // lane placement; "" = pool bar (no lane)
	Parent      string   `json:"parent,omitempty"`
	Start       int64    `json:"start"` // Unix seconds
	End         int64    `json:"end,omitempty"`
	ProgressPct int      `json:"progress_pct,omitempty"`
	Deps        []string `json:"deps,omitempty"`
	Milestone   bool     `json:"milestone,omitempty"`
	Status      string   `json:"status"`
	Blocked     bool     `json:"blocked"`
	Overdue     bool     `json:"overdue"`
	// Actual is the log-derived REALITY of the work (AI 节奏 vs 排期):
	// the first hop into doing is when work truly began, the last hop
	// into done is when it truly finished. The plan above may say 三天,
	// the actual below says 七秒 — the gantt's 提效 math reads both.
	ActualStart int64 `json:"actual_start,omitempty"` // first →进行中
	ActualEnd   int64 `json:"actual_end,omitempty"`   // last →已完成
}

// View is the whole gantt projection (the object dictionary's 排期视图
// row). Range is the view-fit material: the span of every emitted bar
// plus the anchor windows — the version's window under a version lens,
// ALL version windows of the project in the full-project view (v nil,
// 项目全窗); [0,0] when nothing schedules at all. VersionWindow is the
// Q7 anchor (nil when the lens has no window). Inbox is the patrol's
// work queue: the project's OPEN leaf tasks with no start_ts —
// project-wide on purpose, NOT version-lensed (scheduling an unscheduled
// task means choosing its slot, so the queue survives lens switches).
type View struct {
	ProjectKey    string   `json:"project_key"`
	Version       string   `json:"version,omitempty"` // requested lens; "" = full project
	Range         []int64  `json:"range"`
	VersionWindow []int64  `json:"version_window,omitempty"` // [start,end]; 0 side = open
	TodayTS       int64    `json:"today_ts"`
	Lanes         []Lane   `json:"lanes"`
	Bars          []Bar    `json:"bars"`
	Inbox         []string `json:"inbox"`
	// The AI-pace efficiency roll-up the overview card reads: finished
	// leaves that carry BOTH a planned window and a real span — Σ计划
	// vs Σ实际, the multiple is 提效. Zero when nothing has both yet.
	ActualDone    int   `json:"actual_done,omitempty"`         // N of comparable finished leaves
	ActualPlanned int64 `json:"actual_planned_secs,omitempty"` // Σ(end-start)
	ActualSpent   int64 `json:"actual_spent_secs,omitempty"`   // Σ(real finish − real start, ≥1s)
}

// ScheduleView projects list (one project's tasks — hand the caller's
// engine-filtered list over; the lobby default also carries unattached
// tasks) through the version lens v (nil = the whole project) at
// instant now. Group-ness follows the FULL list's parenthood, so a
// module stays a module even when a lens empties its subtree; the
// aggregate itself then folds only the lens-passing leaves. Cancelled
// tasks draw nothing and queue nothing — the gantt mirrors the living
// plan, /p/{key}/tasks keeps the whole ledger.
func ScheduleView(p projects.Project, v *projects.Version, list []Task, now time.Time) View {
	view := View{
		ProjectKey: p.Key,
		Range:      []int64{0, 0},
		TodayTS:    now.Unix(),
		Lanes:      []Lane{},
		Bars:       []Bar{},
		Inbox:      []string{},
	}
	inLens := func(t Task) bool { return v == nil || t.Version == v.Name }
	if v != nil {
		view.Version = v.Name
		if v.StartTS > 0 || v.EndTS > 0 {
			view.VersionWindow = []int64{v.StartTS, v.EndTS}
		}
	}

	byID := make(map[string]Task, len(list))
	children := make(map[string][]Task, len(list))
	for _, t := range list {
		byID[t.ID] = t
		if t.Parent != "" {
			children[t.Parent] = append(children[t.Parent], t)
		}
	}
	lookup := func(id string) (Task, bool) { t, ok := byID[id]; return t, ok }

	// deterministic sweep order: numeric id ascending (list arrives
	// newest-update-first, a rendering-irrelevant order here).
	sorted := append([]Task(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return taskNum(sorted[i].ID) < taskNum(sorted[j].ID)
	})

	// the drawable set: what the lens shows AND can place in time —
	// every non-cancelled lens-passing group plus every leaf with a
	// start. Deps edges resolve against it, so an edge never points at
	// a task this view cannot draw.
	drawable := make(map[string]struct{}, len(sorted))
	for _, t := range sorted {
		if t.Status == StatusCancelled || !inLens(t) {
			continue
		}
		if len(children[t.ID]) > 0 || t.StartTS > 0 {
			drawable[t.ID] = struct{}{}
		}
	}
	depsOf := func(t Task) []string {
		var out []string
		for _, d := range t.Deps {
			if _, ok := drawable[d]; ok {
				out = append(out, d)
			}
		}
		return out
	}

	// leavesUnder walks id's subtree collecting lens-passing,
	// non-cancelled LEAF tasks — the group aggregate's whole input. The
	// walk descends through inner nodes whatever their own lens/status
	// (the leaves decide); the visited set keeps a corrupt parent cycle
	// from hanging the projection.
	leavesUnder := func(id string) []Task {
		var out []Task
		seen := map[string]struct{}{id: {}}
		var walk func(pid string)
		walk = func(pid string) {
			for _, c := range children[pid] {
				if _, dup := seen[c.ID]; dup {
					continue
				}
				seen[c.ID] = struct{}{}
				if len(children[c.ID]) > 0 {
					walk(c.ID)
					continue
				}
				if c.Status != StatusCancelled && inLens(c) {
					out = append(out, c)
				}
			}
		}
		walk(id)
		return out
	}

	// stretch widens the range fit with one more material point (0 =
	// unset on that side).
	stretch := func(ts ...int64) {
		for _, n := range ts {
			if n <= 0 {
				continue
			}
			if view.Range[0] == 0 || n < view.Range[0] {
				view.Range[0] = n
			}
			if n > view.Range[1] {
				view.Range[1] = n
			}
		}
	}

	assignees := make(map[string]struct{}, len(sorted))
	laneStart := make(map[string]int64, len(sorted)) // lane → its earliest bar start (呈现序)
	bars := []Bar{}
	for _, t := range sorted {
		if t.Status == StatusCancelled || !inLens(t) {
			continue
		}
		if len(children[t.ID]) > 0 {
			// group aggregate: span = own window ∪ scheduled leaves;
			// progress = mean of every leaf's pct (integer, B3).
			start, end, sum, n := t.StartTS, t.EndTS, 0, 0
			for _, lf := range leavesUnder(t.ID) {
				sum += lf.ProgressPct
				n++
				if lf.StartTS > 0 && (start == 0 || lf.StartTS < start) {
					start = lf.StartTS
				}
				if lf.EndTS > 0 && (end == 0 || lf.EndTS > end) {
					end = lf.EndTS
				}
			}
			b := Bar{TaskID: t.ID, Kind: BarGroup, Label: t.Title, Assignee: t.Assignee,
				Parent: t.Parent, Start: start, End: end, Deps: depsOf(t),
				Status: t.Status, Blocked: IsBlocked(t, lookup), Overdue: IsOverdue(t, now.Unix())}
			if n > 0 {
				b.ProgressPct = sum / n
			}
			bars = append(bars, b)
		} else if t.StartTS > 0 {
			as, ae := actualSpan(t)
			bars = append(bars, Bar{TaskID: t.ID, Kind: BarTask, Label: t.Title,
				Assignee: t.Assignee, Parent: t.Parent, Start: t.StartTS, End: t.EndTS,
				ProgressPct: t.ProgressPct, Deps: depsOf(t), Milestone: t.Milestone,
				Status: t.Status, Blocked: IsBlocked(t, lookup), Overdue: IsOverdue(t, now.Unix()),
				ActualStart: as, ActualEnd: ae})
		} else {
			// an in-lens open leaf without a start draws nothing here;
			// the inbox pass below owns the queue.
			continue
		}
		b := &bars[len(bars)-1]
		if b.Assignee != "" {
			assignees[b.Assignee] = struct{}{}
			if b.Start > 0 {
				if old, ok := laneStart[b.Assignee]; !ok || b.Start < old {
					laneStart[b.Assignee] = b.Start
				}
			}
		}
		stretch(b.Start, b.End)
		// the 提效 roll-up: finished leaves with BOTH a planned window
		// and a real span (a same-second finish counts as 1s — the
		// multiple stays finite, the story stays honest).
		if b.Kind == BarTask && b.Status == StatusDone && b.End > b.Start &&
			b.ActualStart > 0 && b.ActualEnd >= b.ActualStart {
			view.ActualDone++
			view.ActualPlanned += b.End - b.Start
			if spent := b.ActualEnd - b.ActualStart; spent > 0 {
				view.ActualSpent += spent
			} else {
				view.ActualSpent++
			}
		}
	}

	// the patrol inbox, its own whole-project pass: OPEN leaf tasks
	// with no start, lens-independent on purpose (scheduling an
	// unscheduled task means choosing its slot — the queue survives
	// lens switches). Modules never queue (they draw group aggregates,
	// derived or empty); done/cancelled leaves need no scheduling.
	for _, t := range sorted {
		if len(children[t.ID]) > 0 || t.StartTS > 0 {
			continue
		}
		if t.Status == StatusTodo || t.Status == StatusDoing {
			view.Inbox = append(view.Inbox, t.ID)
		}
	}

	// the range anchor: the lens version's window, or — full-project
	// view — every window the project declares (项目全窗).
	if v != nil {
		stretch(v.StartTS, v.EndTS)
	} else {
		for _, w := range p.Versions {
			stretch(w.StartTS, w.EndTS)
		}
	}

	// lanes: the drawn assignees, deduped. 呈现序 = 服务端定, and the
	// order follows work, not the alphabet: lanes rise by their earliest
	// bar start (name breaks ties) so the top of the gantt is where the
	// schedule actually begins — alphabetical order strands the only
	// visible bars in bottom lanes under a wall of empty top lanes.
	// Pool bars (empty assignee) draw without a lane.
	for a := range assignees {
		view.Lanes = append(view.Lanes, Lane{Assignee: a})
	}
	sort.Slice(view.Lanes, func(i, j int) bool {
		si, sj := laneStart[view.Lanes[i].Assignee], laneStart[view.Lanes[j].Assignee]
		if si != sj {
			return si < sj
		}
		return view.Lanes[i].Assignee < view.Lanes[j].Assignee
	})

	sort.SliceStable(bars, func(i, j int) bool {
		if bars[i].Start != bars[j].Start {
			return bars[i].Start < bars[j].Start
		}
		return taskNum(bars[i].TaskID) < taskNum(bars[j].TaskID)
	})
	view.Bars = bars
	return view
}

// taskNum parses the t_NN id's numeric half (0 for anything else) — the
// engine's own Sscanf idiom, shared by the sweep and draw orders.
func taskNum(id string) int {
	var n int
	if _, err := fmt.Sscanf(id, "t_%d", &n); err != nil {
		return 0
	}
	return n
}

// actualSpan resolves the work's REAL clock. The structured stamps
// (Task.DoingTS/DoneTS, written by applyPatch at the hop itself) win
// per-field; the applied-log trail below is the legacy fallback — it
// scrapes applyPatch's「状态 A→B」notes, which means i18n wording is
// its protocol: transitions may share a note with other diffs, so the
// scan matches the arrow segment, not the whole line. Missing hops
// stay 0 (created-as-doing, pre-stamp ledgers): the caller treats 0 as
// "no reality recorded".
func actualSpan(t Task) (int64, int64) {
	var start, end int64
	for _, e := range t.Log {
		if start == 0 && strings.Contains(e.Note, "→"+statusLabel(StatusDoing)) {
			start = e.TS
		}
		if strings.Contains(e.Note, "→"+statusLabel(StatusDone)) {
			end = e.TS
		}
	}
	if t.DoingTS != 0 {
		start = t.DoingTS
	}
	if t.DoneTS != 0 {
		end = t.DoneTS
	}
	return start, end
}
