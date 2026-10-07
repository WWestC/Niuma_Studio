package requirements

// Store is the requirement ledger's consumer-facing contract. Every
// caller (the server's reqs/plan/purge faces, the dispatch clocks,
// boot's renumber migrations) drinks from this interface. The method
// set is deliberately the ledger's whole exported surface: each method
// already has a legitimate consumer today, and trimming to
// per-consumer interfaces would only multiply the seams a backend swap
// must keep honest. Engine (requirements.go) is the implementation —
// the ONLY one since the sqlite promotion deleted the JSON file rack
// (domain semantics live once, in the engine); storetest.TestReqStore
// is the contract suite that pins them.
//
// The SUBJECT parameter (first on every method) is the storage
// namespace the call acts in: "" is the single-user historical shape,
// a non-empty subject is the multi-user studio's owner account name —
// the namespace every shared ledger row falls under. The engine binds
// ONE subject at open and refuses any other (ErrSingleSubject /
// ErrSubjectMismatch); the per-call shape (not a bound-at-open view)
// is deliberate: the seam exists so a future per-account partitioning
// needs no second interface rewrite.
type Store interface {
	// Create appends a requirement in open on projectKey's shelf; the
	// store stamps ID (that project's own counter), Status, CreatedTS.
	Create(subject, projectKey, title, body, createdBy string) (Req, error)
	// MarkSplit flips open→split — the plan-acceptance write-back.
	MarkSplit(subject, projectKey, id string) (Req, error)
	// Park freezes an open requirement (r_31 stocked-but-frozen shelf).
	Park(subject, projectKey, id, note string, reviewAfter int64) (Req, error)
	// Unpark thaws parking→open (park reason preserved on the row).
	Unpark(subject, projectKey, id string) (Req, error)
	// MarkClosed closes (open|split→closed) and stamps ClosedTS.
	MarkClosed(subject, projectKey, id string) (Req, error)
	// Delete removes an open/parking entry; returns the removed snapshot.
	Delete(subject, projectKey, id string) (Req, error)
	// DropProject wipes one project's shelf outright (reset's half):
	// entries of every status and the r_NN counter all go.
	DropProject(subject, projectKey string) int
	// GetIn is the project-resolved read (ids unique per shelf only).
	GetIn(subject, projectKey, id string) (Req, bool)
	// FindAll returns every shelf's entry carrying id, oldest slot key
	// first — the cross-project host faces' read.
	FindAll(subject, id string) []Req
	// List returns every requirement across shelves, slot keys sorted.
	List(subject string) []Req
	// ListByProject returns one project's requirements, all states.
	ListByProject(subject, projectKey string) []Req
	// Rev is the subject's mutation counter (change detection without
	// diffs).
	Rev(subject string) uint64
	// RenumberPlan computes the compact renumber map with NO mutation.
	RenumberPlan(subject, projectKey string) map[string]string
	// RenumberApply rewrites ids per mapping and resumes the shelf's
	// counter past the new maximum.
	RenumberApply(subject, projectKey string, mapping map[string]string) error
}

// ShelfStore is the Engine's durable side (the tasks.Store posture):
// load every shelf of one subject at open, atomically replace one
// shelf per mutation, remove a shelf outright on project wipe. The
// whole shelf — counter plus entries, in insertion order — is ONE
// document, so a backend swap changes WHERE it lives, never how the
// domain reads or numbers it. The single-database implementation
// lives in sqlstore (req_shelves); a nil seam is the in-memory
// engine. The SUBJECT parameter is the namespace the call acts in;
// the engine threads its bound subject through.
type ShelfStore interface {
	// OpenShelves loads every persisted shelf of one subject. A parse
	// failure is an error (the caller's corrupt-reset path decides
	// what happens next); a missing backing is the fresh-install empty
	// state, not an error.
	OpenShelves(subject string) (shelves map[string]*Shelf, err error)
	// SaveShelf atomically replaces one project's shelf (counter and
	// entries together — a torn shelf must never be readable).
	SaveShelf(subject, project string, f *Shelf) error
	// DeleteShelf removes one project's shelf outright; a missing
	// shelf is success (idempotent wipe).
	DeleteShelf(subject, project string) error
}

// compile-time: the engine must satisfy the contract (an Engine
// method-signature drift is a Store break).
var _ Store = (*Engine)(nil)
