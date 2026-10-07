package staffing

// lookaddon.go 的并表测试：掷骰池/发色表按来源整体替换、快照并入、
// HairHexTaken 的自他视角。权重语义（发色万分比、发型整数权重）由
// plugins 包的 wardrobe 测试端到端兜底，这里钉合并行为本身。

import "testing"

func TestStyleRollPoolMergesAndReplaces(t *testing.T) {
	RegisterLookAddons("test:style", nil, []HairStyleAddon{{Key: "t-s1", Weight: 1}})
	pool := styleRollPool()
	found := 0
	for _, p := range pool {
		if p.Key == "t-s1" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("插件发型应恰好并入一次，出现 %d 次", found)
	}
	// 同来源换装：旧键撤下、新键进来
	RegisterLookAddons("test:style", nil, []HairStyleAddon{{Key: "t-s2", Weight: 2}})
	pool = styleRollPool()
	for _, p := range pool {
		if p.Key == "t-s1" {
			t.Fatal("同来源旧条目应随换装撤下")
		}
	}
	seen2 := false
	for _, p := range pool {
		if p.Key == "t-s2" {
			seen2 = true
		}
	}
	if !seen2 {
		t.Fatal("新条目没并入掷骰池")
	}
	RegisterLookAddons("test:style", nil, nil) // 清场
}

func TestColorRollTableMerges(t *testing.T) {
	RegisterLookAddons("test:color", []HairColorAddon{{Name: "极光", Hex: "#7fe3c1", PerMyriad: 3}}, nil)
	defer RegisterLookAddons("test:color", nil, nil)
	table := colorRollTable()
	n := 0
	for _, c := range table {
		if c.Hex == "#7fe3c1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("插件发色应恰好并入一次，出现 %d 次", n)
	}
}

func TestHairHexTaken(t *testing.T) {
	RegisterLookAddons("test:hex", []HairColorAddon{{Name: "落日", Hex: "#e07850", PerMyriad: 2}}, nil)
	defer RegisterLookAddons("test:hex", nil, nil)
	if !HairHexTaken("", "#e07850") {
		t.Fatal("插件发色应视为占用")
	}
	if HairHexTaken("test:hex", "#e07850") {
		t.Fatal("自己的旧色不该挡自己换装")
	}
	if !HairHexTaken("", "#3b2f2f") {
		t.Fatal("基表黑色应视为占用")
	}
	if HairHexTaken("", "#123456") {
		t.Fatal("没人用过的色不该占用")
	}
}

func TestRollHairColorCanLandOnAddon(t *testing.T) {
	// 权重拉满到 100%：插件色必中——证明并表真的进了掷骰路径
	RegisterLookAddons("test:force", []HairColorAddon{{Name: "必中", Hex: "#abcdef", PerMyriad: 10000}}, nil)
	defer RegisterLookAddons("test:force", nil, nil)
	for i := 0; i < 8; i++ {
		if got := RollHairColor("probe"); got != "#abcdef" {
			t.Fatalf("万分之万权重没掷中插件色，得到 %s", got)
		}
	}
}
