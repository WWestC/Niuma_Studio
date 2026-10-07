package pixart

// addon.go 的注册面测试：查表并入、撞键让位、按来源整体替换（幂等）。
// 全局登记表用唯一键隔离各用例，收尾撤下自己的来源。

import "testing"

func TestRegisterHairStyleAddonsBasic(t *testing.T) {
	defs := []HairStyleDef{
		{Key: "t-mohawk", Name: "莫西干", Fringe: 0, Sideburn: 0, Volume: 2, Back: 3},
		{Key: "standard", Name: "撞基表", Fringe: 0, Sideburn: 0, Volume: 0, Back: 0}, // 基表已有 → 跳过
	}
	got := RegisterHairStyleAddons("test:basic", defs)
	defer RegisterHairStyleAddons("test:basic", nil)
	if len(got) != 1 || got[0] != "t-mohawk" {
		t.Fatalf("收下 %v，想留 [t-mohawk]", got)
	}
	if HairStyleByKey("t-mohawk") == nil {
		t.Fatal("插件发型查不到（HairStyleByKey 没并插件面）")
	}
	if st := HairStyleByKey("t-mohawk"); st.Name != "莫西干" || st.Back != BackSpiky {
		t.Fatalf("插件发型字段走样：%+v", st)
	}
}

func TestRegisterHairStyleAddonsReplaceIsIdempotent(t *testing.T) {
	first := []HairStyleDef{{Key: "t-a1", Name: "一版", Volume: 1}}
	if got := RegisterHairStyleAddons("test:replace", first); len(got) != 1 {
		t.Fatalf("首次登记收下 %v", got)
	}
	// 同来源换装：新键进来、旧键撤下——不是叠加
	second := []HairStyleDef{{Key: "t-a2", Name: "二版", Volume: 2}}
	if got := RegisterHairStyleAddons("test:replace", second); len(got) != 1 || got[0] != "t-a2" {
		t.Fatalf("换装收下 %v", got)
	}
	if HairStyleByKey("t-a1") != nil {
		t.Fatal("同来源旧条目应随换装撤下")
	}
	// 再换回一版也 OK（自己的旧键不挡自己）
	if got := RegisterHairStyleAddons("test:replace", first); len(got) != 1 || got[0] != "t-a1" {
		t.Fatalf("换回收下 %v", got)
	}
	RegisterHairStyleAddons("test:replace", nil)
	if HairStyleByKey("t-a1") != nil {
		t.Fatal("空切片应整体撤下该来源")
	}
}

func TestHairStyleKeyTakenSkipSelf(t *testing.T) {
	RegisterHairStyleAddons("test:taken", []HairStyleDef{{Key: "t-tk", Name: "占用"}})
	defer RegisterHairStyleAddons("test:taken", nil)
	if !HairStyleKeyTaken("", "t-tk") {
		t.Fatal("全局视角应视为占用")
	}
	if !HairStyleKeyTaken("test:other", "t-tk") {
		t.Fatal("他人视角应视为占用")
	}
	if HairStyleKeyTaken("test:taken", "t-tk") {
		t.Fatal("自己的旧键不该挡自己换装")
	}
	if !HairStyleKeyTaken("", "standard") {
		t.Fatal("基表键应视为占用")
	}
}
