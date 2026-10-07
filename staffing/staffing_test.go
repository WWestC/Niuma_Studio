package staffing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openMem is the in-memory shape (path "") — every persistence test
// here exercises the mutation semantics, not the file round-trip.
func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSetModelRoundTrip pins the seat-model slot's write face: stamp,
// read back through Get, clear back to the default chain. The two
// slots write as one triple but each may fly solo — a reasoning on an
// empty pick rides the default-chain model (the ZCode picker's shape).
func TestSetModelRoundTrip(t *testing.T) {
	s := openMem(t)
	if _, err := s.Join("proj", "张三", "后端", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetModel("proj", "张三", "a8ede769/GLM-5.3", "low"); err != nil {
		t.Fatal(err)
	}
	row, ok := s.Get("proj", "张三")
	if !ok || row.Model != "a8ede769/GLM-5.3" || row.Reasoning != "low" {
		t.Fatalf("SetModel 未落行: %+v", row)
	}
	// 重新选档不带思考强度 = 回模型缺省档（不是沿用旧档）
	if _, err := s.SetModel("proj", "张三", "a8ede769/GLM-5.3-Flash", ""); err != nil {
		t.Fatal(err)
	}
	if row, _ = s.Get("proj", "张三"); row.Model != "a8ede769/GLM-5.3-Flash" || row.Reasoning != "" {
		t.Fatalf("重选模型应清思考档: %+v", row)
	}
	// 强度单飞：空模型＋非空强度＝默认链模型挂该档（不清档）
	if _, err := s.SetModel("proj", "张三", "", "high"); err != nil {
		t.Fatal(err)
	}
	if row, _ = s.Get("proj", "张三"); row.Model != "" || row.Reasoning != "high" {
		t.Fatalf("空模型应保留单飞强度: %+v", row)
	}
	// clear = 两槽皆空（the default chain decides again）
	if _, err := s.SetModel("proj", "张三", "", ""); err != nil {
		t.Fatal(err)
	}
	if row, _ = s.Get("proj", "张三"); row.Model != "" || row.Reasoning != "" {
		t.Fatalf("清空未生效（两槽皆空才清档）: %+v", row)
	}
}

// TestSetModelGuards pins the slot's refusal edges: offboard rows are
// history (the pick rides through Join instead), missing rows refuse,
// whitespace/oversize refs never land.
func TestSetModelGuards(t *testing.T) {
	s := openMem(t)
	if _, err := s.Join("proj", "李四", "测试", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Offboard("proj", "李四"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetModel("proj", "李四", "p/m", ""); err == nil ||
		!strings.Contains(err.Error(), "已离编") {
		t.Fatalf("离编行应拒绝改档: %v", err)
	}
	if _, err := s.SetModel("proj", "无名", "p/m", ""); err == nil {
		t.Fatal("缺行应拒绝")
	}
	if _, err := s.SetModel("proj", "王五", "has space", ""); err == nil {
		t.Fatal("含空白的 ref 应拒绝")
	}
	if _, err := s.SetModel("proj", "王五", strings.Repeat("x", 161), ""); err == nil {
		t.Fatal("超长 ref 应拒绝")
	}
	if _, err := s.SetModel("proj", "王五", "p/m", "High"); err == nil {
		t.Fatal("思考强度大写 token 应拒绝（^[a-z0-9]{1,24}$）")
	}
	if _, err := s.SetModel("proj", "王五", "p/m", strings.Repeat("x", 25)); err == nil {
		t.Fatal("思考强度超长应拒绝")
	}
	if _, err := s.Join("proj", "王五", "后端", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetModel("proj", "王五", "p/m", strings.Repeat("x", 24)); err != nil {
		t.Fatalf("合法思考强度不应拒绝: %v", err)
	}
}

// TestJoinCarriesModel pins the reinstatement carry: an offboard row's
// model tier (pick + thinking intensity) survives the 归还 Join (the
// recall/rebirth path must not wipe a configured seat), and the
// persisted file keeps both fields.
func TestJoinCarriesModel(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join("proj", "赵六", "前端", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetModel("proj", "赵六", "zhipu/glm-5.3-flash", "max"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Offboard("proj", "赵六"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join("proj", "赵六", "", ""); err != nil {
		t.Fatal(err)
	}
	row, _ := s.Get("proj", "赵六")
	if row.Model != "zhipu/glm-5.3-flash" || row.Reasoning != "max" {
		t.Fatalf("复职未携带模型档: %q/%q", row.Model, row.Reasoning)
	}
	b, err := os.ReadFile(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"reasoning"`) || !strings.Contains(string(b), `"max"`) {
		t.Fatalf("持久化文件应带 reasoning 字段: %s", b)
	}
}

// TestSetAutoSwitchRoundTrip pins the r_19 双开关: default
// OFF (an unattended token-burning mode must never ride a default),
// flips persist independently, and flipping either leaves auto_recall
// + the other switch untouched.
func TestSetAutoSwitchRoundTrip(t *testing.T) {
	s := openMem(t)
	set := s.SettingsOf("proj-x")
	if set.AutoStock || set.AutoAdvance || set.AnyAutoOn() {
		t.Fatal("双开关默认必须关——无人值守烧 token 的模式不能随默认值悄悄开")
	}
	// 只开补货：推进不动、AnyAutoOn 为真（引擎到访与 ⚡ 徽标口径）
	if set = s.SetAutoStock("proj-x", true); !set.AutoStock || set.AutoAdvance {
		t.Fatal("回执应只带回补货开启")
	}
	if set = s.SettingsOf("proj-x"); !set.AutoStock || set.AutoAdvance || !set.AnyAutoOn() {
		t.Fatal("补货开启未生效或殃及推进")
	}
	// 再开推进：补货不动（双开＝旧全智能）
	if set = s.SetAutoAdvance("proj-x", true); !set.AutoStock || !set.AutoAdvance {
		t.Fatal("回执应带回双开")
	}
	// 只关推进：补货存活（熔断语义——推进关、补货活）
	if set = s.SetAutoAdvance("proj-x", false); set.AutoAdvance || !set.AutoStock {
		t.Fatal("关推进殃及了补货")
	}
	// 全关后 AnyAutoOn 归假
	if set = s.SetAutoStock("proj-x", false); set.AnyAutoOn() {
		t.Fatal("全关后 AnyAutoOn 必须为假")
	}
	if !s.SettingsOf("proj-x").AutoRecall {
		t.Fatal("翻自动开关殃及了自动补员开关（行为位必须互不影响）")
	}
}

// TestSetRebuildVoteRoundTrip pins the AI-rebuild-vote switch: default
// OFF (a member-driven studio restart must never ride a default), the
// flip persists to disk (a reopen reads it back), and flipping leaves
// every other behavior bit untouched.
func TestSetRebuildVoteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staffing.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.SettingsOf("proj-x").RebuildVote {
		t.Fatal("AI 重编译投票默认必须关——成员可重启全室的高权面不能随默认值悄悄开")
	}
	s.SetAutoStock("proj-x", true)
	if set := s.SetRebuildVote("proj-x", true); !set.RebuildVote {
		t.Fatal("回执应带回开关开启")
	}
	if set := s.SettingsOf("proj-x"); !set.RebuildVote || !set.AutoStock || !set.AutoRecall {
		t.Fatal("翻投票开关未生效或殃及了其他行为位")
	}
	// 重开回读：开关要活在盘上（staffing.settings.json），不是内存里的即兴
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.SettingsOf("proj-x").RebuildVote {
		t.Fatal("重开后投票开关丢了（未落盘）")
	}
	if set := s2.SetRebuildVote("proj-x", false); set.RebuildVote {
		t.Fatal("关闭回执应带回关")
	}
}

// TestLegacyAutoPilotMigratesToDual pins the one-way fold: a persisted
// legacy autopilot:true loads as BOTH new switches on, the legacy bit
// clears, and the fold persists immediately — the resurrection guard
// (omitempty drops the off-switch keys from the file, so a surviving
// legacy true would re-enable both switches on next boot).
func TestLegacyAutoPilotMigratesToDual(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staffing.json")
	settingsPath := filepath.Join(dir, "staffing.settings.json")
	legacy := `{"proj-old": {"auto_recall": true, "autopilot": true}, "proj-off": {"auto_recall": true}}`
	if err := os.WriteFile(settingsPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	set := s.SettingsOf("proj-old")
	if !set.AutoStock || !set.AutoAdvance || !set.AnyAutoOn() {
		t.Fatal("旧 autopilot:true 必须折成双开关全开")
	}
	if set.AutoPilot {
		t.Fatal("迁移后旧位必须清 false（防复活）")
	}
	if set = s.SettingsOf("proj-off"); set.AutoStock || set.AutoAdvance || set.AutoPilot {
		t.Fatal("旧 autopilot 缺席/为假的项目不得被迁移波及")
	}
	// 迁移当场落盘：文件里旧键已清、新键已立——双关后重开不再复活
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"autopilot": true`) {
		t.Fatal("迁移未落盘：文件里残留旧键 true——双关后重启会复活")
	}
	s.SetAutoStock("proj-old", false)
	s.SetAutoAdvance("proj-old", false)
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if set = s2.SettingsOf("proj-old"); set.AutoStock || set.AutoAdvance || set.AnyAutoOn() {
		t.Fatal("双关后重开又被旧键复活（迁移落盘失效）")
	}
}

// TestWaterTargetKnob pins the r_19 蓄水目标线 knob: clamp 1–9, the
// 0=unset keep-semantics shared with the other knobs, and unset
// projects walking the default chain.
func TestWaterTargetKnob(t *testing.T) {
	s := openMem(t)
	if k := s.SettingsOf("proj-w").AutoPilotKnobs; k != nil && k.WaterTarget != 0 {
		t.Fatal("未设置项目的蓄水目标线必须走默认链")
	}
	set := s.SetAutoPilotKnobs("proj-w", AutoPilotKnobs{WaterTarget: 99})
	if got := set.AutoPilotKnobs.WaterTarget; got != 9 {
		t.Fatalf("上界夹紧失败: got %d want 9", got)
	}
	set = s.SetAutoPilotKnobs("proj-w", AutoPilotKnobs{WaterTarget: -3})
	if got := set.AutoPilotKnobs.WaterTarget; got != 1 {
		t.Fatalf("下界夹紧失败: got %d want 1", got)
	}
	// 0=保留已存值（只改一个旋钮不重置其他）
	set = s.SetAutoPilotKnobs("proj-w", AutoPilotKnobs{WaterTarget: 5, PokeEveryMin: 30})
	set = s.SetAutoPilotKnobs("proj-w", AutoPilotKnobs{AcceptDelayS: 60})
	if got := set.AutoPilotKnobs.WaterTarget; got != 5 {
		t.Fatalf("0 语义失效——蓄水目标线被重置: got %d want 5", got)
	}
	if got := set.AutoPilotKnobs.PokeEveryMin; got != 30 {
		t.Fatalf("0 语义失效——选题间隔被重置: got %d want 30", got)
	}
}

// TestSetBranchSlot pins the seat-branch slot (v2.7.1 分支隔离): stamp
// (中文分支名合法), read back, clear; refusal edges mirror SetModel.
func TestSetBranchSlot(t *testing.T) {
	s := openMem(t)
	if _, err := s.Join("proj", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBranch("proj", "小猿", "wt/小猿"); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Get("proj", "小猿"); row.Branch != "wt/小猿" {
		t.Fatalf("SetBranch 未落行: %+v", row)
	}
	if _, err := s.SetBranch("proj", "小猿", "-force"); err == nil {
		t.Fatal("选项形状的分支名应被拒")
	}
	if _, err := s.SetBranch("proj", "小猿", "a..b"); err == nil {
		t.Fatal("… 形状的分支名应被拒")
	}
	if _, err := s.SetBranch("proj", "小猿", ""); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Get("proj", "小猿"); row.Branch != "" {
		t.Fatalf("清空未生效: %q", row.Branch)
	}
	// 离编行是历史：槽随行保留（拉回复职即恢复隔离），但不可再写。
	if _, err := s.Offboard("proj", "小猿"); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Get("proj", "小猿"); row.Branch != "" {
		t.Fatal("离编不该抹掉分支槽（拉回要恢复隔离）")
	}
	if _, err := s.SetBranch("proj", "小猿", "wt/x"); err == nil {
		t.Fatal("离编行不可写槽")
	}
}

// TestSetPausedRoundTrip pins 房间暂停的存储位：默认关、翻写回执带回
// 新值、不殃及其他行为位、重开回读仍在盘上（重启不复工烧 token）。
func TestSetPausedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staffing.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.SettingsOf("proj-pz").Paused {
		t.Fatal("房间暂停默认必须关")
	}
	s.SetAutoStock("proj-pz", true)
	if set := s.SetPaused("proj-pz", true); !set.Paused {
		t.Fatal("回执应带回暂停开启")
	}
	if set := s.SettingsOf("proj-pz"); !set.Paused || !set.AutoStock || !set.AutoRecall {
		t.Fatal("翻暂停未生效或殃及了其他行为位")
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.SettingsOf("proj-pz").Paused {
		t.Fatal("重开后暂停丢了（未落盘——重启会自作主张复工）")
	}
	if set := s2.SetPaused("proj-pz", false); set.Paused {
		t.Fatal("恢复回执应带回关")
	}
}

// TestSetPostAnchorContract（岗位锚）：SetPost 只写在编行——离编行的锚
// 留给召回路径（Offboard 保留、Join 复职随行）；显式清锚（空串）也是
// 合法写（重锚前的撤销手）。
func TestSetPostAnchorContract(t *testing.T) {
	s := openMem(t)
	if _, err := s.Join("book", "小笔", "写手", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPost("book", "小笔", "writer"); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Get("book", "小笔"); row.PostKey != "writer" {
		t.Fatalf("锚应落行，得 %+v", row)
	}
	if _, err := s.Offboard("book", "小笔"); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Get("book", "小笔"); row.PostKey != "writer" {
		t.Fatalf("离编应保留锚（补员对账认人），得 %+v", row)
	}
	if _, err := s.SetPost("book", "小笔", "other"); err == nil {
		t.Fatal("离编行的锚不可改（重新入编时携带新值）")
	}
	if _, err := s.Join("book", "小笔", "", ""); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.Get("book", "小笔"); row.PostKey != "writer" {
		t.Fatalf("复职应随行锚，得 %+v", row)
	}
	if _, err := s.SetPost("book", "小笔", ""); err != nil {
		t.Fatalf("清锚是合法写：%v", err)
	}
	if row, _ := s.Get("book", "小笔"); row.PostKey != "" {
		t.Fatalf("锚应已清，得 %+v", row)
	}
}
