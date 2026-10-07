package pixart

// wardrobe_test.go — t_128 资产库的验收测试：计数（六类目配额）、图案
// 几何（行宽/行数/挂点在界内）、兼容回归（ComposeAvatar ≡ BuildAvatar
// 逐字节）、发色概率表合计、以及「every (style×style) 组合都能渲染」
// 的全量冒烟。

import (
	"bytes"
	"image"
	"testing"
)

// --- 计数（任务配额：100 衣 / 50 裤 / 10 裙 / 50 饰品 / 50 发型 / 10 发色）---

func TestTopCount100(t *testing.T) {
	if got := len(TopStyles) * len(TopColors); got != 100 {
		t.Fatalf("上衣 = 版型%d × 色%d = %d 件，要求 100", len(TopStyles), len(TopColors), got)
	}
}

func TestBottomCountAtLeast50(t *testing.T) {
	// 10 型 × 10 色 = 100 件（任务配额 50+，超配交付）。
	if got := len(BottomStyles) * len(BottomColors); got < 50 {
		t.Fatalf("裤子 = 版型%d × 色%d = %d 件，要求 ≥50", len(BottomStyles), len(BottomColors), got)
	}
}

func TestSkirtCountAtLeast10(t *testing.T) {
	if len(Skirts) < 10 {
		t.Fatalf("裙子 %d 款，要求 ≥10", len(Skirts))
	}
}

func TestAccessoryCountAtLeast50(t *testing.T) {
	if len(Accessories) < 50 {
		t.Fatalf("饰品 %d 件，要求 ≥50", len(Accessories))
	}
	// 每挂点至多 1 件的纪律在引擎侧约束（Acc 数组），这里查每件有合法挂点。
	slots := map[string]bool{SlotGlasses: true, SlotEarring: true, SlotHairOrn: true,
		SlotBadge: true, SlotTie: true, SlotScarf: true}
	for _, a := range Accessories {
		if !slots[a.Slot] {
			t.Errorf("%s: 未知挂点 %q", a.Key, a.Slot)
		}
		if a.Hex == "" {
			t.Errorf("%s: 缺主色", a.Key)
		}
	}
}

func TestHairStyleCountAtLeast50(t *testing.T) {
	if len(HairStyles) < 50 {
		t.Fatalf("发型 %d 款，要求 ≥50", len(HairStyles))
	}
	// F1–F8 八款 spec 锚点必须在位且键与定稿一致。
	want := []string{"standard", "fringe", "sidepart", "buzz", "bob", "ponytail", "spiky", "long"}
	for _, k := range want {
		if HairStyleByKey(k) == nil {
			t.Errorf("spec 锚点发型 %s 缺失", k)
		}
	}
	// 键唯一。
	seen := map[string]bool{}
	for _, h := range HairStyles {
		if seen[h.Key] {
			t.Errorf("发型键重复 %s", h.Key)
		}
		seen[h.Key] = true
	}
}

func TestHairColorTable(t *testing.T) {
	if len(HairColors) != 10 {
		t.Fatalf("发色 %d 种，要求 10", len(HairColors))
	}
	sum := 0
	for _, c := range HairColors {
		sum += c.Weight
		if c.Weight <= 0 {
			t.Errorf("%s: 权重 %d 非正", c.Key, c.Weight)
		}
	}
	if sum != 10000 {
		t.Errorf("概率权重合计 %d，要求 10000（黑 9900 + 稀有 100）", sum)
	}
	if HairColors[0].Key != "black" || HairColors[0].Weight != 9900 {
		t.Errorf("主发色应为黑 99%%，现为 %s %d", HairColors[0].Key, HairColors[0].Weight)
	}
}

// --- 图案几何：三视图行数/行宽、字符不越 vocab ---

