// The HR post's role marker and the birth-time governance rank
// registry (v2.2 flagship establishment): the orchestrator (排期编排)
// and HR (人事) are the studio's two mandatory Lv.8 posts — 编排者
// 小牛 and HR 小马 (kb.FlagshipPosts owns the person names). RankForRole
// is the single lookup both the dispatcher's birth stamp and the boot
// recruiter's rank refresh read, so a role-marked member can never
// carry a stale level across rebirth.
package agents

// HRRole is the role marker of the HR post (人事): the boot
// establishment's second mandatory post (kb.FlagshipPosts governs the
// seat; the name above it stays swappable, same contract as the
// orchestrator's).
const HRRole = "人事"

// HRRank is HR's governance rank (Lv.8, flagship establishment):
// peer to the orchestrator — high enough to direct onboarding and
// staffing across the studio, low enough that the host (99) always
// overrules.
const HRRank = 8

// RankForRole returns the governance rank a birth stamps onto a
// role-marked member (orchestrator 排期编排 and HR 人事 today). ok=false
// leaves the rank registry untouched — every other role keeps whatever
// the room granted it.
func RankForRole(role string) (rank int, ok bool) {
	switch role {
	case OrchestratorRole:
		return OrchestratorRank, true
	case HRRole:
		return HRRank, true
	}
	return 0, false
}
