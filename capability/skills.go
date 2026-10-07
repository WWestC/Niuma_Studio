// Skill: the studio's instruction asset — a named, cross-project
// body of know-how a member carries. One skill is slot ①+② of the
// retired capability pack folded into one thing: the instruction
// fabric (Body) plus ordered kb manual references. Skills never touch
// the model or the tool surface; those are the staffing row's own
// knobs.
package capability

import (
	"errors"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// Skill limits. An empty Body is legal (a pure manual-reference
// skill); MaxSkills caps the global library; the model layer does not
// police taste.
const (
	MaxSkills       = 64        // global library cap
	MaxSkillBody    = 16 * 1024 // bytes per skill's instruction body
	MaxSkillManuals = 4         // manual reference count
)

const (
	maxNameRunes = 48
	maxDescRunes = 2000
	maxByRunes   = 24 // created_by: the member-name cap, house style
)

// Skill is one skill — a global, cross-project asset. Skills never
// enter projects or profiles; the ASSEMBLY does (profile defaults ⊕
// staffing override).
type Skill struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Desc      string   `json:"desc,omitempty"`
	Body      string   `json:"body,omitempty"`    // instruction fabric
	Manuals   []string `json:"manuals,omitempty"` // ordered kb doc keys
	CreatedBy string   `json:"created_by,omitempty"`
	CreatedTS int64    `json:"created_ts,omitempty"`
}

// Validate checks the entity's own fields: key slug, prose bounds,
// body byte cap (rejected, never silently truncated — a half
// instruction is worse than a refused save), manual reference shape
// and count. Whether the skill exists or the library is full is the
// store's business.
func (s Skill) Validate() error {
	if !ValidKey(s.Key) {
		return errors.New(i18n.Sf("非法技能 key %q（[a-z0-9_-]{1,32}，创建后不可改名）", s.Key))
	}
	if n := len([]rune(s.Name)); n == 0 {
		return errors.New(i18n.S("技能名称不能为空"))
	} else if n > maxNameRunes {
		return errors.New(i18n.Sf("技能 %s 名称超长：%d rune（上限 %d）", s.Key, n, maxNameRunes))
	}
	if n := len([]rune(s.Desc)); n > maxDescRunes {
		return errors.New(i18n.Sf("技能 %s 描述超长：%d rune（上限 %d）", s.Key, n, maxDescRunes))
	}
	if n := len(s.Body); n > MaxSkillBody {
		return errors.New(i18n.Sf("技能 %s 的正文超限：%d 字节（上限 %d）——拒绝保存，静默截断会让成员拿到半截指令", s.Key, n, MaxSkillBody))
	}
	if len(s.Manuals) > MaxSkillManuals {
		return errors.New(i18n.Sf("技能 %s 的手册引用超数：%d 条（上限 %d）", s.Key, len(s.Manuals), MaxSkillManuals))
	}
	for _, m := range s.Manuals {
		if !validManualKey(m) {
			return errors.New(i18n.Sf("技能 %s 的手册引用非法: %q（kb 文档 key：[a-z0-9_-] 段，两级以内，≤64 字符）", s.Key, m))
		}
	}
	return nil
}

// sanitize clamps the prose fields (house style: display text clamps,
// identifiers validate-or-reject) and trims body/manual items without
// ever truncating them — oversize bodies must fail loudly in
// Validate, not lose their tail here. The key is never rewritten.
func (s Skill) sanitize() Skill {
	s.Name = clampRunes(strings.TrimSpace(s.Name), maxNameRunes)
	s.Desc = clampRunes(strings.TrimSpace(s.Desc), maxDescRunes)
	s.Body = strings.TrimSpace(s.Body)
	if s.Manuals != nil {
		ms := make([]string, 0, len(s.Manuals))
		for _, m := range s.Manuals {
			if m = strings.TrimSpace(m); m != "" {
				ms = append(ms, m)
			}
		}
		if len(ms) == 0 {
			ms = nil
		}
		s.Manuals = ms
	}
	s.CreatedBy = sanitizeBy(s.CreatedBy)
	return s
}

// sanitizeBy clamps a provenance name (created_by): single line,
// trimmed, ≤24 runes — the member-name cap, house style.
func sanitizeBy(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
	}
	s := strings.TrimSpace(string(out))
	if r := []rune(s); len(r) > maxByRunes {
		s = string(r[:maxByRunes])
	}
	return s
}
