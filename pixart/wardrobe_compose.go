package pixart

// wardrobe_compose.go — t_128 资产库的程序化渲染面：把 wardrobe.go 的
// 数据表合成为 AvatarSet/Atlas。分层序按 kb design/r05-avatar §四：
// body 基线 → 发型变体（H 区）→ 上衣（T 区 y10–15）→ 下装（y17–23）→
// 饰品（挂点 Blit）→ 9 帧 atlas。
//
// 兼容铁律（§五.1）：ComposeSpec{} 零值 ＋ standard 发型 ＋ tee/straight
// 下装＝与 legacy BuildAvatar(shirt, hair) 逐字节相同——老 URL、老成员
// 零迁移。ComposeAvatar(shirt, hair) 是便捷入口。

import (
	"image"
	"image/color"
	"strings"
)

// ComposeSpec is one member's full look (spec §四 AvatarSpec). Zero
// values all mean "legacy default" so an empty spec composes the legacy
// avatar byte-for-byte.
type ComposeSpec struct {
	Skin      string   // 终身：肤色 hex（""=legacy skinC）
	Hair      string   // 终身：发色 hex（""=legacy hairPalette 落的值）
	HairStyle string   // 终身：发型 key（""/unknown=standard）
	Top       string   // 每日：上衣版型 key（""=tee）
	TopColor  string   // 每日：上衣 hex（""=legacy 传参）
	Bottom    string   // 每日：下装版型 key（""=straight；裙 key 同用此字段）
	BottomClr string   // 每日：下装 hex（""=legacy pantsC）
	Acc       []string // 终身：饰品 key 0–2 件（多余忽略）
}

// ---------------------------------------------------------------------------
// 发型派生（§二：三轴全部作用在基线 H 区上）
// ---------------------------------------------------------------------------

