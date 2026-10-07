package kb

// vocab_test.go — the private mirrors' pin: every constant kb dares
// to spell itself (vocab.go) must equal its owning package's public
// word. The mirrors exist so kb can stay import-clean (domain
// packages speak wire mirrors and consumer interfaces only); this
// test is what keeps "private copy" from rotting into "second truth"
// — the same discipline as tasks.LobbyKey mirroring wire.LobbyKey.

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/wire"
)

func TestVocabMirrorsOwners(t *testing.T) {
	if statusDone != tasks.StatusDone {
		t.Fatalf("kb.statusDone=%q 与 tasks.StatusDone=%q 漂移", statusDone, tasks.StatusDone)
	}
	if rankHost != tasks.RankHost {
		t.Fatalf("kb.rankHost=%d 与 tasks.RankHost=%d 漂移", rankHost, tasks.RankHost)
	}
	if orchestratorRole != agents.OrchestratorRole || orchestratorRank != agents.OrchestratorRank {
		t.Fatalf("kb 编排者词汇（%q/%d）与 agents（%q/%d）漂移",
			orchestratorRole, orchestratorRank, agents.OrchestratorRole, agents.OrchestratorRank)
	}
	if hrRole != agents.HRRole || hrRank != agents.HRRank {
		t.Fatalf("kb HR 词汇（%q/%d）与 agents（%q/%d）漂移",
			hrRole, hrRank, agents.HRRole, agents.HRRank)
	}
	for _, key := range []string{"", "default", "proj-a"} {
		if got, want := shelfKey(key), tasks.SlotKey(key); got != want {
			t.Fatalf("kb.shelfKey(%q)=%q 与 tasks.SlotKey=%q 漂移", key, got, want)
		}
	}
	if wire.LobbyKey != "default" {
		t.Fatalf("wire.LobbyKey=%q：kb 的 shelfKey 镜像依赖它保持 default", wire.LobbyKey)
	}
}
