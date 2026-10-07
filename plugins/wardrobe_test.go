package plugins

// wardrobe.go 的端到端：manifest → wardrobe.json → pixart 渲染查表面
// ＋staffing 掷骰池。键/色冲突逐条跳过、重放幂等、禁用的插件不应用。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/pixart"
	"github.com/WWestC/Niuma_Studio/staffing"
)

const wardrobePluginJSON = `{
	"id": "a.clothes", "name": "衣橱", "version": "0.1.0",
	"permissions": ["wardrobe"],
	"contributes": { "wardrobe": ["wardrobe.json"] }
}`

const wardrobeJSON = `{
	"hair_colors": [
		{ "key": "aurora", "name": "极光薄荷", "hex": "#7fe3c1", "weight": 3 },
		{ "key": "bad-hex", "name": "坏色", "hex": "#zzzzzz", "weight": 3 },
		{ "key": "dupe-black", "name": "撞黑", "hex": "#3b2f2f", "weight": 3 }
	],
	"hair_styles": [
		{ "key": "t-mohawk", "name": "莫西干", "fringe": 0, "sideburn": 0, "volume": 2, "back": 3, "weight": 1 },
		{ "key": "standard", "name": "撞基表", "fringe": 0, "sideburn": 0, "volume": 0, "back": 0, "weight": 1 },
		{ "key": "t-oob", "name": "轴超界", "fringe": 9, "sideburn": 0, "volume": 0, "back": 0, "weight": 1 }
	]
}`

func TestApplyWardrobeHappyPathAndSkips(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.clothes", wardrobePluginJSON, map[string]string{"wardrobe.json": wardrobeJSON})
	ps := s.Load()
	if len(ps) != 1 {
		t.Fatalf("扫描：%d", len(ps))
	}
	s.ApplyWardrobe(ps)
	p := ps[0]

	if pixart.HairStyleByKey("t-mohawk") == nil {
		t.Fatal("健康发型没进渲染查表面")
	}
	if !staffing.HairHexTaken("", "#7fe3c1") {
		t.Fatal("健康发色没进掷骰表")
	}
	// 三条坏记录逐条点名，且不影响好条目
	for _, want := range []string{"hex 需形如", "重复", "撞车", "轴参数超界"} {
		if !hasProblem(p.Problems, want) {
			t.Fatalf("缺 %q 的问题记录：%v", want, p.Problems)
		}
	}
	if pixart.HairStyleByKey("t-oob") != nil {
		t.Fatal("轴超界的发型不该进渲染面")
	}

	// 重放幂等：同一实例反复 Apply，贡献不丢也不炸（并表不翻倍由
	// staffing/lookaddon_test 的内部视角钉住）
	for i := 0; i < 3; i++ {
		s.ApplyWardrobe(s.Load())
	}
	if pixart.HairStyleByKey("t-mohawk") == nil || !staffing.HairHexTaken("", "#7fe3c1") {
		t.Fatal("重放后插件衣橱失踪")
	}
}

// 停用→重放该把贡献从两张并表里撤下：生产流是同一 Store 实例
// （GUI 进程里 /plugins.json 每次请求重放），diff 撤下来自实例内的
// applied 对账——跨实例没有对账真相，这里就按生产流测。
func TestApplyWardrobeRespectsDisabled(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.clothes", wardrobePluginJSON, map[string]string{"wardrobe.json": wardrobeJSON})
	s.ApplyWardrobe(s.Load())
	if pixart.HairStyleByKey("t-mohawk") == nil || !staffing.HairHexTaken("", "#7fe3c1") {
		t.Fatal("前置：启用态该先应用成功")
	}
	if err := s.SetEnabled("a.clothes", false); err != nil {
		t.Fatal(err)
	}
	s.ApplyWardrobe(s.Load())
	if pixart.HairStyleByKey("t-mohawk") != nil {
		t.Fatal("停用后发型该从渲染面撤下")
	}
	if staffing.HairHexTaken("", "#7fe3c1") {
		t.Fatal("停用后发色该从掷骰表撤下")
	}
}

func TestApplyWardrobeMissingFileRecordsProblem(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.clothes", wardrobePluginJSON, map[string]string{})
	// wardrobe.json 不存在；问题记在当次 Load 的插件条目上（重扫不
	// 带应用期问题——/plugins.json 每次都是新扫描新应用）
	ps := s.Load()
	s.ApplyWardrobe(ps)
	if len(ps) != 1 || !hasProblem(ps[0].Problems, "不可读") {
		t.Fatalf("缺文件该记问题：%v", ps[0].Problems)
	}
}

func TestApplyWardrobeRemovalWithdraws(t *testing.T) {
	s, root, _ := openTempStore(t)
	writePlugin(t, root, "a.clothes", wardrobePluginJSON, map[string]string{"wardrobe.json": wardrobeJSON})
	s.ApplyWardrobe(s.Load())
	if err := s.Remove("a.clothes"); err != nil {
		t.Fatal(err)
	}
	// 卸载后的重放把贡献撤下（目录没了 → Load 空 → 对账撤来源）
	s.ApplyWardrobe(s.Load())
	if pixart.HairStyleByKey("t-mohawk") != nil {
		t.Fatal("卸载后发型该从渲染面撤下")
	}
	if staffing.HairHexTaken("", "#7fe3c1") {
		t.Fatal("卸载后发色该从掷骰表撤下")
	}
}
