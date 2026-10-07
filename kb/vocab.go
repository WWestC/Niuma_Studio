package kb

import "github.com/WWestC/Niuma_Studio/wire"

// vocab.go — kb 的跨包词汇私有镜像：黑板是只读聚合面，它要引用 tasks
// 的状态词/等级词、agents 的旗舰岗位词，但领域包之间只经 wire 镜像与
// 消费者接口说话（chat/arch 与 domain 护栏钉的方向）。镜像常量与
// tasks.LobbyKey 镜像 wire.LobbyKey 同一纪律：私有一份、注释指认主权
// 人、vocab_test.go 钉相等——漂移在测试当场红，不在运行时静默。

// statusDone mirrors tasks.StatusDone: the ledger's delivered state —
// the only task status the delivery history groups by.
const statusDone = "done"

// rankHost mirrors tasks.RankHost: the local human's rank (99), the
// People fallback's stamp when no ledger is wired (tests, boot edges).
const rankHost = 99

// The flagship posts' role markers and governance ranks mirror
// agents' registry (agents/orchestrator.go, agents/flagship.go): the
// values are the boot contract the establishment seed table, the
// birth stamp and FlagshipRankOf all share — kb keeps its own copy
// only to stay import-clean; vocab_test.go pins the equality so the
// two spellings can never drift apart.
const (
	orchestratorRole = "排期编排"
	orchestratorRank = 8
	hrRole           = "人事"
	hrRank           = 8
)

// shelfKey mirrors tasks.SlotKey's read-side normalization: the v1
// empty shape and "default" are the lobby's ONE shelf, so the two
// spellings never split one lobby history into two lines.
func shelfKey(projectKey string) string {
	if projectKey == "" {
		return wire.LobbyKey
	}
	return projectKey
}
