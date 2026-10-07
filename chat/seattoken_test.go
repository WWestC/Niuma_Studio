package chat

// seattoken_test.go — the seat-credential pipeline pins: distinct
// files for names that sanitize identically (小牛/小马 both collapse
// to underscores — the latent shared-credential bug), the save/load
// round trip, and JoinFree's twin-proof contract (a live same-name
// holder either proves supersedeable by token or refuses — never a
// "-2").

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSeatTokenPathNamesDoNotCollide(t *testing.T) {
	a, b := SeatTokenPath("小牛"), SeatTokenPath("小马")
	if a == "" || b == "" {
		t.Fatalf("paths unresolved: %q %q", a, b)
	}
	if a == b {
		t.Fatalf("distinct CJK names share one credential file: %q", a)
	}
	// the second CJK name must not collide with a third either
	if SeatTokenPath("小牛") == SeatTokenPath("小牛-2") {
		t.Fatalf("deduped twin name shares the member's credential file")
	}
	// pure-ASCII names keep the historical path (no digest suffix)
	got, want := SeatTokenPath("alice"), filepath.Join(seatTokenHomeMust(t), ".niuma_token_alice")
	if got != want {
		t.Fatalf("ascii path drifted: got %q want %q", got, want)
	}
}

func seatTokenHomeMust(t *testing.T) string {
	t.Helper()
	dir, err := seatTokenHome()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSeatTokenSaveLoadRoundTrip(t *testing.T) {
	restore := OverrideSeatTokenHome(t.TempDir())
	defer restore()
	SaveSeatToken("", "小牛", "tok-1")
	if got := LoadSeatToken("", "小牛"); got != "tok-1" {
		t.Fatalf("round trip: got %q want tok-1", got)
	}
	if got := LoadSeatToken("", "小马"); got != "" {
		t.Fatalf("another member's credential leaked: %q", got)
	}
	SaveSeatToken("", "小牛", "") // empty saves nothing
	if got := LoadSeatToken("", "小牛"); got != "tok-1" {
		t.Fatalf("empty save must not rekey: %q", got)
	}
}

// TestSeatTokenProjectKeying pins the per-project credential split（分项目
// 根治的座位凭证键）: one person flowing between rooms keeps one credential
// PER ROOM — a book save must never clobber the lobby file, the lobby CLI
// must not present book's token, and a pre-re-keying legacy file stays
// readable through the fallback while the project key takes precedence.
func TestSeatTokenProjectKeying(t *testing.T) {
	restore := OverrideSeatTokenHome(t.TempDir())
	defer restore()
	SaveSeatToken("book", "小苗", "tok-book")
	SaveSeatToken("default", "小苗", "tok-lobby")
	if got := LoadSeatToken("book", "小苗"); got != "tok-book" {
		t.Fatalf("book credential: got %q want tok-book", got)
	}
	if got := LoadSeatToken("default", "小苗"); got != "tok-lobby" {
		t.Fatalf("lobby credential: got %q want tok-lobby", got)
	}
	if SeatTokenPath("小苗") == "" {
		t.Fatal("legacy path unresolved")
	}
	// the legacy name-only file is the write target ONLY for project=="";
	// a room-scoped save must leave it untouched.
	SaveSeatToken("", "小苗", "tok-legacy")
	if got := LoadSeatToken("book", "小苗"); got != "tok-book" {
		t.Fatalf("a room save must not ride the legacy file: got %q", got)
	}
	// transition fallback: a member whose only credential predates the
	// split stays presentable from the scoped read.
	SaveSeatToken("", "小检", "tok-old")
	if got := LoadSeatToken("book", "小检"); got != "tok-old" {
		t.Fatalf("legacy fallback lost: got %q want tok-old", got)
	}
	// and the split files must not collide across rooms for CJK names
	if SeatTokenPathProject("book", "小苗") == SeatTokenPathProject("default", "小苗") {
		t.Fatal("two rooms share one credential file")
	}
}

// TestJoinFreeRefusesInsteadOfTwinning: a live seat under a different
// credential must refuse (ok=false, no seat created); the matching
// credential supersedes in place (name keeps, old transport closes
// with the supersede reason).
func TestJoinFreeRefusesInsteadOfTwinning(t *testing.T) {
	h := NewHub()
	first, firstM := h.Join("alice", "dev", false)
	tok := first.SeatToken()
	if tok == "" {
		t.Fatal("seat carries no credential")
	}

	// stranger (no credential): refused, nothing created
	if _, _, ok := h.JoinFree("alice", "dev", false, ""); ok {
		t.Fatal("no-token JoinFree against a live seat must refuse")
	}
	if _, held := h.SeatHolderToken("alice"); !held {
		t.Fatal("the live seat vanished on refusal")
	}
	for _, m := range h.Members() {
		if m.Name == "alice-2" {
			t.Fatal("refusal spawned a -2 twin")
		}
	}

	// the member's own credential: supersede — old transport closes
	lease, leaseM, _ := h.JoinFree("alice", "dev", false, tok)
	if lease == nil {
		t.Fatal("same-token JoinFree must supersede, not refuse")
	}
	if leaseM.Name != "alice" || leaseM.Name != firstM.Name {
		t.Fatalf("supersede renamed the seat: %q", leaseM.Name)
	}
	select {
	case <-first.Done():
		if first.CloseReason() != CloseReasonSuperseded {
			t.Fatalf("close reason: %q", first.CloseReason())
		}
	case <-time.After(time.Second):
		t.Fatal("old transport never closed on supersede")
	}

	// lease leaves → ghost keeps the credential → JoinFree re-takes it
	h.Leave(lease)
	back, backM, _ := h.JoinFree("alice", "dev", false, "")
	if back == nil || backM.Name != "alice" {
		t.Fatalf("ghost reclaim failed: %+v", backM)
	}
	if back.SeatToken() != tok {
		t.Fatalf("ghost reclaim rekeyed the credential: %q", back.SeatToken())
	}
}

// TestRestoreSeatsCarryGhostToken pins the 小狐-2 root fix: a
// snapshot's ghost carries its seat credential into the restored
// room. Without it every restart minted a fresh token per member —
// the re-keyed seat vs the CLI's still-old credential file opened a
// "-2" window — and a token-carrying stranger could reclaim the
// tokenless restored ghost (empty ghost tokens short-circuited the
// mismatch check for ANY caller).
func TestRestoreSeatsCarryGhostToken(t *testing.T) {
	h1 := NewHub()
	seat, m := h1.Join("小狐", "前端", false)
	if m.Name != "小狐" {
		t.Fatalf("join renamed: %q", m.Name)
	}
	tok := seat.SeatToken()
	h1.Leave(seat) // the ghost forms carrying the credential
	snap := h1.Seats()
	var ghostRow *SeatSnapshot
	for i := range snap {
		if snap[i].Name == "小狐" && snap[i].Ghost {
			ghostRow = &snap[i]
		}
	}
	if ghostRow == nil || ghostRow.Token != tok {
		t.Fatalf("ghost snapshot lost the credential: %+v", ghostRow)
	}

	// restart-stability: the dispatcher's adopt-shaped dial (no token)
	// reclaims the restored ghost and CONTINUES its credential — no
	// per-restart re-mint, the CLI's file token keeps matching.
	h2 := NewHub()
	h2.RestoreSeats(snap)
	again, againM := h2.Join("小狐", "前端", false)
	if againM.Name != "小狐" {
		t.Fatalf("restored ghost not reclaimable by name: %q", againM.Name)
	}
	if got := again.SeatToken(); got != tok {
		t.Fatalf("restart rekeyed the credential: got %q want %q", got, tok)
	}

	// the mismatch guard: a token-carrying stranger must NOT reclaim a
	// restored ghost — it twins instead, like any mismatched dial.
	h3 := NewHub()
	h3.RestoreSeats(snap)
	_, twinM := h3.JoinWithToken("小狐", "前端", false, "stranger-token")
	if twinM.Name != "小狐-2" {
		t.Fatalf("stranger reclaimed the restored ghost: %q", twinM.Name)
	}

	// the member's own credential still reclaims it cleanly
	h4 := NewHub()
	h4.RestoreSeats(snap)
	own, ownM := h4.JoinWithToken("小狐", "前端", false, tok)
	if ownM.Name != "小狐" || own.SeatToken() != tok {
		t.Fatalf("own credential no longer reclaims: %q %q", ownM.Name, own.SeatToken())
	}
}

// TestJoinReclaimsTwinGhostInsteadOfShadowing pins the roster-residue
// half of the 小狐-2 disease: a rejected one-shot retries with the
// same stale credential while its own previous twin ghost holds
// "小狐-2" — the retry reclaims that ghost instead of shadowing it
// (one entity per name), and a mismatched stranger skips ghost-held
// suffixes entirely.
func TestJoinReclaimsTwinGhostInsteadOfShadowing(t *testing.T) {
	h := NewHub()
	_, liveM := h.Join("小狐", "前端", false) // the dispatcher-held seat
	if liveM.Name != "小狐" {
		t.Fatalf("setup: %q", liveM.Name)
	}

	// first stale-credential dial: twins to 小狐-2, leaves a ghost there
	twin, twinM := h.JoinWithToken("小狐", "前端", false, "stale-1")
	if twinM.Name != "小狐-2" {
		t.Fatalf("setup twin: %q", twinM.Name)
	}
	staleTok := twin.SeatToken() // the presented credential rides along
	h.Leave(twin)
	if names := h.GraceNames(); len(names) != 1 || names[0] != "小狐-2" {
		t.Fatalf("setup ghost: %v", names)
	}

	// the retry (same stale credential): reclaims its own 小狐-2 ghost —
	// no second entity, the roster shows exactly one 小狐-2
	retry, retryM := h.JoinWithToken("小狐", "前端", false, staleTok)
	if retryM.Name != "小狐-2" {
		t.Fatalf("retry did not land on its ghost: %q", retryM.Name)
	}
	if got := retry.SeatToken(); got != staleTok {
		t.Fatalf("retry rekeyed the credential: %q", got)
	}
	if names := h.GraceNames(); len(names) != 0 {
		t.Fatalf("a ghost shadows the live twin: %v", names)
	}

	// a DIFFERENT credential skips ghost-held/live suffixes alike:
	// 小狐-3, never a second 小狐-2
	_, otherM := h.JoinWithToken("小狐", "前端", false, "stranger-token")
	if otherM.Name != "小狐-3" {
		t.Fatalf("stranger squatted an occupied suffix: %q", otherM.Name)
	}
}
