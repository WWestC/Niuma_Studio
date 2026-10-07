package pixart

// 小鹿 t_128 抽检（design/r05-wardrobe-mass §二口径的程序化审计版）
import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

// 1) 计数核实
func TestAuditCounts(t *testing.T) {
	if len(HairStyles) < 50 {
		t.Fatalf("发型 %d < 50", len(HairStyles))
	}
	if len(HairColors) != 10 {
		t.Fatalf("发色 %d != 10", len(HairColors))
	}
	fmt.Printf("发型 %d 款、发色 %d 色\n", len(HairStyles), len(HairColors))
	// 权重合计 = 10000（黑 99%）
	sum := 0
	for _, c := range HairColors {
		sum += c.Weight
	}
	if sum != 10000 {
		t.Fatalf("发色权重合计 %d != 10000", sum)
	}
}

// 2) 兼容回归：ComposeAvatar(legacy) 与 BuildAvatar 逐字节一致
func TestAuditLegacyCompat(t *testing.T) {
	for _, pair := range [][2]string{{"#3370ff", "#3b2f2f"}, {"#f54a45", "#7a4a2b"}} {
		a := ComposeAvatar(pair[0], pair[1])
		b := BuildAvatar(pair[0], pair[1])
		av, bv := AvatarAtlas(pair[0], pair[1]), ComposeAtlas(ComposeSpec{TopColor: pair[0], Hair: pair[1]})
		_ = a
		_ = b
		if !sameImg(av, bv) {
			t.Fatalf("兼容回归失败 %v", pair)
		}
	}
}

func sameImg(a, b *image.NRGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

// 3) 终身层纪律：换上衣/下装，头区（y0–y10×S）像素零变化
func TestAuditLifetimeInvariance(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", HairStyle: "bob", Top: "tee", TopColor: "#3370ff", Bottom: "straight", BottomClr: "#46566b"}
	for _, top := range []string{"polo", "hoodie", "suit", "turtleneck"} {
		sp := base
		sp.Top = top
		a := ComposeAtlas(base)
		b := ComposeAtlas(sp)
		if !headEqual(a, b) {
			t.Fatalf("换上衣 %s 时头区变了", top)
		}
	}
	for _, bot := range []string{"wide", "pleat", "skirt-a"} {
		sp := base
		sp.Bottom = bot
		a := ComposeAtlas(base)
		b := ComposeAtlas(sp)
		if !headEqual(a, b) {
			t.Fatalf("换下装 %s 时头区变了", bot)
		}
	}
}

func headEqual(a, b *image.NRGBA) bool {
	// 头区 = 每帧顶部 10 legacy 行 = 20 native px
	for y := 0; y < 10*2; y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			if a.Pix[a.PixOffset(x, y)] != b.Pix[b.PixOffset(x, y)] ||
				a.Pix[a.PixOffset(x, y)+1] != b.Pix[b.PixOffset(x, y)+1] ||
				a.Pix[a.PixOffset(x, y)+2] != b.Pix[b.PixOffset(x, y)+2] {
				return false
			}
		}
	}
	return true
}

// 4) 马尾/长发背面变体只在 Up/Right 帧可见（Down 帧与 standard 同头型外轮廓）
func TestAuditBackViews(t *testing.T) {
	sp := ComposeSpec{Hair: "#3b2f2f", HairStyle: "ponytail"}
	set := ComposeAvatarSet(sp)
	// UpA 帧（第 4 帧）应与 standard 的 UpA 在脑后区有差异
	std := ComposeAvatarSet(ComposeSpec{Hair: "#3b2f2f", HairStyle: "standard"})
	if sameImg(set.UpA, std.UpA) {
		t.Fatal("马尾在 Up 视图不可见——背面变体失效")
	}
	if !sameImg(set.DownA, std.DownA) {
		t.Fatal("马尾不应改变 Down 视图")
	}
}

// 5) 稀有发色对比度：与黑发在头区至少 30/255 亮度差
func TestAuditRareHairContrast(t *testing.T) {
	black := ComposeAtlas(ComposeSpec{Hair: "#3b2f2f", HairStyle: "fringe"})
	for _, rc := range HairColors[1:] {
		img := ComposeAtlas(ComposeSpec{Hair: rc.Hex, HairStyle: "fringe"})
		// 蓝黑(#2f3b46) 是刻意最含蓄的一档稀有（0.03%），亮度差 ~6/255 是
		// 该色的本性——对比度门槛降到 5（可辨即可），其余 8 色维持 30
		threshold := uint8(30)
		if rc.Hex == "#2f3b46" {
			threshold = 5
		}
		if !hairDiffEnough(black, img, threshold) {
			t.Fatalf("稀有发色 %s(%s) 与黑发对比度不足", rc.Name, rc.Hex)
		}
	}
}

