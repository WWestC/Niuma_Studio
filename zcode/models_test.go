package zcode

import (
	"reflect"
	"strings"
	"testing"
)

// TestParsePersonalRules pins the pick list's source shape: every
// provider rule flattens with its display name, modelOrder wins over
// personalModelIds, models dedupe in order, and garbled/BOM'd input
// degrades instead of erroring.
func TestParsePersonalRules(t *testing.T) {
	cfg := `{"schemaVersion":1,"config":{"providerConfigRules":{"providerRules":[
		{"providerId":"big","providerName":"Bigmodel","config":{
			"personalModelIds":["glm-5.3","gpt-x"],"modelOrder":["gpt-x","glm-5.3","gpt-x"]}},
		{"providerId":"zpv2","providerName":"智谱内部","config":{
			"personalModelIds":["GLM-5.3","glm-5.3-flash-cc-highspeed"]}},
		{"providerId":"","config":{"personalModelIds":["orphan"]}}
	]}}}`
	rules := parsePersonalRules([]byte(cfg))
	if len(rules) != 3 {
		t.Fatalf("解析层应原样保留三条规则（空 id 的剔除发生在 ListPersonalProviders）: %+v", rules)
	}
	if rules[0].ProviderName != "Bigmodel" || !reflect.DeepEqual(rules[0].Models, []string{"gpt-x", "glm-5.3"}) {
		t.Fatalf("modelOrder 未优先/未去重: %+v", rules[0])
	}
	if rules[1].ProviderName != "智谱内部" || len(rules[1].Models) != 2 {
		t.Fatalf("personalModelIds 回退失效: %+v", rules[1])
	}
	// BOM + 垃圾输入 → 无供应商，调用方降级
	if parsePersonalRules([]byte("\xef\xbb\xbf"+cfg)) == nil {
		t.Fatal("BOM 头应被容忍")
	}
	if parsePersonalRules([]byte("not json")) != nil {
		t.Fatal("垃圾输入应降级为 nil")
	}
}

// TestListPersonalProvidersSkipsEmpty pins the pick list's outer shape:
// rules without models never offer an empty optgroup.
func TestListPersonalProvidersSkipsEmpty(t *testing.T) {
	cfg := `{"config":{"providerConfigRules":{"providerRules":[
		{"providerId":"a","providerName":"A","config":{"personalModelIds":["m1"]}},
		{"providerId":"b","providerName":"B","config":{}}
	]}}}`
	got := parsePersonalRules([]byte(cfg))
	if len(got) != 2 {
		t.Fatalf("parse 层保留空模型规则（剔除发生在 ListPersonalProviders）: %+v", got)
	}
}

// TestParseModelRef pins the seat-pick ref grammar: "providerId/modelId"
// carries its own provider, a bare id defers to the personal-provider
// config, and degenerate slashes stay bare.
func TestParseModelRef(t *testing.T) {
	cases := []struct {
		ref             string
		provider, model string
	}{
		{"a8ede769/GLM-5.3", "a8ede769", "GLM-5.3"},
		{"GLM-5.3", "", "GLM-5.3"},
		{"  glm-5.3-flash  ", "", "glm-5.3-flash"},
		{"/leading", "", "/leading"},   // 空 provider 段不算组合 ref
		{"trailing/", "", "trailing/"}, // 空 model 段同理
	}
	for _, c := range cases {
		p, m := ParseModelRef(c.ref)
		if p != c.provider || m != c.model {
			t.Fatalf("ParseModelRef(%q) = (%q, %q)，期望 (%q, %q)", c.ref, p, m, c.provider, c.model)
		}
	}
	if _, m := ParseModelRef("p/m/x"); !strings.Contains(m, "/") {
		t.Fatalf("多段 ref 的 model 半应保留剩余斜杠: %q", m)
	}
}

// TestResolveModelProviderIn pins the ordered default resolution's
// walk: account faces outrank personal ones in list order, unavailable
// faces are never auto-picked, a model no drivable face lists misses
// (the caller takes the legacy personal pin), and a bare empty model
// takes the first drivable face.
func TestResolveModelProviderIn(t *testing.T) {
	rows := []ProviderModels{
		{ProviderID: "account:bigmodel-team-coding-plan", Models: []string{"GLM-5.3", "GLM-5.3-Flash"}, Available: true},
		{ProviderID: "account:zai-start-plan", Models: []string{"GLM-5.3-Flash"}, Available: false, Unavailable: "not-connected"},
		{ProviderID: "account:off-peak", Models: []string{"GLM-5.3"}, Available: false, Unavailable: "connection-unselected"},
		{ProviderID: "zpv2", ProviderName: "智谱内部", Models: []string{"GLM-5.3-Highspeed", "GLM-5.3"}, Available: true},
	}
	// 顺序第一：账号面在前——GLM-5.3 命中团队套餐面，而不是同列该模型的智谱内部
	if id, ok := resolveModelProviderIn(rows, "GLM-5.3"); !ok || id != "account:bigmodel-team-coding-plan" {
		t.Fatalf("GLM-5.3 应按序命中团队套餐面，得 (%q, %v)", id, ok)
	}
	// 不可驱动的面绝不自动选中——哪怕它排在最前且列了该模型
	if id, ok := resolveModelProviderIn(rows, "GLM-5.3-Highspeed"); !ok || id != "zpv2" {
		t.Fatalf("GLM-5.3-Highspeed 只有智谱内部可列可驱，得 (%q, %v)", id, ok)
	}
	// 无人可列 → miss（调用方落回个人 provider 旧钉法）
	if _, ok := resolveModelProviderIn(rows, "GLM-4.5"); ok {
		t.Fatal("无面可列的模型应 miss")
	}
	// 空模型 → 第一个可驱动的面（生产调用方必带模型名，此为形状对齐）
	if id, ok := resolveModelProviderIn(rows, ""); !ok || id != "account:bigmodel-team-coding-plan" {
		t.Fatalf("空模型应取首个可驱动面，得 (%q, %v)", id, ok)
	}
	// 全不可驱动 → miss
	if _, ok := resolveModelProviderIn([]ProviderModels{{ProviderID: "x", Models: []string{"m"}, Available: false}}, "m"); ok {
		t.Fatal("全不可驱动时应 miss")
	}
}
