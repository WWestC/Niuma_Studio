package notice

// Store is the notice domain's consumer-facing contract: the chat
// face's notice_save verb, the GET /p/{key}/notice endpoint, the
// dispatcher's cold-start injection. Engine (notice.go) is the
// implementation — the ONLY one since the sqlite promotion deleted
// the JSON file rack. The failure contract is the A-族 REVERSE and
// is part of this interface: a write failure refuses (denied
// publish), never a silent skip — see the package note.
type Store interface {
	// Get reads key's current notice; ok is false when absent.
	Get(key string) (Notice, bool)
	// SetExpect writes key's notice under the expect_rev contract (see
	// the engine); a failed write returns the error and DENIES the
	// publish.
	SetExpect(key, content, by string, expectRev int) (Notice, error)
	// Clear takes key's notice down (rev checked; clearing an absent
	// notice is a no-op success).
	Clear(key, by string, expectRev int) error
}

// RowStore is the Engine's durable side: one row per (subject, room)
// holding that room's current notice document. The single-database
// implementation lives in sqlstore (notice_rows); a nil seam is the
// in-memory engine. The SUBJECT parameter is the namespace the call
// acts in; the engine threads its bound subject through.
type RowStore interface {
	// Load reads one room's notice document (ok false when absent).
	Load(subject, room string) (n Notice, ok bool, err error)
	// Save atomically replaces one room's notice document.
	Save(subject, room string, n Notice) error
	// Remove deletes one room's notice row; a missing row is success.
	Remove(subject, room string) error
}

// compile-time: the engine must satisfy the contract (an Engine
// method-signature drift is a Store break).
var _ Store = (*Engine)(nil)