// hairRows rewrites the head rows of one base view per the style's axis
// parameters. Returns rows y0–13 (head + the shirt band long hair and
// ponytails hang into); rows 10–13 are seeded from the view's own body
// rows so a no-op style leaves them untouched. The caller splices the
// result over the body's rows 0–13.
func hairRows(view string, st *HairStyleDef) []string {
	// base rows per view: head y0–9 + shirt-band seeds y10–13 (mirrors
	// bodyDown/Up/Right — the down face starts at y3, the up head is
	// solid hair)
	var base []string
	switch view {
	case "right":
		base = []string{
			"....HHHHHH......",
			"...HHHHHHHH.....",
			"..HHHHHHHHHH....",
			"..HHHSSSSSS.....",
			"..HHHSSSSSS.....",
			"..HHSSSSESS.....",
			"..HHSSSSESS.....",
			"..HHSSSSSSS.....",
			"...HSSSSSSS.....",
			"....SSSSSS......",
			"....TTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
		}
	case "up":
		base = []string{
			"......HHHH......",
			"....HHHHHHHH....",
			"...HHHHHHHHHH...",
			"...HHHHHHHHHH...",
			"..HHHHHHHHHHHH..",
			"..HHHHHHHHHHHH..",
			"..HHHHHHHHHHHH..",
			"..HHHHHHHHHHHH..",
			"...HHHHHHHHHH...",
			".....SSSSSS.....",
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
		}
	default: // down — the face (S) starts at y3
		base = []string{
			"......HHHH......",
			"....HHHHHHHH....",
			"...HHHHHHHHHH...",
			"...HHSSSSSSHH...",
			"..HHSSSSSSSSHH..",
			"..HSSSESSSESSH..",
			"..HSSSESSSESSH..",
			"..HSSSSSSSSSSH..",
			"...SSSSSSSSSS...",
			".....SSSSSS.....",
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
		}
	}
	if st == nil {
		return base
	}
	rows := make([]string, len(base))
	for i, r := range base {
		rows[i] = r
	}
	set := func(y, x0, x1 int, ch rune) {
		rs := []rune(rows[y])
		for x := x0; x < x1 && x < len(rs); x++ {
			rs[x] = ch
		}
		rows[y] = string(rs)
	}
	// Fringe — only the camera-facing view has a forehead to cover (the
	// down face's S span at y4 is cols 4–11).
	if view == "down" {
		switch st.Fringe {
		case FringeStraight: // 留海下沉 1px：额头 y4 盖发色
			set(4, 4, 12, 'H')
		case FringeSide: // 左侧分缝：顶左 2px 换肤色分缝线
			set(1, 4, 6, 'S')
			set(2, 3, 5, 'S')
		case FringeZig: // 曲齿留海：y4 隔齿
			set(4, 4, 6, 'H')
			set(4, 8, 10, 'H')
		}
	}
	// Sideburn — 鬓角长度：标准＝基线（y3–4 侧沿）；长鬓延到 y6–9；
	// 及肩垂到 y9。
	switch st.Sideburn {
	case SideLong:
		if view == "right" {
			set(8, 2, 4, 'H')
			set(9, 2, 4, 'H')
		} else {
			set(5, 2, 4, 'H')
			set(6, 2, 4, 'H')
			set(5, 12, 14, 'H')
			set(6, 12, 14, 'H')
		}
	case SideShoulder:
		if view == "right" {
			for y := 5; y <= 9; y++ {
				set(y, 2, 4, 'H')
			}
		} else {
			for y := 5; y <= 9; y++ {
				set(y, 2, 4, 'H')
				set(y, 12, 14, 'H')
			}
		}
	}
	// Volume — 头顶蓬度：平顶削 y0 顶行；蓬松 y0/y1 加宽。
	switch st.Volume {
	case VolFlat:
		set(0, 6, 10, '.')
		if view == "right" {
			set(0, 4, 10, '.')
		}
	case VolFull:
		if view == "right" {
			set(0, 3, 5, 'H')
			set(1, 2, 3, 'H')
		} else {
			set(0, 5, 6, 'H')
			set(0, 10, 11, 'H')
			set(1, 3, 4, 'H')
			set(1, 12, 13, 'H')
		}
	}
	// Back — 背面变体，只改 away/side 视图；长发/马尾垂进 y10–13 背区。
	switch st.Back {
	case BackPonytail: // 脑后垂 3×5 马尾：Up 居中，Right 贴脑后西侧
		if view == "up" {
			for y := 8; y <= 10; y++ {
				set(y, 7, 10, 'H')
			}
		} else if view == "right" {
			for y := 5; y <= 9; y++ {
				set(y, 1, 3, 'H')
			}
		}
	case BackLong: // 两侧垂至肩 y9–13：三种视图都加背发
		if view == "up" {
			for y := 9; y <= 13; y++ {
				set(y, 3, 6, 'H')
				set(y, 10, 13, 'H')
			}
		} else if view == "right" {
			for y := 9; y <= 13; y++ {
				set(y, 2, 5, 'H')
			}
		} else {
			for y := 9; y <= 13; y++ {
				set(y, 2, 5, 'H')
				set(y, 11, 14, 'H')
			}
		}
	case BackSpiky: // 顶部 1px 尖刺：冠沿锯齿状外扩
		if view == "right" {
			set(0, 3, 4, 'H')
			set(0, 6, 7, 'H')
			set(0, 9, 10, 'H')
		} else {
			set(0, 5, 6, 'H')
			set(0, 9, 11, 'H')
		}
	case BackBuzz: // 削薄：H 区上半换暗一号发色 h（轮廓内收）
		for y := 0; y <= 3; y++ {
			rs := []rune(rows[y])
			for x, ch := range rs {
				if ch == 'H' {
					rs[x] = 'h'
				}
			}
			rows[y] = string(rs)
		}
	}
	return rows
}

// ---------------------------------------------------------------------------
// 分层合成
// ---------------------------------------------------------------------------

// patchRows returns the view's clothing patch for a style: the style's
// own rows, or the legacy base patch when style is nil/unknown.
func patchRows(view string, styleRows []string) []string {
	if len(styleRows) == 0 {
		switch view {
		case "right":
			return legacyRightPatch
		default:
			return legacyDownPatch
		}
	}
	return styleRows
}

// legacy clothing patches (avatar.go base y10–15 / y17–23), used whenever
// a spec omits a style — the byte-compat fallback.
var legacyDownPatch = []string{
	"...TTTTTTTTTT...",
	"..TTTTTTTTTTTT..",
	"..TTTTTTTTTTTT..",
	"..TTTTTTTTTTTT..",
	".SSTTTTTTTTTTSS.",
	".SSTTTTTTTTTTSS.",
}
var legacyRightPatch = []string{
	"....TTTTTTT.....",
	"...TTTTTTTT.....",
	"...TTTTTTTT.....",
	"...TTTTTTTT.....",
	"...STTTTTTS.....",
	"...STTTTTTS.....",
}
var legacyLegsDown = []string{
	"...PPPPPPPPPP...",
	"...PPPP..PPPP...",
	"...PPP....PPP...",
	"...PPP....PPP...",
	"...PPP....PPP...",
	"..OOOO....OOOO..",
	"..OOOO....OOOO..",
}
var legacyLegsRight = []string{
	"....PPPPPPP.....",
	"....PPP.PPP.....",
	"....PP..PPP.....",
	"....PP..PPP.....",
	"....PP..PPP.....",
	"...OOOO.OOOO....",
	"...OOOO.OOOO....",
}

