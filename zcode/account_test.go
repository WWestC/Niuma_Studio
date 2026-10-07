package zcode

import (
	"reflect"
	"testing"
)

// The desktop picker's account faces, as zcode-builtin.json ships
// them (access.type zhipu-account + builtinModelIds), reduced to the
// fields the composition reads.
const accountBuiltinFixture = `{"config":{"providerConfigRules":{"providerRules":[
	{"providerId":"account:bigmodel-individual-coding-plan","providerName":"BigModel Individual Coding Plan",
	 "config":{"access":{"type":"zhipu-account","mode":"individual-coding-plan","accountType":"bigmodel"},
	          "builtinModelIds":["GLM-5.3","GLM-5.3-Flash"]}},
	{"providerId":"account:bigmodel-team-coding-plan","providerName":"BigModel Team Coding Plan",
	 "config":{"access":{"type":"zhipu-account","mode":"team-coding-plan","accountType":"bigmodel"},
	          "builtinModelIds":["GLM-5.3","GLM-5.3-Flash"]}},
	{"providerId":"account:bigmodel-start-plan","providerName":"Start Plan",
	 "config":{"access":{"type":"zhipu-account","mode":"start-plan","accountType":"bigmodel"},
	          "builtinModelIds":["GLM-5.3-Flash","GLM-5.2","GLM-5-Turbo"]}},
	{"providerId":"account:zai-start-plan","providerName":"Start Plan",
	 "config":{"access":{"type":"zhipu-account","mode":"start-plan","accountType":"zai"},
	          "builtinModelIds":["GLM-5.3-Flash","GLM-5.2","GLM-5-Turbo"]}},
	{"providerId":"account:bigmodel-offpeak-idle-plan","providerName":"BigModel Idle plan",
	 "config":{"access":{"type":"zhipu-account","mode":"off-peak","accountType":"bigmodel"},
	          "visibility":"hidden","builtinModelIds":["GLM-5.3","GLM-5.3-Flash"]}},
	{"providerId":"tpl-bigmodel-api","providerName":"BigModel API",
	 "config":{"access":{"type":"api-key"},"builtinModelIds":["GLM-5.3"]}}
]}}}`

// TestParseAccountRules pins the parse half: zhipu-account rules only
// (api-key templates dropped), access fields flattened, garbage
// degrades to nil.
func TestParseAccountRules(t *testing.T) {
	rules := parseAccountRules([]byte(accountBuiltinFixture))
	if len(rules) != 5 {
		t.Fatalf("应保留 5 条 zhipu-account 规则（api-key 模板剔除）: %+v", rules)
	}
	if rules[1].ProviderID != "account:bigmodel-team-coding-plan" ||
		rules[1].Mode != "team-coding-plan" || rules[1].Family != "bigmodel" ||
		!reflect.DeepEqual(rules[1].Models, []string{"GLM-5.3", "GLM-5.3-Flash"}) {
		t.Fatalf("团队规则展平错误: %+v", rules[1])
	}
	if parseAccountRules([]byte("not json")) != nil {
		t.Fatal("垃圾输入应降级为 nil")
	}
}

