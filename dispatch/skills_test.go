package dispatch

// 技能与 MCP 的调度器半边（dispatch/skills.go）：①斜杠附着——人类
// 消息里的 /key 词元命中技能库时，注入正文以【技能｜key】节前置；
// ②自制技能协议线——成员回复里的 ```niuma-skill 围栏块，开关开则
// 入库＋剥块＋系统行回执，关则忽略＋提示行；③出生挂 MCP——装配了
// MCP 的成员会话经 CreateSessionMCP 出生（非空即整体替换默认 fleet）。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

func openTestLibrary(t *testing.T) *capability.Store {
	t.Helper()
	lib, err := capability.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func testSkillDispatcher(t *testing.T, lib *capability.Store, br Bridge) (*Dispatcher, *chat.Hub) {
	t.Helper()
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	d := Start(hub, store, br, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
		Library:     lib,
	})
	t.Cleanup(d.Stop)
	return d, hub
}

// --- 斜杠附着 -----------------------------------------------------------

func TestAttachSlashSkillsPrependsSections(t *testing.T) {
	lib := openTestLibrary(t)
	if _, err := lib.UpsertSkill(capability.Skill{Key: "review", Name: "评审", Body: "评审前先看错误面"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.UpsertSkill(capability.Skill{Key: "writer", Name: "文案", Body: "先列大纲"}); err != nil {
		t.Fatal(err)
	}
	d, _ := testSkillDispatcher(t, lib, &ackBridge{})
	line := "【办公室消息｜来自 房主】/review @小马 看看这个分支"
	out := d.attachSlashSkills("/review @小马 看看这个分支", line)
	i := strings.Index(out, "【技能｜review】")
	j := strings.Index(out, "评审前先看错误面")
	k := strings.Index(out, "【办公室消息｜来自 房主】")
	if i < 0 || j < 0 || k < 0 || !(i < j && j < k) {
		t.Errorf("技能节应前置于消息节: %q", out)
	}
	// 未知 /foo 词原样不动；重复 key 去重；URL 里的路径不误伤
	out2 := d.attachSlashSkills("/review /review /unknown http://x/a/b", "LINE")
	if got := strings.Count(out2, "【技能｜review】"); got != 1 {
		t.Errorf("重复 token 应去重，得 %d 次", got)
	}
	if strings.Contains(out2, "unknown") && strings.Contains(out2, "【技能｜unknown】") {
		t.Error("未知 key 不应产生技能节")
	}
	if out3 := d.attachSlashSkills("没有斜杠", "LINE"); out3 != "LINE" {
		t.Errorf("无 token 应原样返回，得 %q", out3)
	}
}

// --- 自制技能协议线 --------------------------------------------------------

func systemTexts(hub *chat.Hub) []string {
	var out []string
	for _, m := range hub.History() {
		if m.Type == chat.MsgSystem {
			out = append(out, m.Text)
		}
	}
	return out
}

func hasSystem(hub *chat.Hub, substr string) bool {
	for _, s := range systemTexts(hub) {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func TestSkillProposalSwitchOnSavesAndStrips(t *testing.T) {
	lib := openTestLibrary(t)
	lib.SetModelAuthoring(true)
	ab := &ackBridge{}
	d, hub := testSkillDispatcher(t, lib, ab)
	if err := d.attach("小马", "工程师", "s-skill-1"); err != nil {
		t.Fatal(err)
	}
	reply := "我把评审套路固化一下：\n```niuma-skill\nkey: api-review\nname: 接口评审\n先查错误面，再看兼容性。\n```\n以上。"
	out, acted := d.skillProposal(&member{name: "小马"}, reply)
	if !acted {
		t.Fatal("应视为自制技能提案（acted）")
	}
	if strings.Contains(out, "```niuma-skill") || strings.Contains(out, "key: api-review") {
		t.Errorf("已入库的块应从镜像剥除，得 %q", out)
	}
	if !strings.Contains(out, "以上。") || !strings.Contains(out, "固化一下") {
		t.Errorf("块外正文应保留，得 %q", out)
	}
	sk, ok := lib.GetSkill("api-review")
	if !ok || sk.Name != "接口评审" || sk.Body != "先查错误面，再看兼容性。" {
		t.Errorf("技能应入库（正文 trim），得 %+v", sk)
	}
	if sk.CreatedBy != "小马" {
		t.Errorf("CreatedBy 应记成员名，得 %q", sk.CreatedBy)
	}
	if !hasSystem(hub, "自制了技能 api-review") {
		t.Errorf("应有收录系统行，得 %q", systemTexts(hub))
	}
	// 纯块回复：剥完为空，镜像无话
	out2, acted2 := d.skillProposal(&member{name: "小马"}, "```niuma-skill\nkey: x2\nname: X2\n正文\n```")
	if !acted2 || out2 != "" {
		t.Errorf("纯块回复剥完应为空，得 (%q,%v)", out2, acted2)
	}
}

func TestSkillProposalSwitchOffKeepsAndNotes(t *testing.T) {
	lib := openTestLibrary(t) // 缺省关
	d, hub := testSkillDispatcher(t, lib, &ackBridge{})
	reply := "```niuma-skill\nkey: k\nname: K\n正文\n```"
	out, acted := d.skillProposal(&member{name: "小马"}, reply)
	if !acted {
		t.Fatal("开关关也应有忽略提示（acted）")
	}
	if out != reply {
		t.Errorf("开关关原文应保留，得 %q", out)
	}
	if _, ok := lib.GetSkill("k"); ok {
		t.Error("开关关不应入库")
	}
	if !hasSystem(hub, "已忽略") {
		t.Errorf("应有忽略系统行，得 %q", systemTexts(hub))
	}
}

func TestSkillProposalMalformedBlockUntouched(t *testing.T) {
	lib := openTestLibrary(t)
	lib.SetModelAuthoring(true)
	d, hub := testSkillDispatcher(t, lib, &ackBridge{})
	// 缺 name: 行——不是提案，原文保留、无系统行、不入库
	reply := "```niuma-skill\nkey: only-key\n正文\n```"
	out, acted := d.skillProposal(&member{name: "小马"}, reply)
	if acted || out != reply {
		t.Errorf("形状不对的块应原样放行，得 (%q,%v)", out, acted)
	}
	if hasSystem(hub, "自制") {
		t.Error("不应有收录/忽略系统行")
	}
	// 非法 key：拦截、拒绝、保留原文＋未收录行
	reply2 := "```niuma-skill\nkey: BAD KEY\nname: N\n正文\n```"
	out2, acted2 := d.skillProposal(&member{name: "小马"}, reply2)
	if !acted2 {
		t.Fatal("非法 key 的块也应拦截（有回执）")
	}
	if out2 != reply2 {
		t.Errorf("未收录的块应保留原文，得 %q", out2)
	}
	if !hasSystem(hub, "未收录") {
		t.Errorf("应有未收录系统行，得 %q", systemTexts(hub))
	}
}

// onEvent 全链路：协议线先于镜像——入库＋剥块＋回执，残余正文照常说话。
func TestSkillProposalRidesOnEvent(t *testing.T) {
	lib := openTestLibrary(t)
	lib.SetModelAuthoring(true)
	ab := &ackBridge{}
	d, hub := testSkillDispatcher(t, lib, ab)
	if err := d.attach("小马", "工程师", "s-skill-evt"); err != nil {
		t.Fatal(err)
	}
	d.onEvent(turnCompletedEvent("s-skill-evt",
		"```niuma-skill\nkey: flow\nname: 流程\n三步走。\n```\n顺手说一句：流程已固化。"))
	waitFor(t, func() bool { return hasSystem(hub, "自制了技能 flow") }, "收录系统行应出现")
	if _, ok := lib.GetSkill("flow"); !ok {
		t.Error("技能应经 onEvent 入库")
	}
	found := false
	for _, m := range hub.History() {
		if m.Type == chat.MsgSay && m.Origin == chat.OriginBridge &&
			strings.Contains(m.Text, "流程已固化") && !strings.Contains(m.Text, "```niuma-skill") {
			found = true
		}
	}
	if !found {
		t.Errorf("残余正文应照常镜像且不带块，历史: %+v", hub.History())
	}
}

// --- 出生挂 MCP -----------------------------------------------------------

// mcpBridge 记录两种出生调用，可拔插 CreateSessionMCP 扩展。
type mcpBridge struct {
	ackBridge
	withMCP bool
	mu      sync.Mutex
	plain   int
	mcpSess [][]zcode.MCPServerSpec
}

func (f *mcpBridge) CreateSession(ctx context.Context, workspace, mode string) (string, error) {
	f.mu.Lock()
	f.plain++
	f.mu.Unlock()
	return "s-plain", nil
}

func (f *mcpBridge) CreateSessionMCP(ctx context.Context, workspace, mode string, servers []zcode.MCPServerSpec) (string, error) {
	f.mu.Lock()
	f.mcpSess = append(f.mcpSess, servers)
	f.mu.Unlock()
	return "s-mcp", nil
}

func TestCreateBirthSessionMCPOverride(t *testing.T) {
	lib := openTestLibrary(t)
	// 空装配/nil fabric → 普通 create
	br := &mcpBridge{}
	d2, _ := testSkillDispatcher(t, lib, br)
	if _, err := d2.createBirthSession(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if br.plain != 1 || len(br.mcpSess) != 0 {
		t.Fatalf("无 MCP 应走普通 create，得 plain=%d mcp=%d", br.plain, len(br.mcpSess))
	}
	// 装配 MCP → CreateSessionMCP，spec 名=key、isolation=session、头随行
	fabric := &capability.EffectiveFabric{MCP: []capability.MCPServer{
		{Key: "penpot", Type: "http", URL: "http://127.0.0.1:4401/mcp",
			Headers: []capability.MCPHeader{{Name: "Authorization", Value: "Bearer tok"}}},
		{Key: "search", Type: "sse", URL: "http://127.0.0.1:9/sse"},
	}}
	br2 := &mcpBridge{withMCP: true}
	d3, _ := testSkillDispatcher(t, lib, br2)
	if _, err := d3.createBirthSession(context.Background(), t.TempDir(), fabric); err != nil {
		t.Fatal(err)
	}
	if br2.plain != 0 || len(br2.mcpSess) != 1 {
		t.Fatalf("有 MCP 应走 CreateSessionMCP，得 plain=%d mcp=%d", br2.plain, len(br2.mcpSess))
	}
	specs := br2.mcpSess[0]
	if len(specs) != 2 || specs[0].Name != "penpot" || specs[0].Type != "http" ||
		specs[0].URL != "http://127.0.0.1:4401/mcp" || specs[0].Isolation != "session" {
		t.Errorf("spec 形状不对: %+v", specs[0])
	}
	if len(specs[0].Headers) != 1 || specs[0].Headers[0].Name != "Authorization" || specs[0].Headers[0].Value != "Bearer tok" {
		t.Errorf("请求头应随行: %+v", specs[0].Headers)
	}
	if specs[1].Name != "search" || specs[1].Type != "sse" {
		t.Errorf("第二个 spec 不对: %+v", specs[1])
	}
}
