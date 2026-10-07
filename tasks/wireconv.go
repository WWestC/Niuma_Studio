package tasks

// wireconv.go — the hand-written remainder of the mirror bridge: the
// Wire()/FromWire() conversions for Task/Patch/Proposal moved next
// door into wireconv_gen.go (generated from both struct shapes by
// `go run ./tools/wiregen` — a field change is domain + wire mirror +
// fixture, then one wiregen run). What stays HERE is the non-
// mechanical face: the read-side sugar whose shape is a consumption
// choice, not a struct-shape derivation.

import "github.com/WWestC/Niuma_Studio/wire"

// WireTasksOf is TasksOf's wire-mirror face: the same ordering, the
// tasks already projected onto wire.Task. Read-side consumers that
// speak only mirrors (kb's TaskLedger, structural) drink here instead
// of the domain slice — the JSON stays byte-identical either way
// (wire's compat test pins that).
func (e *Engine) WireTasksOf(name string) []wire.Task {
	ts := e.TasksOf(name)
	if len(ts) == 0 {
		return nil
	}
	out := make([]wire.Task, len(ts))
	for i := range ts {
		out[i] = *ts[i].Wire()
	}
	return out
}