// TestComposeAccountProviders pins the full composition matrix against
// the desktop registry's semantics: every visible face stands in file
// order — the selected+keyed faces available, the unselected twin and
// the keyless family annotated unselectable (never dropped), Start
// Plan trimmed to the entitled model, off-peak (visibility:hidden)
// and api-key templates never offer.
func TestComposeAccountProviders(t *testing.T) {
	rules := parseAccountRules([]byte(accountBuiltinFixture))
	kinds := map[string]string{"bigmodel": "team-coding-plan", "zai": "start-plan"}
	keys := map[string]string{
		"builtin:bigmodel-coding-plan": "team-key",
		"builtin:bigmodel-start-plan":  "jwt",
		// builtin:zai-* 无钥匙 → zai 面保留但标注未连接
	}
	got := composeAccountProviders(rules, kinds, keys)
	want := []ProviderModels{
		{ProviderID: "account:bigmodel-individual-coding-plan", ProviderName: "BigModel Individual Coding Plan",
			Badge: "个人", Models: []string{"GLM-5.3", "GLM-5.3-Flash"}, Unavailable: "connection-unselected"},
		{ProviderID: "account:bigmodel-team-coding-plan", ProviderName: "BigModel Team Coding Plan",
			Badge: "团队", Models: []string{"GLM-5.3", "GLM-5.3-Flash"}, Available: true},
		{ProviderID: "account:bigmodel-start-plan", ProviderName: "Start Plan",
			Badge: "免费", Models: []string{"GLM-5.3-Flash"}, Available: true},
		{ProviderID: "account:zai-start-plan", ProviderName: "Start Plan",
			Badge: "免费", Models: []string{"GLM-5.3-Flash", "GLM-5.2", "GLM-5-Turbo"}, Unavailable: "not-connected"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("合成结果与桌面注册表不符:\n got  %+v\n want %+v", got, want)
	}
}

// TestComposeAccountProvidersDegrades pins the degradation edges: no
// connection selections (garbled setting.json) mark the paid faces
// unselectable (connection-unselected) instead of hiding them, and a
// Start Plan rule without the entitled model keeps the full builtin
// list behind an unavailable verdict — the desktop's unentitled push
// leaves the shipped models untouched.
func TestComposeAccountProvidersDegrades(t *testing.T) {
	rules := parseAccountRules([]byte(accountBuiltinFixture))
	keys := map[string]string{"builtin:bigmodel-coding-plan": "k", "builtin:bigmodel-start-plan": "j"}
	got := composeAccountProviders(rules, nil, keys)
	var team, start *ProviderModels
	for i := range got {
		switch got[i].ProviderID {
		case "account:bigmodel-team-coding-plan":
			team = &got[i]
		case "account:bigmodel-start-plan":
			start = &got[i]
		}
	}
	if team == nil || team.Available || team.Unavailable != "connection-unselected" {
		t.Fatalf("无选择时付费团队面应保留并标注未选连接: %+v", team)
	}
	if start == nil || !start.Available || !reflect.DeepEqual(start.Models, []string{"GLM-5.3-Flash"}) {
		t.Fatalf("Start Plan 应照常可用且裁剪: %+v", start)
	}
	trimmed := accountRule{ProviderID: "account:bigmodel-start-plan", Mode: "start-plan", Family: "bigmodel",
		Models: []string{"GLM-5.2", "GLM-5-Turbo"}}
	got = composeAccountProviders([]accountRule{trimmed}, nil, keys)
	if len(got) != 1 || got[0].Available ||
		!reflect.DeepEqual(got[0].Models, []string{"GLM-5.2", "GLM-5-Turbo"}) ||
		got[0].Unavailable != "no-entitled-models" {
		t.Fatalf("Start Plan 无 entitled 模型时应整面保留为不可用: %+v", got)
	}
}

// TestBuiltinProviderFor pins the account→builtin key mapping the CLI
// itself uses (team and individual share the family coding-plan key;
// start-plan has its own) plus the strangers.
func TestBuiltinProviderFor(t *testing.T) {
	cases := map[string]string{
		"account:bigmodel-team-coding-plan":       "builtin:bigmodel-coding-plan",
		"account:bigmodel-individual-coding-plan": "builtin:bigmodel-coding-plan",
		"account:bigmodel-start-plan":             "builtin:bigmodel-start-plan",
		"account:zai-start-plan":                  "builtin:zai-start-plan",
		"account:zai-team-coding-plan":            "builtin:zai-coding-plan",
		"account:bigmodel-offpeak-idle-plan":      "",
		"a8ede769-personal":                       "",
		"":                                        "",
	}
	for in, want := range cases {
		if got := builtinProviderFor(in); got != want {
			t.Fatalf("builtinProviderFor(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestParseModelRefAccountRefs pins that account refs survive the seat
// grammar untouched — the colon-bearing provider id rides the slash
// split as-is (the picker's option values are exactly these refs).
func TestParseModelRefAccountRefs(t *testing.T) {
	p, m := ParseModelRef("account:bigmodel-team-coding-plan/GLM-5.3-Flash")
	if p != "account:bigmodel-team-coding-plan" || m != "GLM-5.3-Flash" {
		t.Fatalf("account ref 被错误切分: (%q, %q)", p, m)
	}
}

// TestSelectedPlanKindsAndKeys pins the setting.json/config.json parse
// halves: kinds take non-empty selections only, keys skip empty
// strings, both degrade to empty on garbage.
func TestSelectedPlanKindsAndKeys(t *testing.T) {
	kinds := selectedPlanKinds([]byte(`{"providerFamilyConnectionSelections":{
		"zai":{"kind":"start-plan"},"bigmodel":{"kind":"team-coding-plan"},"x":{}}}`))
	if !reflect.DeepEqual(kinds, map[string]string{"zai": "start-plan", "bigmodel": "team-coding-plan"}) {
		t.Fatalf("kinds 解析错误: %+v", kinds)
	}
	keys := builtinAPIKeys([]byte(`{"provider":{
		"builtin:bigmodel-coding-plan":{"options":{"apiKey":"plain-key"}},
		"builtin:zai-coding-plan":{"options":{"apiKey":""}}}}`))
	if len(keys) != 1 || keys["builtin:bigmodel-coding-plan"] != "plain-key" {
		t.Fatalf("keys 解析错误: %+v", keys)
	}
	if selectedPlanKinds([]byte("garbage")) != nil || builtinAPIKeys([]byte("garbage")) != nil {
		t.Fatal("垃圾输入应降级为 nil")
	}
}
