package plan

// Store is the plan domain's consumer-facing contract: the server's
// plan verbs and accept path, the patrol prompt, boot's renumber
// migrations. Engine (plan.go) is the implementation — the ONLY one
// since the sqlite promotion deleted the JSON file rack (domain
// semantics live once, in the engine); storetest.TestPlanStore is the
// contract suite that pins them.
// The SUBJECT parameter (first on every method) is the storage
// namespace the call acts in ("" = the single-user historical shape,
// non-empty = the multi-user studio owner account); the engine binds
// one subject at open and refuses any other. See requirements.Store
// for the full contract note.
type Store interface {
	// Submit installs p as projectKey's pending plan, stamping ID /
	// Submitter / SubmittedTS; returns the stored plan and the one it
	// SUPERSEDED (nil when the slot was empty).
	Submit(subject, projectKey, submitter string, p Plan, now int64) (stored, superseded *Plan)
	// Pending returns a copy of projectKey's awaiting plan (nil = none).
	Pending(subject, projectKey string) *Plan
	// PendingIDs lists the subject's projects holding an awaiting plan,
	// sorted.
	PendingIDs(subject string) []string
	// Take pops the pending plan when its id matches planID (the
	// accept/reject gate: one shot, id-checked).
	Take(subject, projectKey, planID string) (*Plan, error)
	// Drop deletes projectKey's slot outright (reset's half); the p_NN
	// counter goes with it. Reports whether a plan was awaiting.
	Drop(subject, projectKey string) bool
	// RemapReq rewrites the pending plan's Req and prose r_NN tokens per
	// mapping (the renumber migrations' companion face).
	RemapReq(subject, projectKey string, mapping map[string]string) bool
	// RemapTasks rewrites the pending plan's prose t_NN tokens per
	// mapping (plan tasks carry no structural id; prose is the surface).
	RemapTasks(subject, projectKey string, mapping map[string]string) bool
}

// SlotStore is the Engine's durable side (the tasks.Store posture):
// the whole slot — counter plus the single pending document — is ONE
// document, so a backend swap changes WHERE it lives, never how the
// domain reads or numbers it. The single-database implementation
// lives in sqlstore (plan_slots); a nil seam is the in-memory engine.
// The SUBJECT parameter is the namespace the call acts in; the
// engine threads its bound subject through.
type SlotStore interface {
	// OpenSlots loads every persisted slot of one subject. A parse
	// failure is an error (the caller's corrupt-reset path decides
	// what happens next); a missing backing is the fresh-install
	// empty state, not an error.
	OpenSlots(subject string) (slots map[string]*Slot, err error)
	// SaveSlot atomically replaces one project's slot (counter and
	// document together — a torn slot must never be readable).
	SaveSlot(subject, project string, f *Slot) error
	// DeleteSlot removes one project's slot outright; a missing slot
	// is success (idempotent wipe).
	DeleteSlot(subject, project string) error
}

// compile-time: the engine must satisfy the contract it predates (an
// Engine method-signature drift is a Store break).
var _ Store = (*Engine)(nil)
