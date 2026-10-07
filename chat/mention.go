package chat

import (
	"regexp"
	"strings"
)

// The @-mention parser (pure): at-name extraction, the @所有人
// roll-call vocabulary and the role-stem rules the hub's group wake
// resolves against. No Hub state — everything here is a function of
// its inputs.

// atMentionStart reports whether the '@' at r[i] may open a mention.
// A mention may start anywhere except inside an ASCII word/handle, so
// CJK text like "帮我@小策" works without a leading space. Right after
// ASCII word text the '@' STILL opens a mention when the name itself
// heads with CJK — "bug@小策" names 小策 — because a CJK-headed name
// can't be the tail of an email; an ASCII-headed one ("a@b.com", a
// member literally named "b" notwithstanding) stays inert.
func atMentionStart(r []rune, i int) bool {
	if i == 0 {
		return true
	}
	if r[i-1] == '@' {
		return false // a doubled '@' never opens one
	}
	if !asciiWordish(r[i-1]) {
		return true
	}
	return i+1 < len(r) && !asciiWordish(r[i+1])
}

// asciiWordish reports the characters an email/handle is built from:
// ASCII letters, digits and ._-+ (the '@' itself excluded — callers
// test around it).
func asciiWordish(p rune) bool {
	switch {
	case p >= 'a' && p <= 'z', p >= 'A' && p <= 'Z', p >= '0' && p <= '9':
		return true
	case p == '.' || p == '_' || p == '-' || p == '+':
		return true
	}
	return false
}

type allKind int

// allMentionKind classifies the strongest all-address token in text:
// allRollCall for "@所有人" (summons with a reply window), allBroadcast
// for "@全员" (wake only), allNone without one. Both tokens share the
// same boundary rules — line start, whitespace, word text or CJK may
// precede; enumeration punctuation and, per ARB-4's rider, quotes on
// either side of the token (『@所有人』, `@全员`) mark a mention of the
// word rather than a use of it, so prose about the tokens stays inert.
// The roll-call token wins when both appear.
func allMentionKind(text string) allKind {
	roll := []rune("@" + MentionAllToken)
	cast := []rune("@全员")
	r := []rune(text)
	best := allNone
	for i := 0; i < len(r); i++ {
		if r[i] != '@' || !atMentionStart(r, i) {
			continue
		}
		if i > 0 && (listPunctBefore(r[i-1]) || quoteRune(r[i-1])) {
			continue
		}
		for _, c := range []struct {
			tok  []rune
			kind allKind
		}{{roll, allRollCall}, {cast, allBroadcast}} {
			end := i + len(c.tok)
			if end > len(r) || string(r[i:end]) != string(c.tok) {
				continue
			}
			if end < len(r) && quoteRune(r[end]) {
				continue // closing quote: the word, not the summons
			}
			if c.kind > best {
				best = c.kind
			}
		}
	}
	return best
}

// quoteRune reports an opening/closing quote: a token hugging one
// reads as being talked about, not used.
func quoteRune(r rune) bool {
	switch r {
	case '『', '』', '「', '」', '“', '”', '‘', '’', '"', '\'', '`', '《', '》':
		return true
	}
	return false
}

// crowdAddressWords are the plain-Chinese crowd words that read as an
// intended all-address when the @ summons syntax is missing — the
// 「全部人都去查看」family (房主实录: a plain-word crowd address wakes
// nobody, and the silence reads as the room ignoring the owner).
var crowdAddressWords = []string{"所有人", "全员", "大家", "各位", "全部人", "每个人"}

