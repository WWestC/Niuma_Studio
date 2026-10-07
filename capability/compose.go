// Compose: the single-source fabric assembly. Three consumers read
// this one function — birth injection, the web fabric preview, the
// audit digest — none of them re-implements it.
package capability

import (
	"errors"
	"fmt"
	"strings"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/i18n"
)

// MaxFabricBytes caps one seat's composed fabric: the fabric is
// injection body — unbounded stacking would eat the session's context
// budget, so Compose refuses over-limit assemblies instead of
// trimming them.
const MaxFabricBytes = 32 * 1024

// EffectiveFabric is Compose's output: everything a seat runs on,
// folded from the room base + profile inline layer + the assembly
// lists. Model nil = no seat pick (the process defaults stand); MCP
// empty = born without the mcpServers override (the member's default
// MCP fleet applies).
type EffectiveFabric struct {
	Text    string      `json:"text"`
	Manuals []string    `json:"manuals,omitempty"`
	Model   *ModelTier  `json:"model,omitempty"`
	MCP     []MCPServer `json:"mcp,omitempty"`
}

// AuthoringNote is the 自制技能 protocol briefing appended to the
// fabric when the studio's model-authoring switch is on — the only
// place a model learns the fenced-block vocabulary.
const AuthoringNote = "【自制技能协议】\n" +
	"你可以把工作中沉淀的套路固化为工作室技能：在回复中写一个信息串为 niuma-skill 的围栏代码块，" +
	"块内第一行「key: <slug>」（[a-z0-9_-]，1–32 字符，创建后不可改名）、第二行「name: <名称>」，" +
	"其余行为技能正文（给未来使用者的指令面料）。调度器会把块收录进技能库，重名 key 整体替换；" +
	"仅在确有跨任务复用价值时使用，不要为一次性操作造技能。"

// Compose folds one profile and its two ordered assembly lists into
// the effective fabric:
//
//   - Text: the room base — agents.HandoffPrompt(cfg), which already
//     carries the profile inline layer (Manual reference + Prompt) —
//     then each skill's body appended in assembly order as its own
//     section:
//
//     【技能｜<key>】
//     <body>
//
//     With no skills (and the authoring switch off) the Text is
//     byte-for-byte the v1 HandoffPrompt — that equivalence is the
//     regression baseline, locked by test.
//
//   - Manuals: profile Manual + every skill's manuals, first
//     occurrence order, deduped.
//
//   - MCP: the resolved server list as given — assembly order, no
//     folding (a server list has no slots to merge).
//
// modelAuthoring appends the AuthoringNote briefing so a member born
// or re-injected under an open switch knows the protocol; the switch
// also gates the dispatcher's intercept live, so flipping it takes
// effect for the intercept immediately and for the briefing at the
// next injection.
//
// Compose is pure — no IO, no store lookups, no per-skill
// re-validation (the store owns skill validity); the one limit it
// does enforce is the whole-fabric cap. Duplicate keys in the input
// are followed as given: bodies land twice — pruning the list is the
// resolver's business.
func Compose(cfg agents.Config, skills []Skill, mcps []MCPServer, modelAuthoring bool) (EffectiveFabric, error) {
	var b strings.Builder
	b.WriteString(agents.HandoffPrompt(cfg))

	manuals := make([]string, 0, 1+len(skills))
	seenManual := make(map[string]struct{}, 1+len(skills))
	if cfg.Manual != "" {
		manuals = append(manuals, cfg.Manual)
		seenManual[cfg.Manual] = struct{}{}
	}

	for _, sk := range skills {
		if sk.Body != "" {
			fmt.Fprintf(&b, "\n\n【技能｜%s】\n%s", sk.Key, sk.Body)
		}
		for _, m := range sk.Manuals {
			if m == "" {
				continue
			}
			if _, dup := seenManual[m]; dup {
				continue
			}
			seenManual[m] = struct{}{}
			manuals = append(manuals, m)
		}
	}

	if modelAuthoring {
		b.WriteString("\n\n")
		b.WriteString(AuthoringNote)
	}

	if b.Len() > MaxFabricBytes {
		return EffectiveFabric{}, errors.New(i18n.Sf("单岗有效面料超限：%d 字节（上限 %d）——面料是注入正文，超限拒绝装配并报因", b.Len(), MaxFabricBytes))
	}

	var outManuals []string
	if len(manuals) > 0 {
		outManuals = manuals
	}
	var outMCP []MCPServer
	if len(mcps) > 0 {
		outMCP = append([]MCPServer(nil), mcps...)
	}
	return EffectiveFabric{Text: b.String(), Manuals: outManuals, MCP: outMCP}, nil
}
