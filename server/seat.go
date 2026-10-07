package server

// seat.go — the verb contract layer's assembly point: everything the
// shell owes the verb bodies, wired in ONE place. This is boot_fleet's
// pattern applied inward — a func field per shell helper, the whole
// table reviewable at a glance. As domain faces migrate out of the
// shell (the family split), their taps move with them: this table's
// rows only ever leave.

import (
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// ensureSeat lazily builds the seat for bare-constructed servers
// (tests assemble &Server{} field-by-field; faces must self-heal
// rather than nil-panic through the seat).
func (s *Server) ensureSeat() {
	if s.seat == nil {
		s.seat = s.newVerbsSeat()
	}
}

// newVerbsSeat builds the Seat over this server: the shared fields
// plus the full tap table (shell-owned helpers, transitional
// scaffolding with a ratchet).
func (s *Server) newVerbsSeat() *verbs.Seat {
	return &verbs.Seat{
		Stores:   s.opts.Stores,
		Fleet:    s.opts.Fleet,
		Local:    s.opts.LocalName,
		Subject:  s.opts.Subject,
		Registry: s.opts.Registry,
		Hub:      s.hub,
		Taps: verbs.Taps{
			// Task engine ops (ops.go / gitflow.go).
			EngineOp: s.engineOp,
			TaskOp:   s.taskOp,
			TaskUpdateGateOp: func(h *chat.Hub, c *chat.Client, actor, taskID string, p tasks.Patch) {
				s.gitFace().TaskUpdateGateOp(h, c, actor, taskID, p)
			},
			TaskConfirmGateOp: func(h *chat.Hub, c *chat.Client, actor, taskID string) {
				s.gitFace().TaskConfirmGateOp(h, c, actor, taskID)
			},
			// KB ops (ops.go / scope.go).
			KbOp:        s.kbOp,
			KbWriteGate: s.kbWriteGate,
			KbRestoreAs: s.kbRestoreAs,
			KbArchiveAs: s.kbArchiveAs,
			KbDeleteOp:  s.kbDeleteOp,
		},
	}
}
