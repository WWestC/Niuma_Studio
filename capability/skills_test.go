package capability

import (
	"strings"
	"testing"
)

func TestValidKey(t *testing.T) {
	good := []string{"a", "reviewer", "code-review", "roles_hr", "x0_-", strings.Repeat("k", 32)}
	for _, k := range good {
		if !ValidKey(k) {
			t.Errorf("ValidKey(%q) = false, want true", k)
		}
	}
	bad := []string{"", "A", "Big", "a b", "中", "a/b", "a.b", strings.Repeat("k", 33)}
	for _, k := range bad {
		if ValidKey(k) {
			t.Errorf("ValidKey(%q) = true, want false", k)
		}
	}
}

func TestSkillValidate(t *testing.T) {
	ok := Skill{Key: "reviewer", Name: "评审员", Body: strings.Repeat("a", MaxSkillBody)}
	if err := ok.Validate(); err != nil {
		t.Fatalf("boundary-valid skill refused: %v", err)
	}
	cases := []struct {
		name  string
		skill Skill
	}{
		{"大写 key", Skill{Key: "Reviewer", Name: "n"}},
		{"空名称", Skill{Key: "k", Name: ""}},
		{"名称超长", Skill{Key: "k", Name: strings.Repeat("名", maxNameRunes+1)}},
		{"描述超长", Skill{Key: "k", Name: "n", Desc: strings.Repeat("述", maxDescRunes+1)}},
		{"正文超限", Skill{Key: "k", Name: "n", Body: strings.Repeat("a", MaxSkillBody+1)}},
		{"手册超数", Skill{Key: "k", Name: "n", Manuals: []string{"a", "b", "c", "d", "e"}}},
		{"手册空段", Skill{Key: "k", Name: "n", Manuals: []string{"roles/"}}},
		{"手册三级", Skill{Key: "k", Name: "n", Manuals: []string{"a/b/c"}}},
		{"手册大写", Skill{Key: "k", Name: "n", Manuals: []string{"Roles/hr"}}},
	}
	for _, c := range cases {
		if err := c.skill.Validate(); err == nil {
			t.Errorf("%s: Validate 意外通过（应拒绝）", c.name)
		}
	}
	// 空正文合法：纯手册引用技能
	if err := (Skill{Key: "k", Name: "n", Manuals: []string{"roles/hr"}}).Validate(); err != nil {
		t.Errorf("纯手册技能应合法: %v", err)
	}
}

func TestSkillSanitize(t *testing.T) {
	in := Skill{
		Key:       "k",
		Name:      "  名字超出——" + strings.Repeat("长", maxNameRunes+4) + "  ",
		Desc:      "\n 描述 \t",
		Body:      "  面料  ",
		Manuals:   []string{" a ", "", "b"},
		CreatedBy: "房\r间里的人" + strings.Repeat("X", maxByRunes),
	}
	out := in.sanitize()
	if out.Name != "名字超出——"+strings.Repeat("长", maxNameRunes-len([]rune("名字超出——"))) {
		t.Errorf("Name 未按 rune 数收敛到 %d: %q (%d)", maxNameRunes, out.Name, len([]rune(out.Name)))
	}
	if out.Desc != "描述" {
		t.Errorf("Desc 应 trim: %q", out.Desc)
	}
	if out.Body != "面料" {
		t.Errorf("Body 应 trim 且绝不截断: %q", out.Body)
	}
	if len(out.Manuals) != 2 || out.Manuals[0] != "a" || out.Manuals[1] != "b" {
		t.Errorf("Manuals 应去空并 trim: %q", out.Manuals)
	}
	if out.CreatedBy != "房间里的人"+strings.Repeat("X", maxByRunes-len([]rune("房间里的人"))) {
		t.Errorf("CreatedBy 控制符剥离 + rune 收敛: %q", out.CreatedBy)
	}
	empty := Skill{Key: "k", Name: "n", Manuals: []string{" ", ""}}.sanitize()
	if empty.Manuals != nil {
		t.Errorf("全空 Manuals 应归 nil，得 %q", empty.Manuals)
	}
}

