package pixart

// t_128 数值自检：把「目视抽检」翻译成可断言的不变量。

import (
	"testing"
)

// 马尾纪律（§二 F6）：仅 Up/Right 可见，Down 不可见。
func TestPonytailInvisibleFromDown(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	std := ComposeAvatarSet(base)
	pn := base
	pn.HairStyle = "ponytail"
	pony := ComposeAvatarSet(pn)
	if !sameImage(std.DownA, pony.DownA) {
		t.Error("马尾改变了 Down 视图——应不可见")
	}
	if sameImage(std.UpA, pony.UpA) {
		t.Error("马尾在 Up 视图不可见——画丢了")
	}
	if sameImage(std.RightA, pony.RightA) {
		t.Error("马尾在 Right 视图不可见——画丢了")
	}
}

// 发型纪律：任意发型只改 y0–13（头＋背发带），躯干以下与 standard 一致。
func TestHairConfinedToHeadBand(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	std := ComposeAvatarSet(base)
	for _, h := range HairStyles {
		sp := base
		sp.HairStyle = h.Key
		set := ComposeAvatarSet(sp)
		if !sameImage(cropY(std.DownA, 14*S, 24*S), cropY(set.DownA, 14*S, 24*S)) {
			t.Errorf("发型 %s 污染了 y14 以下的躯干", h.Key)
		}
	}
}

// 52 款发型互不重复（同色同衣基线下 Down 视图两两不同）。
func TestHairStylesPairwiseDistinct(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	seen := map[string]string{} // pix hash → style key
	for _, h := range HairStyles {
		sp := base
		sp.HairStyle = h.Key
		set := ComposeAvatarSet(sp)
		sig := string(set.DownA.Pix) + "|" + string(set.UpA.Pix)
		if prev, dup := seen[sig]; dup {
			t.Errorf("发型 %s 与 %s 三视图渲染完全相同", h.Key, prev)
		}
		seen[sig] = h.Key
	}
}

// 10 版型上衣互不重复；同款不同色也应不同。
func TestTopsPairwiseDistinct(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	sigs := map[string]string{}
	for _, ts := range TopStyles {
		for _, c := range TopColors {
			sp := base
			sp.Top, sp.TopColor = ts.Key, c
			sig := string(ComposeAvatarSet(sp).DownA.Pix)
			if prev, dup := sigs[sig]; dup {
				t.Errorf("上衣 %s(%s) 与 %s 渲染相同", ts.Key, c, prev)
			}
			sigs[sig] = ts.Key + "(" + c + ")"
		}
	}
}

// 下装/裙互不重复。
func TestBottomsSkirtsDistinct(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	sigs := map[string]string{}
	for _, bs := range BottomStyles {
		for _, c := range BottomColors {
			sp := base
			sp.Bottom, sp.BottomClr = bs.Key, c
			sig := string(ComposeAvatarSet(sp).DownA.Pix)
			if prev, dup := sigs[sig]; dup {
				t.Errorf("下装 %s(%s) 与 %s 渲染相同", bs.Key, c, prev)
			}
			sigs[sig] = bs.Key + "(" + c + ")"
		}
	}
	for _, sk := range Skirts {
		sp := base
		sp.Bottom, sp.BottomClr = sk.Key, sk.Color
		sig := string(ComposeAvatarSet(sp).DownA.Pix)
		if prev, dup := sigs[sig]; dup {
			t.Errorf("裙 %s 与 %s 渲染相同", sk.Key, prev)
		}
		sigs[sig] = sk.Key
	}
}

// 52 件饰品按挂点落位：像素变化必须落在各自挂点包络内。
func TestAccessoryChangeInSlotEnvelope(t *testing.T) {
	// 挂点包络（down 视图，16×24 legacy 网格）
	env := map[string][4]int{
		SlotGlasses: {4, 4, 12, 8},
		SlotEarring: {2, 6, 14, 9},
		SlotHairOrn: {3, 0, 12, 3},
		SlotBadge:   {6, 10, 10, 16},
		SlotTie:     {6, 10, 10, 15},
		SlotScarf:   {4, 8, 12, 14},
	}
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	plain := ComposeAvatarSet(base)
	for _, a := range Accessories {
		sp := base
		sp.Acc = []string{a.Key}
		worn := ComposeAvatarSet(sp)
		e := env[a.Slot]
		for y := 0; y < 24*S; y++ {
			for x := 0; x < 16*S; x++ {
				i := plain.DownA.PixOffset(x, y)
				if plain.DownA.Pix[i] == worn.DownA.Pix[i] &&
					plain.DownA.Pix[i+1] == worn.DownA.Pix[i+1] &&
					plain.DownA.Pix[i+2] == worn.DownA.Pix[i+2] {
					continue
				}
				gx, gy := x/S, y/S
				if gx < e[0] || gy < e[1] || gx >= e[2] || gy >= e[3] {
					t.Errorf("%s: 像素变化越出挂点包络 (grid %d,%d)", a.Key, gx, gy)
				}
			}
		}
	}
}

// 挂点互斥：两件眼镜只画第一件（§三：每挂点至多 1 件）。
func TestOnePerSlot(t *testing.T) {
	base := ComposeSpec{Hair: "#3b2f2f", TopColor: "#3370ff"}
	sp := base
	sp.Acc = []string{"glasses-box-black"}
	one := ComposeAvatarSet(sp)
	sp.Acc = []string{"glasses-box-black", "glasses-round-gold"}
	two := ComposeAvatarSet(sp)
	if !sameImage(one.DownA, two.DownA) {
		t.Error("第二件同挂点饰品改变了渲染——互斥纪律失效")
	}
}