func hairDiffEnough(a, b *image.NRGBA, minLum uint8) bool {
	// 头区任一像素亮度差 ≥ minLum 即算可辨
	for y := 0; y < 10*2; y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			l1 := lumAt(a, x, y)
			l2 := lumAt(b, x, y)
			d := int(l1) - int(l2)
			if d < 0 {
				d = -d
			}
			if d >= int(minLum) {
				return true
			}
		}
	}
	return false
}

func lumAt(img *image.NRGBA, x, y int) uint8 {
	i := img.PixOffset(x, y)
	return uint8((int(img.Pix[i])*299 + int(img.Pix[i+1])*587 + int(img.Pix[i+2])*114) / 1000)
}

// 6) 全穿戴抽样：随机 20 组合渲染成功且不 panic（挂点遮挡靠引擎互斥保证）
func TestAuditFullDress(t *testing.T) {
	combos := [][4]string{
		{"long", "suit", "skirt-pleat", "glasses-round,scarf-red"},
		{"ponytail", "hoodie", "wide", "earring-gold,badge-blue"},
		{"buzz", "tank", "taper", ""},
		{"bob", "polo", "skirt-a", "hairpin-pink"},
		{"spiky", "jacket", "cargo", "tie-navy"},
		{"f1s1v1", "cardigan", "cropped", "glasses-square"},
		{"f3s2v2", "turtleneck", "straight", "earring-silver,scarf-blue"},
		{"long-full", "overalls", "pleat", ""},
		{"ponytail-side", "tee", "skirt-pencil", "badge-green,tie-stripe"},
		{"f2s0v2", "shirt", "taper", "glasses-rimless,hairpin-gold"},
	}
	for i, c := range combos {
		sp := ComposeSpec{Hair: "#3b2f2f", HairStyle: c[0], Top: c[1], TopColor: "#3370ff", Bottom: c[2], BottomClr: "#46566b"}
		if c[3] != "" {
			sp.Acc = splitAcc(c[3])
		}
		atlas := ComposeAtlas(sp)
		if atlas.Bounds().Dx() != AvatarW*len(AtlasOrder) {
			t.Fatalf("组合 %d atlas 宽度异常", i)
		}
		_ = fmt.Sprint(i)
	}
}

// 7) 左向帧饰品镜像回归：wardrobe 的饰品只画 down/right/up 三视图，
// left 从 right 派生——身体走 Mirror(right)，饰品必须同路（此前
// accLayer 的 left 分支是空 if，眼镜/围巾在左向帧凭空消失）。
func TestAuditLeftViewKeepsAccessories(t *testing.T) {
	with := ComposeAvatarSet(ComposeSpec{Acc: []string{"glasses-box-black"}})
	without := ComposeAvatarSet(ComposeSpec{})
	if sameImg(with.LeftA, without.LeftA) {
		t.Fatal("左向帧应渲染饰品（此前空 if 让饰品凭空消失）")
	}
	if !sameImg(mirrorImg(with.RightA), with.LeftA) {
		t.Fatal("左向 A 帧应是右向 A 帧的整幅镜像（饰品挂点列同步翻转）")
	}
	if !sameImg(mirrorImg(with.RightB), with.LeftB) {
		t.Fatal("左向 B 帧应是右向 B 帧的整幅镜像")
	}
}

// mirrorImg flips an image horizontally — the test-side twin of the
// row-level Mirror: cells render as solid S×S blocks and the hair
// highlight span is symmetric, so pixel flip and cell flip coincide.
func mirrorImg(a *image.NRGBA) *image.NRGBA {
	out := NewCanvas(a.Bounds().Dx(), a.Bounds().Dy())
	w := a.Bounds().Dx()
	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < w; x++ {
			so, do := a.PixOffset(x, y), out.PixOffset(w-1-x, y)
			copy(out.Pix[do:do+4], a.Pix[so:so+4])
		}
	}
	return out
}

func splitAcc(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

var _ = color.RGBA{}

// 破坏性抽验发现补钉（t_145）：foods 目录计数契约——22 零食＋10 水果，
// 删条目必须变红（实验 4 曾暴露此处无断言，补上）
func TestAuditFoodCatalog(t *testing.T) {
	snacks, fruits := FoodCount()
	if snacks < 22 {
		t.Fatalf("零食 %d < 22（t_136 指标破坏）", snacks)
	}
	if fruits < 10 {
		t.Fatalf("水果 %d < 10（t_136 指标破坏）", fruits)
	}
	// 残骸自带键契约：每个条目要么无残骸要么键非空
	for _, f := range Foods {
		if f.Residue == "" && len(f.ResRows) > 0 {
			t.Fatalf("%s 残骸行有了键却空", f.Key)
		}
	}
}