func TestValidManualKey(t *testing.T) {
	for _, m := range []string{"roles/reviewer", "hr", strings.Repeat("a", 64)} {
		if !validManualKey(m) {
			t.Errorf("validManualKey(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "/x", "x/", "a/b/c", strings.Repeat("a", 65), "角色/手册"} {
		if validManualKey(m) {
			t.Errorf("validManualKey(%q) = true, want false", m)
		}
	}
}

func TestMCPValidate(t *testing.T) {
	ok := MCPServer{Key: "penpot", Name: "设计工具", Type: "http", URL: "http://localhost:4401/mcp",
		Headers: []MCPHeader{{Name: "Authorization", Value: "Bearer tok"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法 MCP 服务被拒: %v", err)
	}
	cases := []struct {
		name string
		m    MCPServer
	}{
		{"大写 key", MCPServer{Key: "P", Name: "n", Type: "http", URL: "http://x/y"}},
		{"空名称", MCPServer{Key: "k", Name: "", Type: "http", URL: "http://x/y"}},
		{"type 词汇", MCPServer{Key: "k", Name: "n", Type: "stdio", URL: "http://x/y"}},
		{"url 空", MCPServer{Key: "k", Name: "n", Type: "http", URL: ""}},
		{"url 非 http", MCPServer{Key: "k", Name: "n", Type: "http", URL: "ftp://x/y"}},
		{"url 相对", MCPServer{Key: "k", Name: "n", Type: "http", URL: "/mcp"}},
		{"url 无 host", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://"}},
		{"url 超长", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/" + strings.Repeat("a", maxMCPURLRunes)}},
		{"头超数", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/y",
			Headers: []MCPHeader{{Name: "a", Value: "1"}, {Name: "b", Value: "1"}, {Name: "c", Value: "1"}, {Name: "d", Value: "1"},
				{Name: "e", Value: "1"}, {Name: "f", Value: "1"}, {Name: "g", Value: "1"}, {Name: "h", Value: "1"}, {Name: "i", Value: "1"}}}},
		{"头名空白", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/y", Headers: []MCPHeader{{Name: "a b", Value: "1"}}}},
		{"头名超长", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/y", Headers: []MCPHeader{{Name: strings.Repeat("h", maxHeaderName+1), Value: "1"}}}},
		{"头值超长", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/y", Headers: []MCPHeader{{Name: "h", Value: strings.Repeat("v", maxHeaderValue+1)}}}},
		{"头名重复", MCPServer{Key: "k", Name: "n", Type: "http", URL: "http://x/y",
			Headers: []MCPHeader{{Name: "X-A", Value: "1"}, {Name: "x-a", Value: "2"}}}},
	}
	for _, c := range cases {
		if err := c.m.Validate(); err == nil {
			t.Errorf("%s: Validate 意外通过（应拒绝）", c.name)
		}
	}
}

func TestMCPSanitizeAndMask(t *testing.T) {
	in := MCPServer{Key: "k", Name: "  名字  ", Desc: " d ", Type: " http ", URL: " http://x/y ",
		Headers: []MCPHeader{{Name: " a ", Value: " v "}, {Name: "", Value: "x"}, {Name: "b", Value: "12345678"}}}
	out := in.sanitize()
	if out.Name != "名字" || out.Desc != "d" || out.Type != "http" || out.URL != "http://x/y" {
		t.Errorf("sanitize 应 trim 各槽: %+v", out)
	}
	if len(out.Headers) != 2 || out.Headers[0].Name != "a" || out.Headers[0].Value != "v" {
		t.Errorf("头应 trim 并去空名行: %+v", out.Headers)
	}
	masked := out.MaskedHeaders()
	if masked[1].Value != "…5678" {
		t.Errorf("脱敏值应只剩尾 4 字符: %q", masked[1].Value)
	}
	if masked[0].Value != "v" {
		t.Errorf("短值原样: %q", masked[0].Value)
	}
}
