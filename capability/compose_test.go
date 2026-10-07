package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
)

func baseCfg() agents.Config {
	return agents.Config{Name: "小测", Role: "测试员", Prompt: "任务指令"}
}

// 基线：空装配且开关关时，Compose 输出必须与 HandoffPrompt 逐字节一致。
func TestComposeBaselineNoSkills(t *testing.T) {
	cfg := baseCfg()
	fab, err := Compose(cfg, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if fab.Text != agents.HandoffPrompt(cfg) {
		t.Error("空装配的 Text 应与 HandoffPrompt 逐字节一致")
	}
	if fab.Manuals != nil || fab.Model != nil || fab.MCP != nil {
		t.Errorf("空装配不应产生任何槽位: %+v", fab)
	}
}

func TestComposeSkillSectionsInOrder(t *testing.T) {
	skills := []Skill{
		{Key: "a", Body: "指令A"},
		{Key: "b", Body: "指令B"},
	}
	fab, err := Compose(baseCfg(), skills, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(fab.Text, "【技能｜a】")
	j := strings.Index(fab.Text, "指令A")
	k := strings.Index(fab.Text, "【技能｜b】")
	l := strings.Index(fab.Text, "指令B")
	if i < 0 || j < 0 || k < 0 || l < 0 || !(i < j && j < k && k < l) {
		t.Errorf("技能正文应按装配顺序成节: %q", fab.Text[len(fab.Text)-200:])
	}
}

func TestComposeManualsDedupProfileFirst(t *testing.T) {
	cfg := baseCfg()
	cfg.Manual = "roles/base"
	skills := []Skill{
		{Key: "a", Manuals: []string{"roles/reviewer", "roles/base"}},
		{Key: "b", Manuals: []string{"roles/reviewer", "roles/hr"}},
	}
	fab, err := Compose(cfg, skills, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"roles/base", "roles/reviewer", "roles/hr"}
	if len(fab.Manuals) != len(want) {
		t.Fatalf("手册链应 profile 优先 + 去重: %q", fab.Manuals)
	}
	for i := range want {
		if fab.Manuals[i] != want[i] {
			t.Errorf("手册链[%d]=%q, want %q", i, fab.Manuals[i], want[i])
		}
	}
}

func TestComposeMCPPassthrough(t *testing.T) {
	mcps := []MCPServer{
		{Key: "penpot", Type: "http", URL: "http://127.0.0.1:4401/mcp"},
		{Key: "search", Type: "sse", URL: "http://127.0.0.1:9/sse"},
	}
	fab, err := Compose(baseCfg(), nil, mcps, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fab.MCP) != 2 || fab.MCP[0].Key != "penpot" || fab.MCP[1].Key != "search" {
		t.Fatalf("MCP 列表应原样按序过, 得 %+v", fab.MCP)
	}
	// fabric 不得别名调用方内存
	mcps[0].URL = "mutated"
	if fab.MCP[0].URL != "http://127.0.0.1:4401/mcp" {
		t.Error("MCP 应为拷贝，不得别名调用方内存")
	}
}

func TestComposeAuthoringNoteGate(t *testing.T) {
	fabOff, _ := Compose(baseCfg(), nil, nil, false)
	if strings.Contains(fabOff.Text, "自制技能协议") {
		t.Error("开关关时面料不应携带自制技能协议")
	}
	fabOn, _ := Compose(baseCfg(), nil, nil, true)
	if !strings.Contains(fabOn.Text, "自制技能协议") || !strings.Contains(fabOn.Text, "niuma-skill") {
		t.Error("开关开时面料应携带自制技能协议说明")
	}
	if !strings.HasSuffix(fabOn.Text, AuthoringNote) {
		t.Error("协议说明应在面料末尾")
	}
}

func TestComposeDuplicateKeysFollowed(t *testing.T) {
	sk := Skill{Key: "dup", Body: "X"}
	fab, _ := Compose(baseCfg(), []Skill{sk, sk}, nil, false)
	if got := strings.Count(fab.Text, "【技能｜dup】"); got != 2 {
		t.Errorf("重复 key 照单全收：正文应落两次，得 %d 次", got)
	}
}

func TestComposeFabricCap(t *testing.T) {
	cfg := baseCfg()
	cfg.Prompt = strings.Repeat("长", MaxFabricBytes) // 基座就超限
	if _, err := Compose(cfg, []Skill{{Key: "a", Body: "x"}}, nil, false); err == nil {
		t.Error("超限面料应拒绝装配并报因")
	}
}

func TestEffectiveLists(t *testing.T) {
	if got := EffectiveSkills([]string{"p1", "p2"}, []string{"s1"}); len(got) != 1 || got[0] != "s1" {
		t.Errorf("非空岗位装配应整体接管，得 %q", got)
	}
	if got := EffectiveSkills([]string{"p1"}, nil); len(got) != 1 || got[0] != "p1" {
		t.Errorf("空岗位装配应继承档案默认，得 %q", got)
	}
	if got := EffectiveMCPs([]string{"m1"}, []string{"n1", "n2"}); len(got) != 2 || got[0] != "n1" {
		t.Errorf("EffectiveMCPs 同律，得 %q", got)
	}
}

func TestResolveDegrades(t *testing.T) {
	if got := ResolveSkills(nil, []string{"a"}); got != nil {
		t.Error("nil store 应得 nil")
	}
	if got := ResolveMCPs(nil, []string{"a"}); got != nil {
		t.Error("nil store 应得 nil（MCP）")
	}
	s, _ := Open("", "")
	if got := ResolveSkills(s, nil); got != nil {
		t.Error("空 keys 应得 nil")
	}
	_, _ = s.UpsertSkill(Skill{Key: "a", Name: "A", Body: "A"})
	_, _ = s.UpsertSkill(Skill{Key: "c", Name: "C"})
	got := ResolveSkills(s, []string{"a", "ghost", "a", "c"})
	if len(got) != 3 || got[0].Key != "a" || got[1].Key != "a" || got[2].Key != "c" {
		t.Errorf("缺失键静默降级、重复照给、顺序保持，得 %+v", got)
	}
	_, _ = s.UpsertMCP(MCPServer{Key: "m", Name: "M", Type: "http", URL: "http://x/y"})
	if mcp := ResolveMCPs(s, []string{"m", "ghost"}); len(mcp) != 1 || mcp[0].Key != "m" {
		t.Errorf("MCP 解析同律，得 %+v", mcp)
	}
}

func TestMigrateLegacyPacks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capabilities.json")
	legacy := `[{"key":"a","name":"A","prompt":"正文A","manuals":["roles/hr"],"created_by":"房主","created_ts":42},
	{"key":"b","name":"B","model":{"id":"m1"}},
	{"key":"c","name":"C","tools":{"deny":["Bash"]}},
	{"key":"d","name":"D","manuals":["docs/x"]}]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := MigrateLegacyPacks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("有 prompt/手册槽的包才迁移（a、d），得 %d 个: %+v", len(got), got)
	}
	if got[0].Key != "a" || got[0].Body != "正文A" || len(got[0].Manuals) != 1 || got[0].CreatedBy != "房主" || got[0].CreatedTS != 42 {
		t.Errorf("a 包应平移为技能: %+v", got[0])
	}
	if got[1].Key != "d" || got[1].Body != "" || len(got[1].Manuals) != 1 {
		t.Errorf("d 包应为纯手册技能: %+v", got[1])
	}
	// 缺文件 = 无事可做
	if got, err := MigrateLegacyPacks(filepath.Join(dir, "none.json")); err != nil || got != nil {
		t.Errorf("缺失旧库应得 (nil,nil)，得 (%v,%v)", got, err)
	}
}