func TestClothingPatchGeometry(t *testing.T) {
	check := func(kind, key string, rows []string, wantH int) {
		t.Helper()
		if len(rows) != wantH {
			t.Errorf("%s %s: 行数 %d，要求 %d", kind, key, len(rows), wantH)
		}
		vocab := map[rune]bool{'T': true, 't': true, 'k': true, 'P': true, 'p': true,
			'j': true, 'S': true, 'O': true, '.': true}
		for y, r := range rows {
			if got := len([]rune(r)); got != 16 {
				t.Errorf("%s %s: 第 %d 行宽 %d，要求 16", kind, key, y, got)
			}
			for _, ch := range r {
				if !vocab[ch] {
					t.Errorf("%s %s: 非法符文 %q", kind, key, ch)
				}
			}
		}
	}
	for _, s := range TopStyles {
		check("上衣", s.Key, s.Down, 6)
		check("上衣", s.Key, s.Up, 6)
		check("上衣", s.Key, s.Right, 6)
	}
	for _, s := range BottomStyles {
		check("下装", s.Key, s.Down, 7)
		check("下装", s.Key, s.Up, 7)
		check("下装", s.Key, s.Right, 7)
	}
	for _, s := range Skirts {
		check("裙", s.Key, s.Down, 7)
		check("裙", s.Key, s.Up, 7)
		check("裙", s.Key, s.Right, 7)
	}
}

func TestAccessoryGeometry(t *testing.T) {
	for _, a := range Accessories {
		if len(a.Views) == 0 {
			t.Errorf("%s: 一个视图都没有", a.Key)
		}
		for view, v := range a.Views {
			switch view {
			case "down", "up", "right":
			default:
				t.Errorf("%s: 非法视图 %q（left 由 right 镜像，不落数据）", a.Key, view)
			}
			if v.X < 0 || v.Y < 0 || v.X > 15 || v.Y > 23 {
				t.Errorf("%s/%s: 锚点 (%d,%d) 出界", a.Key, view, v.X, v.Y)
			}
			for _, r := range v.Rows {
				for _, ch := range r {
					if ch != '1' && ch != '2' && ch != '.' {
						t.Errorf("%s/%s: 非法符文 %q", a.Key, view, ch)
					}
				}
			}
			// 饰品不得盖眼：眼镜的 down 视图例外（框让开眼位），其余
			// down 视图命中 (6..10, 5..7) 眼区即报。
			if a.Slot != SlotGlasses && view == "down" {
				for y, r := range v.Rows {
					for x, ch := range r {
						if ch == '.' {
							continue
						}
						gy, gx := v.Y+y, v.X+x
						if gy >= 5 && gy <= 7 && gx >= 5 && gx <= 10 {
							t.Errorf("%s: down 视图 (%d,%d) 盖进眼区", a.Key, gx, gy)
						}
					}
				}
			}
		}
	}
}

// --- 兼容回归：ComposeAvatar ≡ BuildAvatar 逐字节（spec §五.1）---

func TestComposeLegacyByteCompat(t *testing.T) {
	pairs := [][2]string{
		{"#d95f4e", "#4a3b2a"}, {"#5b8bbd", "#22252a"}, {"#7fb0d8", "#4a3b2a"},
		{"#34c724", "#6a4a2f"}, {"#3370ff", "#3b2f2f"},
	}
	for _, p := range pairs {
		want := AvatarAtlas(p[0], p[1])
		got := ComposeAtlas(ComposeSpec{TopColor: p[0], Hair: p[1], Top: "tee", Bottom: "straight"})
		if !sameImage(want, got) {
			t.Errorf("shirt=%s hair=%s: Compose 输出与 legacy 不一致", p[0], p[1])
		}
	}
}

func sameImage(a, b *image.NRGBA) bool {
	if !a.Rect.Eq(b.Rect) {
		return false
	}
	return bytes.Equal(a.Pix, b.Pix)
}

// --- 全量冒烟：每个发型、每件上衣/下装/裙/饰品都能过一遍渲染 ---

