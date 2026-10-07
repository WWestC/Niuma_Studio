package pixart

import (
	"image"
)

// AvatarSet holds the pre-rendered frames of one member's character.
type AvatarSet struct {
	DownA, DownB, DownBlink *image.NRGBA // facing camera
	UpA, UpB                *image.NRGBA // facing away
	RightA, RightB          *image.NRGBA
	LeftA, LeftB            *image.NRGBA
	RightBlink              *image.NRGBA
}

// AvatarSize is the sprite footprint (16 wide, 24 tall, ×S rendered).
const (
	AvatarW = 16 * S
	AvatarH = 24 * S // t_76 rescale
)

// Sprite legend:
//
//	H hair    S skin    E eye(open)    e eye(closed)
//	T shirt   P pants   O shoe
//
// body always faces the camera.
var bodyDown = []string{
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
	".SSTTTTTTTTTTSS.",
	".SSTTTTTTTTTTSS.",
	"..TTTPPPPPPTTT..",
	"...PPPPPPPPPP...",
	"...PPPP..PPPP...",
	"...PPP....PPP...",
	"...PPP....PPP...",
	"...PPP....PPP...",
	"..OOOO....OOOO..",
	"..OOOO....OOOO..",
}

// legsB is the "passing" pose: feet together, used as the second walk
// frame (reads as a step cycle against the apart pose above).
var bodyUp = []string{
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
	".SSTTTTTTTTTTSS.",
	".SSTTTTTTTTTTSS.",
	"..TTTPPPPPPTTT..",
	"...PPPPPPPPPP...",
	"...PPPP..PPPP...",
	"...PPP....PPP...",
	"...PPP....PPP...",
	"...PPP....PPP...",
	"..OOOO....OOOO..",
	"..OOOO....OOOO..",
}

var bodyRight = []string{
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
	"...STTTTTTS.....",
	"...STTTTTTS.....",
	"...TTTPPPPP.....",
	"....PPPPPPP.....",
	"....PPP.PPP.....",
	"....PP..PPP.....",
	"....PP..PPP.....",
	"....PP..PPP.....",
	"...OOOO.OOOO....",
	"...OOOO.OOOO....",
}

// legsTogether returns leg rows (7) for the passing pose, adapted per
// view by caller.
func legsTogether(view string) []string {
	switch view {
	case "right":
		return []string{
			"....PPPPPP......",
			"....PPPPP.......",
			"....PPPPP.......",
			"....PPPP........",
			"....PPPP........",
			"...OOOOOO.......",
			"...OOOOOO.......",
		}
	default: // down / up
		return []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....PPP..PPP....",
			"....PPP..PPP....",
			"...OOOO..OOOO...",
			"...OOOO..OOOO...",
		}
	}
}

func frameB(body []string, view string) []string {
	return append(append([]string(nil), body[:len(body)-7]...), legsTogether(view)...)
}

var skinC = C("#f0c8a0")
var pantsC = C("#46566b")
var shoeC = C("#6b4a35")
var eyeC = C("#2e2622")

// ── 形象分层（r_05，t_126）：终身层（发型/发色/肤色/饰品）＋每日层
//（上衣/裤鞋成套）——AvatarSpec 是分层的完整描述，BuildAvatar(spec)
// 按定稿 §四渲染序合成；spec 只有 shirt/hair 两维时逐字节等于旧输出
//（老 URL 兼容铁律）。终身层真源在 staffing 档案，这里只管画 ──

// HairStyle F1–F8（design/r05-avatar §二）：全部从现有圆盖头派生——
// 留海/分缝/削薄/鬓角/马尾/尖刺/长发七变体＋标准基线，不新建骨架。
const (
	HairStandard = "F1"
	HairFringe   = "F2"
	HairSidepart = "F3"
	HairBuzz     = "F4"
	HairBob      = "F5"
	HairPonytail = "F6"
	HairSpiky    = "F7"
	HairLong     = "F8"
)

// Accessory slots（§三挂点）：眼镜/耳环/发饰/工牌/领带，每挂点最多 1 件。
// t_128 并轨后新增围巾槽 C（r_05 任务正文点名围巾，量产库 8 款围巾）。
const (
	AccGlasses = "glasses" // 眼镜 G：Down/Right
	AccEarring = "earring" // 耳环 R：Down/Right
	AccHairpin = "hairpin" // 发饰 B：全视图
	AccBadge   = "badge"   // 工牌 W：Down
	AccTie     = "tie"     // 领带 K：Down
	AccScarf   = "scarf"   // 围巾 C：全视图（t_128 增设挂点）
)

// Accessory is one worn piece: slot + style + color hex. Style 是 t_128
// 资产库的饰品键（wardrobe.go Accessories 的 Key，如 "glasses-box-black"）
// ——为空时按 Slot 渲染该挂点的默认款。
type Accessory struct {
	Slot  string
	Style string
	Color string
}