// darken lowers each channel — the t/p/j/k accent tier (darker same-hue).
func darken(c color.RGBA) color.RGBA {
	return color.RGBA{
		R: uint8(float64(c.R) * 0.72), G: uint8(float64(c.G) * 0.72),
		B: uint8(float64(c.B) * 0.72), A: c.A,
	}
}

// ComposePalette assembles the render palette for one spec.
func ComposePalette(sp ComposeSpec) Palette {
	skin := skinC
	if sp.Skin != "" {
		skin = C(sp.Skin)
	}
	hair := C("#4a3b2a")
	if sp.Hair != "" {
		hair = C(sp.Hair)
	}
	top := C("#7fb0d8")
	if sp.TopColor != "" {
		top = C(sp.TopColor)
	}
	bottom := pantsC
	if sp.BottomClr != "" {
		bottom = C(sp.BottomClr)
	}
	return Palette{
		'S': skin, 'E': eyeC, 'e': C("#8a6a52"),
		'T': top, 't': darken(top),
		'P': bottom, 'p': darken(bottom),
		'O': shoeC, 'H': hair, 'h': darken(hair),
		'k': C("#e8e4de"), 'j': C("#e8e4de"),
	}
}

// composeView renders one view's full charmap: base body with the hair
// block rewritten per style, clothing patches swapped in.
func composeView(view string, sp ComposeSpec) []string {
	st := HairStyleByKey(sp.HairStyle)
	if st == nil {
		st = HairStyleByKey("standard")
	}
	head := hairRows(view, st)

	var body []string
	switch view {
	case "right":
		body = append([]string(nil), bodyRight...)
	default:
		body = append([]string(nil), bodyDown...)
		if view == "up" {
			body = append([]string(nil), bodyUp...)
		}
	}
	// splice the derived head (y0–13: head block + back-hair band) over
	// the body's rows 0–13
	for y := 0; y < 14 && y < len(head); y++ {
		body[y] = head[y]
	}

	// clothing: pick the style defs (nil-safe fallbacks)
	var ts *TopStyleDef
	for i := range TopStyles {
		if TopStyles[i].Key == sp.Top {
			ts = &TopStyles[i]
			break
		}
	}
	var bs *BottomStyleDef
	for i := range BottomStyles {
		if BottomStyles[i].Key == sp.Bottom {
			bs = &BottomStyles[i]
			break
		}
		for i := range Skirts {
			if Skirts[i].Key == sp.Bottom {
				bs = &Skirts[i].BottomStyleDef
				break
			}
		}
	}

	var topPatch, botPatch []string
	if view == "right" {
		topPatch = legacyRightPatch
		botPatch = legacyLegsRight
		if ts != nil {
			topPatch = ts.Right
		}
		if bs != nil {
			botPatch = bs.Right
		}
	} else {
		topPatch = legacyDownPatch
		botPatch = legacyLegsDown
		if ts != nil {
			if view == "up" && len(ts.Up) > 0 {
				topPatch = ts.Up
			} else {
				topPatch = ts.Down
			}
		}
		if bs != nil {
			if view == "up" && len(bs.Up) > 0 {
				botPatch = bs.Up
			} else {
				botPatch = bs.Down
			}
		}
	}
	for y := 0; y < 6 && y < len(topPatch); y++ {
		body[10+y] = topPatch[y]
	}
	for y := 0; y < 7 && y < len(botPatch); y++ {
		body[17+y] = botPatch[y]
	}
	return body
}

