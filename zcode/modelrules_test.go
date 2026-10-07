package zcode

import (
	"reflect"
	"testing"
)

// The bundled config's modelRules shape, as zcode-builtin.json ships
// it under config.modelConfigRules: rows without a reasoningLevel
// values list (pure contextWindow/inputFormat overrides) sit between
// the level-bearing ones.
const modelRulesFixture = `{"config":{"modelConfigRules":{"modelRules":[
	{"modelMatch":".*",
	 "config":{"optionSpecs":{"reasoningLevel":{"values":["disabled","enabled"]}}}},
	{"modelMatch":".*\\[1m\\]",
	 "config":{"properties":{"contextWindow":1000000}}},
	{"modelMatch":".*glm-5(?:[.\\-:/\\[].*)?",
	 "config":{"optionSpecs":{"reasoningLevel":{"values":["disabled","enabled"]},"maxOutputTokens":{"max":64000}}}},
	{"modelMatch":".*glm-5\\.3(?:-flash)?(?:[.\\-:/\\[].*)?",
	 "config":{"optionSpecs":{"reasoningLevel":{"values":["low","high","max"]}}}},
	{"modelMatch":".*gpt-5\\.6(?:-(?:sol|terra|luna))?(?:[.\\-:/\\[].*)?",
	 "config":{"optionSpecs":{"reasoningLevel":{"values":["none","low","medium","high","xhigh","max"]}}}},
	{"modelMatch":".*broken([)",
	 "config":{"optionSpecs":{"reasoningLevel":{"values":["x","y"]}}}}
]}}}`

// TestParseModelRules pins the parse half: values-bearing rows only,
// garbage degrades to nil.
func TestParseModelRules(t *testing.T) {
	rules := parseModelRules([]byte(modelRulesFixture))
	if len(rules) != 5 {
		t.Fatalf("应保留 5 条带档位的规则（无 values 的 [1m] 行剔除）: %+v", rules)
	}
	if !reflect.DeepEqual(rules[2].Levels, []string{"low", "high", "max"}) || rules[2].Match != `.*glm-5\.3(?:-flash)?(?:[.\-:/\[].*)?` {
		t.Fatalf("glm-5.3 规则解析错误: %+v", rules[2])
	}
	if parseModelRules([]byte("not json")) != nil {
		t.Fatal("垃圾输入应降级为 nil")
	}
}

// TestResolveModelRuleLevels pins the picker's cascade as the CLI runs
// it: file order, a later match overrides an earlier one (the
// catch-all ".*" ships first), case-insensitively against the bare
// model id — the builtin lists "GLM-5.3" while its rules write
// "glm-5\.3". An id nothing matches, or an empty id, resolves nil.
func TestResolveModelRuleLevels(t *testing.T) {
	table := compileModelRules(parseModelRules([]byte(modelRulesFixture)))
	if len(table) != 4 {
		t.Fatalf("无法编译的 broken 正则应跳行而非弃表: %d", len(table))
	}
	cases := []struct {
		id   string
		want []string
	}{
		{"GLM-5.3", []string{"low", "high", "max"}},       // 大小写不敏感 + 后行覆盖前行
		{"glm-5.3-flash", []string{"low", "high", "max"}}, // 同族变体
		{"gpt-5.6-sol", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"a8ede769/GLM-5.3", []string{"low", "high", "max"}}, // 完整引用（provider/model）照撞族规则
		{"kimi-k3", []string{"disabled", "enabled"}},         // 无专门规则 → 兜底
		{"", nil},
	}
	for _, c := range cases {
		if got := resolveLevels(table, c.id); !reflect.DeepEqual(got, c.want) {
			t.Errorf("resolveLevels(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// TestResolveModelRuleLevelsNoCatchAll: a table whose ship order loses
// the catch-all leaves strangers at nil — the caller then offers no
// explicit level picks instead of guessing.
func TestResolveModelRuleLevelsNoCatchAll(t *testing.T) {
	table := compileModelRules([]modelRule{
		{Match: `.*glm-5\.3`, Levels: []string{"low", "high", "max"}},
	})
	if got := resolveLevels(table, "kimi-k3"); got != nil {
		t.Errorf("无兜底规则时陌生模型应为 nil, got %v", got)
	}
}