// AvatarSpec is the layered look (§四): the lifetime layer (skin, hair
// color, hair style, accessories) plus the daily layer (top + bottom as
// one outfit set). Style fields take wardrobe.go keys（版型维度，t_128）;
// zero-value daily fields fall back to the legacy single-color render.
type AvatarSpec struct {
	Skin        string      // 终身：肤色 hex（空＝现有 #f0c8a0）
	Hair        string      // 终身：发色 hex（现有字段语义）
	HairStyle   string      // 终身：发型键——F1–F8 别名或 wardrobe 50 键（空＝F1 标准）
	Shirt       string      // 每日：上衣主色（兼容旧调用的 shirt 维）
	TopStyle    string      // 每日：上衣版型键（空＝tee，兼容锚点）
	Pants       string      // 每日：裤色（空＝现有 pantsC）
	BottomStyle string      // 每日：下装版型键——裤或裙（空＝straight）
	Acc         []Accessory // 终身：0–2 件
}

// hairStyleKey maps the spec's hair-style field onto wardrobe.go's key
// space: the F1–F8 aliases from the design doc land on their anchor
// styles; anything else passes through (a wardrobe key like "f2s1v0"),
// unknown/empty falls back to the standard cap.
func hairStyleKey(style string) string {
	switch style {
	case "", "F1":
		return "standard"
	case "F2":
		return "fringe"
	case "F3":
		return "sidepart"
	case "F4":
		return "buzz"
	case "F5":
		return "bob"
	case "F6":
		return "ponytail"
	case "F7":
		return "spiky"
	case "F8":
		return "long"
	default:
		return style
	}
}

// slotOf maps an accessory slot id onto wardrobe.go's G/R/B/W/K/C space.
func slotOf(slot string) string {
	switch slot {
	case AccGlasses, "G":
		return SlotGlasses
	case AccEarring, "R":
		return SlotEarring
	case AccHairpin, "B":
		return SlotHairOrn
	case AccBadge, "W":
		return SlotBadge
	case AccTie, "K":
		return SlotTie
	case AccScarf, "C":
		return SlotScarf
	}
	return slot
}

// accDefaultFor picks the wardrobe library's first item of a slot when
// a caller passed only slot+color (the pre-merge pool shape).
func accDefaultFor(slot string) string {
	s := slotOf(slot)
	if s == slot { // not a known slot id — treat as a wardrobe key
		return slot
	}
	for i := range Accessories {
		if Accessories[i].Slot == s {
			return Accessories[i].Key
		}
	}
	return ""
}

// BuildAvatarSpec renders the layered look — since the t_128 merge it
// delegates to the wardrobe composer (one rendering truth: hair axis
// derivation, clothing patches, accessory anchors all live in
// wardrobe_compose.go). F1–F8 alias onto wardrobe keys; blink frames
// ride the composer (which fixes the pre-merge blink-without-hair bug).
// The two-field legacy path (BuildAvatar) stays byte-identical (compat
// test pins it).
func BuildAvatarSpec(spec AvatarSpec) *AvatarSet {
	cs := ComposeSpec{
		Skin:      spec.Skin,
		Hair:      spec.Hair,
		HairStyle: hairStyleKey(spec.HairStyle),
		Top:       orDefault(spec.TopStyle, "tee"),
		TopColor:  spec.Shirt,
		Bottom:    orDefault(spec.BottomStyle, "straight"),
		BottomClr: spec.Pants,
	}
	for _, a := range spec.Acc {
		key := a.Style
		if key == "" {
			key = accDefaultFor(a.Slot) // slot-only caller: wardrobe default of that slot
		}
		if key != "" {
			cs.Acc = append(cs.Acc, key)
		}
	}
	return ComposeAvatarSet(cs)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// BuildAvatar renders the character frames with the member's shirt and
// hair colors — the legacy two-field entry, byte-identical through
// BuildAvatarSpec with an otherwise-empty spec (兼容铁律).
func BuildAvatar(shirtHex, hairHex string) *AvatarSet {
	return BuildAvatarSpec(AvatarSpec{Shirt: shirtHex, Hair: hairHex})
}

// AtlasOrder is the avatar atlas's frame order — the canvas board
// (web/room/art.js) slices by it; changing the order breaks the wire.
var AtlasOrder = []string{"downA", "downB", "downBlink", "upA", "upB", "leftA", "leftB", "rightA", "rightB"}

// AvatarAtlasSpec lays the layered look's every frame (the who=&day=
// face's atlas; AtlasOrder identical to the legacy atlas).
func AvatarAtlasSpec(spec AvatarSpec) *image.NRGBA {
	set := BuildAvatarSpec(spec)
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

// AvatarAtlas lays one member's every frame in a single row, frame
// width AvatarW — one PNG per (shirt, hair) rides to the browser
// instead of ten requests.
func AvatarAtlas(shirtHex, hairHex string) *image.NRGBA {
	set := BuildAvatar(shirtHex, hairHex)
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
