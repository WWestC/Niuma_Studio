package capability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSkillUpsertProvenance(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "skills.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	sk, err := s.UpsertSkill(Skill{Key: "k", Name: "first", CreatedBy: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	if sk.CreatedBy != "房主" || sk.CreatedTS == 0 {
		t.Fatalf("创建应盖章 created_by/created_ts，得 %+v", sk)
	}
	// 更新是整体替换：槽位被清空是可表达的；但创建溯源保留（记录谁生的，不是谁最后摸的）
	sk2, err := s.UpsertSkill(Skill{Key: "k", Name: "second", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if sk2.CreatedBy != "房主" || sk2.CreatedTS != sk.CreatedTS {
		t.Errorf("更新应保留创建溯源，得 by=%q ts=%d", sk2.CreatedBy, sk2.CreatedTS)
	}
	got, ok := s.GetSkill("k")
	if !ok || got.Name != "second" || got.Body != "x" {
		t.Errorf("更新后应整体替换，得 %+v", got)
	}
	if len(s.ListSkills()) != 1 {
		t.Errorf("同 key upsert 不应产生孪生，库里有 %d 条", len(s.ListSkills()))
	}
}

func TestStoreMCPUpsertAndPersist(t *testing.T) {
	dir := t.TempDir()
	skillsPath := filepath.Join(dir, "skills.json")
	mcpsPath := filepath.Join(dir, "mcps.json")
	s, _ := Open(skillsPath, mcpsPath)
	m, err := s.UpsertMCP(MCPServer{Key: "penpot", Name: "设计", Type: "http", URL: "http://127.0.0.1:4401/mcp", CreatedBy: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	if m.CreatedTS == 0 {
		t.Fatal("创建应盖章 created_ts")
	}
	if _, err := s.UpsertMCP(MCPServer{Key: "penpot", Name: "设计2", Type: "http", URL: "http://x/y"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetMCP("penpot"); got.Name != "设计2" || got.CreatedBy != "房主" {
		t.Errorf("整体替换且保留溯源，得 %+v", got)
	}
	if _, err := s.UpsertMCP(MCPServer{Key: "bad", Name: "n", Type: "stdio", URL: "http://x/y"}); err == nil {
		t.Error("非法 type 应被拒")
	}
	// 两文件各自持久化：重开后两库都在
	s2, err := Open(skillsPath, mcpsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s2.GetMCP("penpot"); got.Name != "设计2" {
		t.Errorf("MCP 应持久化，得 %+v", got)
	}
}

func TestStoreAuthoringSwitchPersists(t *testing.T) {
	dir := t.TempDir()
	skillsPath := filepath.Join(dir, "skills.json")
	s, _ := Open(skillsPath, "")
	if s.ModelAuthoring() {
		t.Error("缺省开关应关")
	}
	s.SetModelAuthoring(true)
	s.SetModelAuthoring(true) // 幂等
	if !s.ModelAuthoring() {
		t.Fatal("开关应已开")
	}
	s2, _ := Open(skillsPath, "")
	if !s2.ModelAuthoring() {
		t.Error("开关应随 skills.json 持久化")
	}
	s2.SetModelAuthoring(false)
	s3, _ := Open(skillsPath, "")
	if s3.ModelAuthoring() {
		t.Error("关闭也应持久化")
	}
}

func TestStoreCaps(t *testing.T) {
	s, _ := Open("", "")
	for i := 0; i < MaxSkills; i++ {
		key := string(rune('a'+i%26)) + string(rune('a'+i/26)) + string(rune('a'+i/676))
		if _, err := s.UpsertSkill(Skill{Key: key, Name: "n"}); err != nil {
			t.Fatalf("第 %d 个技能不应被拒: %v", i+1, err)
		}
	}
	if _, err := s.UpsertSkill(Skill{Key: "overflow", Name: "n"}); err == nil {
		t.Error("第 65 个技能应被拒（库满）")
	}
	// 库满后更新既有 key 仍然合法
	if _, err := s.UpsertSkill(Skill{Key: "aaa", Name: "updated"}); err != nil {
		t.Errorf("库满时更新既有技能不应被拒: %v", err)
	}
	for i := 0; i < MaxMCPs; i++ {
		key := string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := s.UpsertMCP(MCPServer{Key: key, Name: "n", Type: "http", URL: "http://x/y"}); err != nil {
			t.Fatalf("第 %d 个 MCP 不应被拒: %v", i+1, err)
		}
	}
	if _, err := s.UpsertMCP(MCPServer{Key: "overflow", Name: "n", Type: "http", URL: "http://x/y"}); err == nil {
		t.Error("第 17 个 MCP 应被拒（库满）")
	}
}

func TestStoreRemoveAndPersist(t *testing.T) {
	dir := t.TempDir()
	skillsPath := filepath.Join(dir, "skills.json")
	mcpsPath := filepath.Join(dir, "mcps.json")
	s, _ := Open(skillsPath, mcpsPath)
	for _, k := range []string{"a", "b"} {
		if _, err := s.UpsertSkill(Skill{Key: k, Name: k}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpsertMCP(MCPServer{Key: "m", Name: "m", Type: "http", URL: "http://x/y"}); err != nil {
		t.Fatal(err)
	}
	if !s.RemoveSkill("a") {
		t.Error("RemoveSkill(存在) 应 true")
	}
	if s.RemoveSkill("a") {
		t.Error("RemoveSkill(已删) 应 false")
	}
	if !s.RemoveMCP("m") || s.RemoveMCP("m") {
		t.Error("RemoveMCP 两态应正确")
	}
	// 重开验证持久化：落盘的是删除后的快照
	s2, err := Open(skillsPath, mcpsPath)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.ListSkills()
	if len(list) != 1 || list[0].Key != "b" {
		t.Errorf("重开后技能应只剩 b，得 %+v", list)
	}
	if len(s2.ListMCPs()) != 0 {
		t.Errorf("重开后 MCP 库应空，得 %+v", s2.ListMCPs())
	}
	if s2.Rev() != 0 {
		t.Errorf("重开是全新计数，Rev=%d", s2.Rev())
	}
}

func TestStoreOpenErrorsAndMemory(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad-skills.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(bad, ""); err == nil {
		t.Error("损坏技能库应作为 Open 错误暴露")
	}
	badM := filepath.Join(dir, "bad-mcps.json")
	if err := os.WriteFile(badM, []byte("[not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open("", badM); err == nil {
		t.Error("损坏 MCP 库应作为 Open 错误暴露")
	}
	missing := filepath.Join(dir, "nope.json")
	s, err := Open(missing, missing)
	if err != nil {
		t.Fatalf("缺失文件应空库起步: %v", err)
	}
	if len(s.ListSkills()) != 0 || len(s.ListMCPs()) != 0 {
		t.Error("空库应 0 条")
	}
	mem, _ := Open("", "") // 空路径 = 仅内存
	if _, err := mem.UpsertSkill(Skill{Key: "k", Name: "n"}); err != nil {
		t.Fatalf("内存库 upsert: %v", err)
	}
	if _, err := mem.UpsertMCP(MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/y"}); err != nil {
		t.Fatalf("内存库 upsert MCP: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "caps.json")); err == nil {
		t.Error("空路径不应落盘")
	}
}

func TestStoreUpsertValidation(t *testing.T) {
	s, _ := Open("", "")
	if _, err := s.UpsertSkill(Skill{Key: "BAD", Name: "n"}); err == nil {
		t.Error("非法 key 应被拒")
	}
	if _, err := s.UpsertSkill(Skill{Key: "ok", Name: ""}); err == nil {
		t.Error("空名称应被拒")
	}
	// 超长正文拒绝而非静默截断
	if _, err := s.UpsertSkill(Skill{Key: "ok", Name: "n", Body: strings.Repeat("b", MaxSkillBody+1)}); err == nil {
		t.Error("超限正文应拒绝保存")
	}
	if _, err := s.UpsertMCP(MCPServer{Key: "ok", Name: "n", Type: "http", URL: "//bad"}); err == nil {
		t.Error("非法 URL 应拒绝保存")
	}
}