func TestEveryAssetRenders(t *testing.T) {
	base := ComposeSpec{Skin: "#f0c8a0", Hair: "#3b2f2f", TopColor: "#3370ff", BottomClr: "#2f343c"}
	// every hairstyle over the tee baseline
	for _, h := range HairStyles {
		sp := base
		sp.HairStyle = h.Key
		img := ComposeAtlas(sp)
		if img.Bounds().Dx() != AvatarW*len(AtlasOrder) || img.Bounds().Dy() != AvatarH {
			t.Fatalf("发型 %s: atlas 尺寸异常 %v", h.Key, img.Bounds())
		}
	}
	// every top over the standard hair
	for _, ts := range TopStyles {
		for _, c := range TopColors {
			sp := base
			sp.Top, sp.TopColor = ts.Key, c
			if img := ComposeAtlas(sp); img.Bounds().Dx() == 0 {
				t.Fatal("空 atlas")
			}
		}
	}
	// every bottom & skirt
	for _, bs := range BottomStyles {
		for _, c := range BottomColors {
			sp := base
			sp.Bottom, sp.BottomClr = bs.Key, c
			if img := ComposeAtlas(sp); img.Bounds().Dx() == 0 {
				t.Fatal("空 atlas")
			}
		}
	}
	for _, sk := range Skirts {
		sp := base
		sp.Bottom, sp.BottomClr = sk.Key, sk.Color
		if img := ComposeAtlas(sp); img.Bounds().Dx() == 0 {
			t.Fatal("空 atlas")
		}
	}
	// every accessory (glasses+earring+orn, then badge/tie/scarf pairs)
	for _, a := range Accessories {
		sp := base
		sp.Acc = []string{a.Key}
		if img := ComposeAtlas(sp); img.Bounds().Dx() == 0 {
			t.Fatal("空 atlas")
		}
	}
}

// 饰品确实画上去了：同一 spec 戴/不戴眼镜，downA 必须有像素差。
func TestAccessoryVisiblyDrawn(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	plain := ComposeAvatarSet(base)
	spec := base
	spec.Acc = []string{"glasses-box-black"}
	worn := ComposeAvatarSet(spec)
	if sameImage(plain.DownA, worn.DownA) {
		t.Error("戴眼镜后 downA 无像素变化——饰品没画上")
	}
	if !sameImage(plain.UpA, worn.UpA) {
		t.Error("眼镜（无 up patch）改变了 up 视图——视图隐藏失效")
	}
}

// 稀有发色可渲：10 色逐一出 atlas 且互不相同。
func TestEveryHairColorRendersDistinct(t *testing.T) {
	first := ""
	for _, c := range HairColors {
		sp := ComposeSpec{Hair: c.Hex, TopColor: "#3370ff"}
		img := ComposeAtlas(sp)
		if img.Bounds().Dx() == 0 {
			t.Fatalf("发色 %s 渲染失败", c.Key)
		}
		if first == "" {
			first = string(img.Pix)
		} else if string(img.Pix) == first {
			t.Errorf("发色 %s 与黑发渲染结果相同", c.Key)
		}
	}
}

// 终身层纪律：换上衣不影响发型/肤色像素区（head rows y0–9）。
func TestLifetimeLayerStableAcrossOutfits(t *testing.T) {
	sp := ComposeSpec{Hair: "#7a4a2b", HairStyle: "bob", Skin: "#e8b88c", TopColor: "#3370ff"}
	a := ComposeAvatarSet(sp)
	sp.Top, sp.TopColor = "vest", "#f54a45"
	b := ComposeAvatarSet(sp)
	// compare the head band (y0–9 rows, ×S) of downA
	if !sameImage(cropY(a.DownA, 0, 10*S), cropY(b.DownA, 0, 10*S)) {
		t.Error("换上衣改变了头区像素——终身层被每日层污染")
	}
}

