package pixart

// walkb_test.go — 走路帧 B 帧下装感知契约（r_13，t_168）：裙/短裤的
// B 帧不再被直筒腿顶替、全款渲染健壮、straight 兜底与现版逐字节一致、
// 变体优先于兜底。

import (
	"bytes"
	"testing"
)

// specOf 一个测试用 spec 捷径。
func specOf(bottom string) ComposeSpec {
	return ComposeSpec{Top: "tee", TopColor: "#7fb0d8", Hair: "#4a3b2a", Bottom: bottom}
}

// ① aline-mini 裙的 DownB：y17–23 区含裙色块（WalkB 的 P 行渲染为
// 下装色——非 pantsC 主导的直筒腿）。
func TestWalkBSkirtDownB(t *testing.T) {
	sp := specOf("aline-mini")
	sp.BottomClr = "#8c3b3b" // 裙色（酒红）
	set := ComposeAvatarSet(sp)
	want := C("#8c3b3b")
	n := 0
	for y := 17 * S; y < 24*S; y++ {
		for x := 0; x < AvatarW; x++ {
			c := set.DownB.NRGBAAt(x, y)
			if c.R == want.R && c.G == want.G && c.B == want.B {
				n++
			}
		}
	}
	if n < 40 { // WalkB 的 P 行至少 40 native px 渲染成裙色
		t.Fatalf("裙色块仅 %d px（<40）——B 帧仍被直筒腿顶替", n)
	}
	// t_169 验收补强（破坏性抽验抓的缺口）：裙款 WalkB 的迈步带
	//（y19–21）应露肤色（裙摆收窄成迈步形）——兜底 legsTogether 的
	// 同区是 P 分腿无肤色。删 WalkB 变体时旧断言不红（兜底也是裙色），
	// 这条是「变体在测」的真判据。
	skinN := 0
	for y := 19 * S; y < 22*S; y++ {
		for x := 0; x < AvatarW; x++ {
			c := set.DownB.NRGBAAt(x, y)
			if c.R == 240 && c.G == 200 && c.B == 160 { // 默认肤 #f0c8a0
				skinN++
			}
		}
	}
	if skinN < 12 {
		t.Fatalf("迈步带肤色仅 %d px（<12）——WalkB 变体缺席，B 帧走了兜底分腿", skinN)
	}
}

// ② shorts 的 RightB：短裤色与 A 帧同源（B 帧不是别的色）。
func TestWalkBShortsColor(t *testing.T) {
	sp := specOf("shorts")
	sp.BottomClr = "#33415c"
	set := ComposeAvatarSet(sp)
	want := C("#33415c")
	cntA, cntB := 0, 0
	for y := 17 * S; y < 24*S; y++ {
		for x := 0; x < AvatarW; x++ {
			c := set.RightA.NRGBAAt(x, y)
			if c.R == want.R && c.G == want.G && c.B == want.B {
				cntA++
			}
			c2 := set.RightB.NRGBAAt(x, y)
			if c2.R == want.R && c2.G == want.G && c2.B == want.B {
				cntB++
			}
		}
	}
	if cntB == 0 {
		t.Fatalf("B 帧无短裤色（A 帧 %d px）", cntA)
	}
}

// ③ 全 10 裙＋短裤＋全裤款 B 帧渲染不 panic 且尺寸合法。
func TestWalkBAllBottomsRender(t *testing.T) {
	type kc struct{ k, c string }
	all := []kc{}
	for _, b := range BottomStyles {
		all = append(all, kc{b.Key, "#2f343c"})
	}
	for _, s := range Skirts {
		all = append(all, kc{s.Key, "#8c3b3b"})
	}
	for _, e := range all {
		sp := specOf(e.k)
		sp.BottomClr = e.c
		set := ComposeAvatarSet(sp)
		frames := []*struct {
			img interface{ Bounds() interface{} }
		}{}
		_ = frames
		for _, f := range [][]byte{set.DownB.Pix, set.UpB.Pix, set.RightB.Pix, set.LeftB.Pix} {
			if len(f) != AvatarW*AvatarH*4 {
				t.Fatalf("%s B 帧尺寸非法: %d", e.k, len(f))
			}
		}
	}
}

// ④ straight 兜底 B 帧与旧输出逐字节相等（兼容铁律）。
func TestWalkBStraightFallbackBytes(t *testing.T) {
	sp := specOf("straight")
	withField := ComposeAvatarSet(sp)
	// 手工模拟旧路径：直接 frameB（无 WalkB 感知）
	set := &AvatarSet{}
	_ = set
	// 旧输出＝ComposeAvatar（bottom 缺省也是 straight 渲染）
	legacy := ComposeAvatar("#7fb0d8", "#4a3b2a")
	pairs := [][2][]byte{
		{withField.DownB.Pix, legacy.DownB.Pix},
		{withField.UpB.Pix, legacy.UpB.Pix},
		{withField.RightB.Pix, legacy.RightB.Pix},
		{withField.LeftB.Pix, legacy.LeftB.Pix},
	}
	for i, p := range pairs {
		if !bytes.Equal(p[0], p[1]) {
			t.Fatalf("straight B 帧 %d 与旧输出不一致（兼容铁律破坏）", i)
		}
	}
}

// ⑤ WalkB 变体优先于兜底：有变体的款 B 帧 ≠ legsTogether 形态
// （aline-mini 的 DownB 与 straight 的 DownB 不同）。
func TestWalkBVariantWins(t *testing.T) {
	skirt := ComposeAvatarSet(specOf("aline-mini"))
	straight := ComposeAvatarSet(specOf("straight"))
	if bytes.Equal(skirt.DownB.Pix, straight.DownB.Pix) {
		t.Fatal("有 WalkB 变体的款 B 帧落了 legsTogether 兜底——变体未生效")
	}
}
