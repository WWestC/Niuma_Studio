package agents

// RankForRole tests (v2.2 flagship establishment): the two mandatory
// posts stamp their governance rank at birth — 编排者（排期编排）and
// HR（人事）both Lv.8 — and every other role leaves the rank registry
// untouched.

import "testing"

func TestRankForRole(t *testing.T) {
	for _, c := range []struct {
		role string
		want int
		ok   bool
	}{
		{OrchestratorRole, OrchestratorRank, true},
		{HRRole, HRRank, true},
		{"测试工程师", 0, false},
		{"", 0, false},
	} {
		got, ok := RankForRole(c.role)
		if got != c.want || ok != c.ok {
			t.Fatalf("RankForRole(%q) = %d,%v；want %d,%v", c.role, got, ok, c.want, c.ok)
		}
	}
	if HRRank != 8 {
		t.Fatalf("HR 应 Lv.8，得 %d", HRRank)
	}
}