// crowdAddressMiss reports whether text carries one of the plain crowd
// words — prose that reads like an all-address was intended. Pure word
// presence: quoting rules are deliberately NOT re-applied here (「@全
// 员」discussed in prose hits too), because the hub gates the nudge on
// the say's resolved mentions being empty — a live @所有人 that woke the
// room never self-nags, and the leftover hits (an empty room, a quoted
// token) still told the truth: nobody woke.
func crowdAddressMiss(text string) bool {
	for _, w := range crowdAddressWords {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// RoleStem returns a role's taxonomy: the first segment before any
// separator — "前端开发·界面交互（三号位）" stems to "前端开发".
// The hub's group-wake parser and the Web composer's "@<岗位>组"
// completion candidates share this one extractor (v0.9 IN4 —
// 提取逻辑只写一份).
func RoleStem(role string) string {
	for _, sep := range []rune("·：:（(，,／/　 ") {
		if idx := strings.IndexRune(role, sep); idx > 0 {
			role = role[:idx]
		}
	}
	return strings.TrimSpace(role)
}

// listPunctBefore reports punctuation that marks the @ as an item in
// an enumeration rather than a live address.
func listPunctBefore(p rune) bool {
	switch p {
	case '/', '\\', ',', '，', '、', ';', '；',
		'(', ')', '（', '）', '[', ']', '【', '】', '「', '」', '『', '』', '<', '>', '《', '》':
		return true
	}
	return false
}

// quotePrefixRe mirrors the Web composer's quote-reply prefix
// 「引用 #N @X：snip」 (chat/stream.js QUOTE_RE): optional seq, optional
// '@', a from-name free of 「」：, then the snip up to the closing 」.
// Anchored at the text head — that is where the composer (and the
// dispatcher's stale-reply header) put it.
var quotePrefixRe = regexp.MustCompile(`\A「引用 (?:#\d+ )?@?[^「」：]{1,60}：[^」]*」`)

// maskQuoteSnip blanks the snip of a leading 「引用 …」 prefix: the
// from-token's @X keeps summoning (引用即点名, quote_test), but @-names
// and summons tokens QUOTED inside the snip are the quoted person's
// words riding along, not the speaker's address — quoting a line that
// contains "@李四" must not wake 李四, and an "@所有人" echoed inside a
// snip must not roll-call the room. Everything outside the snip scans
// unchanged.
func maskQuoteSnip(text string) string {
	m := quotePrefixRe.FindStringIndex(text)
	if m == nil {
		return text
	}
	head := text[m[0]:m[1]]
	// the from group can't contain '：', so the first one in the prefix
	// IS the separator
	colon := strings.IndexRune(head, '：')
	if colon < 0 {
		return text
	}
	return head[:colon+len("：")] + "」" + text[m[1]:]
}

// appendMention adds name to mentions unless already present.
func appendMention(mentions []string, name string) []string {
	for _, m := range mentions {
		if m == name {
			return mentions
		}
	}
	return append(mentions, name)
}

// ExtractMentions scans text for "@name" tokens matched against names
// (given as rune slices; pass nil to match nothing). A "@" opens a
// mention anywhere except inside an ASCII word/handle (atMentionStart):
// CJK and punctuation before it all trigger, and after ASCII word text
// a CJK-headed name still does ("bug@小策"), while "a@b.com" stays
// plain text. The longest matching name wins, so with
// both "Bot" and "Bot-2" online "@Bot-2" hits Bot-2. The returned
// names use the canonical casing from names, deduplicated in order of
// appearance.
func ExtractMentions(text string, names [][]rune) []string {
	sorted := append([][]rune(nil), names...)
	// longest first so "Bot-2" wins over "Bot" at the same position
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j]) > len(sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	var out []string
	seen := map[string]bool{}
	r := []rune(text)
	for i := 0; i < len(r); i++ {
		if r[i] != '@' {
			continue
		}
		if !atMentionStart(r, i) {
			continue
		}
		for _, name := range sorted {
			if len(name) == 0 || i+1+len(name) > len(r) {
				continue
			}
			if strings.EqualFold(string(r[i+1:i+1+len(name)]), string(name)) {
				if !seen[string(name)] {
					seen[string(name)] = true
					out = append(out, string(name))
				}
				i += len(name) // consume the name; loop's i++ moves past it
				break
			}
		}
	}
	return out
}