// ComposeAvatarSet renders the full 9-frame set for a spec (the layered
// successor of BuildAvatar).
func ComposeAvatarSet(sp ComposeSpec) *AvatarSet {
	pal := ComposePalette(sp)
	// 每挂点至多 1 件（§三挂点纪律）：后到的同挂点饰品被忽略。
	accs := make([]*AccessoryDef, 0, 2)
	slotTaken := map[string]bool{}
	for _, k := range sp.Acc {
		a := AccessoryByKey(k)
		if a == nil || slotTaken[a.Slot] {
			continue
		}
		slotTaken[a.Slot] = true
		accs = append(accs, a)
	}
	accPal := func(a *AccessoryDef) Palette {
		p := Palette{'1': C(a.Hex)}
		if a.Hex2 != "" {
			p['2'] = C(a.Hex2)
		}
		return p
	}
	// accLayer renders the accessory stack for one view as a transparent
	// sprite-grid image (16×24 cells, ×S), for Blit over the body.
	accLayer := func(view string) *image.NRGBA {
		img := NewCanvas(16*S, 24*S)
		for _, a := range accs {
			if v, ok := a.Views[view]; ok {
				Blit(img, Render(v.Rows, accPal(a)), v.X*S, v.Y*S)
				continue
			}
			// up 无补丁即隐（Views 无键，直落 continue）。left 也没有
			// 专属视图——饰品跟身体走同一条镜像路：right 补丁逐行翻字，
			// 挂点列翻到 16-X-w，与身体 Mirror(right) 的逐格翻转配对
			// （此前的空 if 让左向帧的饰品凭空消失）。
			if view != "left" {
				continue
			}
			v, ok := a.Views["right"]
			if !ok {
				continue
			}
			w := 0
			for _, r := range v.Rows {
				if n := len([]rune(r)); n > w {
					w = n
				}
			}
			Blit(img, Render(mirrorPatch(v.Rows, w), accPal(a)), (16-v.X-w)*S, v.Y*S)
		}
		return img
	}

	blink := func(rows []string) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = strings.ReplaceAll(r, "E", "e")
		}
		return out
	}
	over := func(view string, rows []string) *image.NRGBA {
		img := Render(rows, pal)
		if lay := accLayer(view); lay != nil {
			Blit(img, lay, 0, 0)
		}
		return img
	}

	down := composeView("down", sp)
	up := composeView("up", sp)
	right := composeView("right", sp)
	// t_167：B 帧下装感知——下装款的 WalkB 变体替换腿块（nil＝
	// legsTogether 兜底，straight 与空 Bottom 逐字节同现版）
	frameBS := func(body []string, view string) []string {
		if w := walkBOf(sp); w != nil {
			out := append([]string(nil), body[:len(body)-7]...)
			return append(out, w...)
		}
		return frameB(body, view)
	}
	return &AvatarSet{
		DownA:      over("down", down),
		DownB:      over("down", frameBS(down, "down")),
		DownBlink:  over("down", blink(down)),
		UpA:        over("up", up),
		UpB:        over("up", frameBS(up, "up")),
		RightA:     over("right", right),
		RightB:     over("right", frameBS(right, "right")),
		RightBlink: over("right", blink(right)),
		LeftA:      over("left", Mirror(right)),
		LeftB:      over("left", Mirror(frameBS(right, "right"))),
	}
}

// mirrorPatch flips an accessory patch cell-wise: ragged rows are
// padded to w first so a short row's cells land where a full mirror
// puts them (Mirror's bare reversal assumes uniform-width rows). The
// padding spaces render transparent — no palette entry.
func mirrorPatch(rows []string, w int) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		pad := make([]rune, w)
		for j := range pad {
			pad[j] = ' '
		}
		copy(pad, []rune(r))
		for l, rr := 0, w-1; l < rr; l, rr = l+1, rr-1 {
			pad[l], pad[rr] = pad[rr], pad[l]
		}
		out[i] = string(pad)
	}
	return out
}

// walkBOf resolves the spec's B-frame leg variant: the named bottom
// style's WalkB, else the skirt's (SkirtDef embeds BottomStyleDef so
// one lookup covers both), else nil (straight 兜底).
func walkBOf(sp ComposeSpec) []string {
	key := sp.Bottom
	if key == "" {
		key = "straight"
	}
	for i := range BottomStyles {
		if BottomStyles[i].Key == key {
			return BottomStyles[i].WalkB
		}
	}
	for i := range Skirts {
		if Skirts[i].Key == key {
			return Skirts[i].WalkB
		}
	}
	return nil
}

// ComposeAvatar is the legacy-signature shortcut: shirt+hair only, all
// else legacy defaults — guaranteed byte-identical to BuildAvatar.
func ComposeAvatar(shirtHex, hairHex string) *AvatarSet {
	return ComposeAvatarSet(ComposeSpec{
		TopColor: shirtHex, Hair: hairHex,
		Top: "tee", Bottom: "straight",
	})
}

// ComposeAtlas lays a spec's frames in the wire's AtlasOrder — the
// layered successor of AvatarAtlas (same frame order, same size).
func ComposeAtlas(sp ComposeSpec) *image.NRGBA {
	set := ComposeAvatarSet(sp)
	frames := map[string]*image.NRGBA{
		"downA": set.DownA, "downB": set.DownB, "downBlink": set.DownBlink,
		"upA": set.UpA, "upB": set.UpB,
		"leftA": set.LeftA, "leftB": set.LeftB,
		"rightA": set.RightA, "rightB": set.RightB,
	}
	img := NewCanvas(AvatarW*len(AtlasOrder), AvatarH)
	for i, key := range AtlasOrder {
		Blit(img, frames[key], i*AvatarW, 0)
	}
	return img
}
