// The conversation-side skill surfaces: ① slash attachment — a human
// line's /key tokens that resolve in the skill library prepend the
// skill bodies onto the injection (the chat text itself keeps the
// tokens, readable and searchable); ② the 自制技能 protocol line — a
// member's reply carrying a ```niuma-skill fenced block lands in the
// library when the studio's model-authoring switch is on (the same
// intercept-chain discipline as the vote/ack protocol lines).
package dispatch

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/i18n"
)

// slashTokenRe is one /key attachment token: a whitespace-delimited
// word that is exactly "/" plus a legal asset slug. Deliberately
// strict — no trailing punctuation tolerance — the composer's menu
// inserts "/key " with a trailing space; hand-typed tokens that miss
// (e.g. "/review，") stay plain text, which is the safe failure.
var slashTokenRe = regexp.MustCompile(`^/([a-z0-9_-]{1,32})$`)

// skillBlockRe is the 自制技能 fenced block: info string niuma-skill,
// inner text = "key: <slug>" / "name: <名称>" header lines + body.
var skillBlockRe = regexp.MustCompile("(?s)```niuma-skill[ \t]*\r?\n(.*?)\r?\n?```")

// attachSlashSkills prepends the resolved skills' bodies onto the
// injection line, in first-occurrence order, deduped. No library, no
// tokens, or all misses → the line unchanged. The bodies ride as
// 【技能｜key】 sections ahead of the message — context first, then
// the words.
func (d *Dispatcher) attachSlashSkills(srcText, line string) string {
	var b strings.Builder
	seen := make(map[string]bool)
	for _, tok := range strings.Fields(srcText) {
		msm := slashTokenRe.FindStringSubmatch(tok)
		if msm == nil || seen[msm[1]] {
			continue
		}
		sk, ok := d.cfg.Library.GetSkill(msm[1])
		if !ok || sk.Body == "" {
			continue
		}
		seen[msm[1]] = true
		fmt.Fprintf(&b, "【技能｜%s】\n%s\n\n", sk.Key, sk.Body)
	}
	if b.Len() == 0 {
		return line
	}
	return b.String() + line
}

// skillProposal is the 自制技能 protocol line (the onEvent intercept
// chain's member-facing leg). Returns the reply to mirror (blocks
// that LANDED are stripped; "" mirrors nothing) and whether anything
// observable happened (a system line — the caller's spoke flag).
//
//   - no proposal block in the reply → (content, false): mirror as-is.
//   - blocks present, switch off → full content kept + one ignore
//     note: honest refusal, not silent swallowing.
//   - blocks present, switch on → each well-formed block upserts into
//     the library (CreatedBy = the member, provenance is the trail);
//     saved blocks strip from the mirror, malformed or refused ones
//     stay visible with their reason on a system line.
func (d *Dispatcher) skillProposal(m *member, content string) (string, bool) {
	if d.cfg.Library == nil || !strings.Contains(content, "```niuma-skill") {
		return content, false
	}
	if !d.cfg.Library.ModelAuthoring() {
		d.hub.SystemRecorded(i18n.Sf("%s 的回复里有自制技能块——已忽略（「允许成员自制技能」未开启，设置卡可开）", m.name))
		return content, true
	}
	out := content
	acted := false
	for _, blk := range skillBlockRe.FindAllStringSubmatch(content, -1) {
		sk, ok := parseSkillBlock(blk[1])
		if !ok {
			continue // 形状不对：不是提案，原文保留
		}
		sk.CreatedBy = m.name
		saved, err := d.cfg.Library.UpsertSkill(sk)
		if err != nil {
			d.hub.SystemRecorded(i18n.Sf("%s 的自制技能 %s 未收录：%v", m.name, sk.Key, err))
			acted = true
			continue
		}
		out = strings.Replace(out, blk[0], "", 1)
		d.hub.SystemRecorded(i18n.Sf("%s 自制了技能 %s（%s）——已入库，可装配给任何成员", m.name, saved.Key, saved.Name))
		acted = true
	}
	if strings.TrimSpace(out) == "" {
		out = ""
	}
	return out, acted
}

// parseSkillBlock parses one block's inner text: the first non-empty
// line must be "key: <slug>", the second "name: <名称>", everything
// after is the body. Anything else is not a proposal.
func parseSkillBlock(inner string) (capability.Skill, bool) {
	var key, name string
	var body []string
	header := 0
	for _, ln := range strings.Split(inner, "\n") {
		t := strings.TrimSpace(ln)
		switch header {
		case 0:
			k, ok := cutField(t, "key")
			if !ok {
				return capability.Skill{}, false
			}
			key = k
			header = 1
		case 1:
			n, ok := cutField(t, "name")
			if !ok {
				return capability.Skill{}, false
			}
			name = n
			header = 2
		default:
			body = append(body, ln)
		}
	}
	if header < 2 || key == "" || name == "" {
		return capability.Skill{}, false
	}
	return capability.Skill{Key: key, Name: name, Body: strings.TrimSpace(strings.Join(body, "\n"))}, true
}

// cutField reads "field: value" (spaces around the colon tolerated).
func cutField(line, field string) (string, bool) {
	if !strings.HasPrefix(line, field+":") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, field+":")), true
}
