package zcode

// modelRules — the CLI's bundled zcode-builtin.json ships a global
// modelRules table beside the provider rules: one row per model family
// ({modelMatch regex, config.optionSpecs.reasoningLevel.values}). That
// table is the desktop picker's per-model thinking-intensity list —
// GLM-5.3 offers low/high/max, the gpt-5.6 family none..max, and an id
// no specific rule names falls to the shipped-first catch-all ".*"
// (disabled/enabled). Niuma parses the same file so the birth/改档 UIs
// offer exactly the levels ZCode itself would, from the same truth.

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"sync"
)

// modelRule is one modelRules row reduced to the level lookup's needs.
// Rows without a reasoningLevel values list (pure contextWindow/
// inputFormat overrides) never change the level set and drop here.
type modelRule struct {
	Match  string
	Levels []string
}

// compiledRule is a modelRule with its match pre-compiled.
type compiledRule struct {
	re     *regexp.Regexp
	levels []string
}

// parseModelRules is the pure half of the builtin read (BOM-tolerant,
// garbage = no rules).
func parseModelRules(b []byte) []modelRule {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var cfg struct {
		Config struct {
			ModelConfigRules struct {
				ModelRules []struct {
					ModelMatch string `json:"modelMatch"`
					Config     struct {
						OptionSpecs struct {
							ReasoningLevel struct {
								Values []string `json:"values"`
							} `json:"reasoningLevel"`
						} `json:"optionSpecs"`
					} `json:"config"`
				} `json:"modelRules"`
			} `json:"modelConfigRules"`
		} `json:"config"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	rules := cfg.Config.ModelConfigRules.ModelRules
	out := make([]modelRule, 0, len(rules))
	for _, r := range rules {
		if r.ModelMatch == "" || len(r.Config.OptionSpecs.ReasoningLevel.Values) == 0 {
			continue
		}
		out = append(out, modelRule{Match: r.ModelMatch, Levels: r.Config.OptionSpecs.ReasoningLevel.Values})
	}
	return out
}

// compileModelRules prepares parsed rules for matching. Case-
// insensitively (the builtin lists "GLM-5.3" while its rules write
// "glm-5\.3" — the CLI's own resolution honors both), and a pattern Go
// can't compile skips its row rather than the whole table.
func compileModelRules(rules []modelRule) []compiledRule {
	out := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile("(?i)" + r.Match)
		if err != nil {
			continue
		}
		out = append(out, compiledRule{re: re, levels: r.Levels})
	}
	return out
}

// resolveLevels walks the table in file order — the CLI's cascade: a
// later matching row's values override an earlier one's (the catch-all
// ".*" ships first, specific families after). Nil when nothing matches.
// The returned slice aliases the table; callers only read.
func resolveLevels(rules []compiledRule, modelID string) []string {
	if modelID == "" {
		return nil
	}
	var levels []string
	for _, r := range rules {
		if r.re.MatchString(modelID) {
			levels = r.levels
		}
	}
	return levels
}

var (
	levelsOnce  sync.Once
	levelsTable []compiledRule
)

// ReasoningLevels reports the thinking-intensity levels the CLI's own
// picker offers a model id — the bundled modelRules table, cached (the
// builtin file is static for the process's lifetime). Nil = no rule
// spoke (file missing, nothing matched): the caller offers no explicit
// level picks and the model's default tier stands.
func ReasoningLevels(modelID string) []string {
	levelsOnce.Do(func() {
		path := findProviderConfig(FindBundle())
		if path == "" {
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		levelsTable = compileModelRules(parseModelRules(b))
	})
	return resolveLevels(levelsTable, modelID)
}
