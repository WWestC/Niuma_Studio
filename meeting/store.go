package meeting

// Store is the meeting domain's consumer-facing contract: the
// dispatcher's convene/chair legs, the review clocks, the HTTP
// meeting face, boot's renumber migrations. Engine (meeting.go) is
// the implementation — the ONLY one since the sqlite promotion
// deleted the JSON file rack; storetest.TestMeetingStore is the
// contract suite that pins the persisted-history semantics.
//
// The live meeting is memory-only BY DESIGN (a restart frees the
// room) — only the finished history rides the persistence seam. The
// engine binds ONE storage subject at open (multi-user = the studio
// owner account's namespace); the seam calls carry it, the face does
// not (every face call serves the bound studio).
type Store interface {
	// Begin convenes the room's one live meeting (occupancy gate: a
	// second Begin while one is live refuses). The store stamps ID
	// (that project's own m_NN counter), Status and StartTS.
	Begin(projectKey, reqID, reqTitle, chair string, participants []string) (Meeting, error)
	// Active returns the project's live meeting (a copy), if any.
	Active(projectKey string) (Meeting, bool)
	// SetPhase advances the live meeting's agenda.
	SetPhase(projectKey, phase string)
	// NoteOpening records the chair's presentation (first non-empty
	// wins).
	NoteOpening(projectKey, text string)
	// NoteConclusion records the chair's 方案 from the summary phase
	// (last write wins).
	NoteConclusion(projectKey, text string)
	// Line appends one discussion turn to the live transcript (capped).
	Line(projectKey, by, text string)
	// End releases the room: the live meeting moves into the persisted
	// history (capped per project, oldest dropped).
	End(projectKey string) (Meeting, bool)
	// DropProject wipes one project's shelf outright (reset's half).
	DropProject(projectKey string) int
	// History returns the project's finished meetings, newest first,
	// at most n (0 = all).
	History(projectKey string, n int) []Meeting
	// ReviewedAt returns each requirement's last review time (the
	// EndTS of the project's most recent finished meeting about it).
	ReviewedAt(projectKey string) map[string]int64
	// RenumberPlan computes the compact renumber map over the finished
	// history with NO mutation.
	RenumberPlan(projectKey string) map[string]string
	// RenumberApply rewrites ids on the history per mapping and resumes
	// the shelf's counter past the new maximum.
	RenumberApply(projectKey string, mapping map[string]string) error
	// RemapReq rewrites the shelf's requirement foreign keys per
	// mapping (the renumber migrations' companion face).
	RemapReq(projectKey string, mapping map[string]string) int
}

// ShelfStore is the Engine's durable side (the tasks.Store posture):
// the whole shelf — counter plus the capped finished history — is ONE
// document. The single-database implementation lives in sqlstore
// (meeting_shelves); a nil seam is the in-memory engine. The SUBJECT
// parameter is the namespace the call acts in; the engine threads its
// bound subject through.
type ShelfStore interface {
	// OpenShelves loads every persisted shelf of one subject. A parse
	// failure is an error (the caller's corrupt-reset path decides
	// what happens next); a missing backing is the fresh-install
	// empty state, not an error.
	OpenShelves(subject string) (shelves map[string]*Shelf, err error)
	// SaveShelf atomically replaces one project's shelf (counter and
	// history together — a torn shelf must never be readable).
	SaveShelf(subject, project string, f *Shelf) error
	// DeleteShelf removes one project's shelf outright; a missing
	// shelf is success (idempotent wipe).
	DeleteShelf(subject, project string) error
}

// compile-time: the engine must satisfy the contract (an Engine
// method-signature drift is a Store break).
var _ Store = (*Engine)(nil)
