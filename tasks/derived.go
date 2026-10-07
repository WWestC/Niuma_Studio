// v2 P4-a derived scheduling state (PRD §4.2 / orchestration §3.2):
// blocked and overdue are COMPUTED, never stored — the four-state
// machine stays minimal, the projections (board page, ScheduleView,
// web gantt) all call these from one home. Pure reads, no locks: the
// caller hands the resolver, the engine wires it under its own.
package tasks

// DepLookup resolves a dependency id to its task. Engines answer from
// the live ledger; tests from a map — the derived state never owns
// storage.
type DepLookup func(id string) (Task, bool)

// IsBlocked reports the blocked derived state: a todo task holding at
// least one prerequisite that is not done. The dependency stays a soft
// lock (警示不硬禁 — AI collaboration runs parallel, §3.2), so this is a
// warning flag, not a gate. An unresolvable dep counts as blocking —
// the write path keeps deps resolvable, so a miss is corruption worth
// drawing attention to, not a reason to go quiet.
func IsBlocked(t Task, byID DepLookup) bool {
	if t.Status != StatusTodo || len(t.Deps) == 0 {
		return false
	}
	for _, d := range t.Deps {
		if dep, ok := byID(d); !ok || dep.Status != StatusDone {
			return true
		}
	}
	return false
}

// IsOverdue reports the overdue derived state: now past the task's
// end_ts while it is still open (todo or doing). A task without an
// end_ts is never overdue; done/cancelled tasks never are — their
// UpdatedTS is the completion moment (A4).
func IsOverdue(t Task, now int64) bool {
	return t.EndTS > 0 && now > t.EndTS &&
		(t.Status == StatusTodo || t.Status == StatusDoing)
}