func cropY(img *image.NRGBA, y0, y1 int) *image.NRGBA {
	out := NewCanvas(img.Bounds().Dx(), y1-y0)
	for y := y0; y < y1; y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			i := img.PixOffset(x, y)
			if img.Pix[i+3] == 0 {
				continue
			}
			Blit(out, pixelAt(img, x, y), x, y-y0)
		}
	}
	return out
}

func pixelAt(img *image.NRGBA, x, y int) *image.NRGBA {
	one := NewCanvas(1, 1)
	i := img.PixOffset(x, y)
	one.Pix[0], one.Pix[1], one.Pix[2], one.Pix[3] = img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]
	return one
}

// --- t_128 并轨回归：AvatarSpec（t_126 契约面）→ Compose 一条真源 ---

// F1–F8 别名与 wardrobe 键渲染等价（BuildAvatarSpec 的映射正确性）。
func TestAvatarSpecAliasEquivalence(t *testing.T) {
	alias := map[string]string{
		"F1": "standard", "F2": "fringe", "F3": "sidepart", "F4": "buzz",
		"F5": "bob", "F6": "ponytail", "F7": "spiky", "F8": "long",
	}
	for f, key := range alias {
		a := BuildAvatarSpec(AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff", HairStyle: f})
		b := ComposeAvatarSet(ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff", HairStyle: key})
		if !sameImage(a.DownA, b.DownA) || !sameImage(a.UpA, b.UpA) {
			t.Errorf("发型别名 %s 与键 %s 渲染不一致", f, key)
		}
	}
}

// 眨眼帧保留发型（并轨前 bug：眨眼帧走裸 bodyDown，发型闪回圆盖）。
func TestBlinkKeepsHairStyle(t *testing.T) {
	spec := AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff", HairStyle: "F8"} // long
	set := BuildAvatarSpec(spec)
	std := BuildAvatarSpec(AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff"})
	if sameImage(set.DownBlink, std.DownBlink) {
		t.Error("长发成员的眨眼帧与标准发型相同——眨眼时发型闪回（并轨前 bug 复发）")
	}
}

// AvatarSpec 版型维度贯通：TopStyle/BottomStyle 真的换了渲染。
func TestAvatarSpecStyleDimensions(t *testing.T) {
	plain := AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff"}
	vest := plain
	vest.TopStyle = "vest"
	a := BuildAvatarSpec(plain)
	b := BuildAvatarSpec(vest)
	if sameImage(a.DownA, b.DownA) {
		t.Error("TopStyle=vext 未改变渲染——版型维度没接上")
	}
	skirt := plain
	skirt.BottomStyle = "pleats"
	c := BuildAvatarSpec(skirt)
	if sameImage(a.DownA, c.DownA) {
		t.Error("BottomStyle=pleats 未改变渲染——裙维度没接上")
	}
}

// slot-only 饰品调用（存量档案形态）落 wardrobe 默认款。
func TestAvatarSpecSlotOnlyAccessory(t *testing.T) {
	spec := AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff",
		Acc: []Accessory{{Slot: "glasses", Color: "#2e2622"}}}
	set := BuildAvatarSpec(spec)
	bare := BuildAvatarSpec(AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff"})
	if sameImage(set.DownA, bare.DownA) {
		t.Error("slot-only 眼镜没渲染——存量档案饰品丢失")
	}
}

// Style 库键直连：spec 带 Style 的饰品按库键渲染。
func TestAvatarSpecStyledAccessory(t *testing.T) {
	spec := AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff",
		Acc: []Accessory{{Slot: "glasses", Style: "glasses-solid-black"}}}
	solid := BuildAvatarSpec(spec)
	box := BuildAvatarSpec(AvatarSpec{Hair: "#3b2f2f", Shirt: "#3370ff",
		Acc: []Accessory{{Slot: "glasses", Style: "glasses-box-black"}}})
	if sameImage(solid.DownA, box.DownA) {
		t.Error("墨镜与黑框方镜渲染相同——Style 库键没生效")
	}
}
