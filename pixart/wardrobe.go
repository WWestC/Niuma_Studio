package pixart

// wardrobe.go — t_128 服饰资产库：r_05「员工形象系统」的资产数据表，
// 规格出自 kb design/r05-avatar（t_125 定稿 rev 2）。本文件只有数据与
// 轻量查表，不含渲染——程序化渲染在 wardrobe_compose.go，量产抽检的
// contact-sheet 在 tools/wardrobedump。
//
// 批次化原则（任务描述原文「数据描述而非逐张手绘」）：
//   - 上衣  = 10 款版型（字符画图案）× 10 色 = 100 件
//   - 裤子  = 10 款版型 × 5 色 = 50 件
//   - 裙子  = 10 款版型（各配默认色）
//   - 发型  = 留海×鬓角×蓬度三轴网格 + 背面变体，共 52 款（§二「全部从
//     圆盖头派生」的数据化：每款是一组轴参数，不是一张手绘）
//   - 饰品  = 挂点×形状模板×色，共 52 件
//   - 发色  = §二概率表原样（黑 99%）
//
// 图案（Top/Bottom/Skirt 的 Down/Up/Right 行）是 16×24 sprite 的分区
// 替换块：上衣占 y10–15（6 行），下装/裙占 y17–23（7 行），y16 腰线由
// 基线字符画继承。行内符文约定（ComposePalette 解析）：
//   T 上衣主色（每日 hex）  t 上衣暗一档（程序派生）  k 版型装饰色（Accent）
//   P 下装主色             p 下装暗一档             j 下装装饰色（Accent）
//   S 肤色（终身）         O 鞋                     H/h 发色/发色暗档
// 饰品图案用 1=主色 2=辅色，锚点坐标＝16×24 legacy 网格（§三挂点）。

import "fmt"

// ---------------------------------------------------------------------------
// 发色（终身层）——§二概率表，Weight 为万分比，全表合计 10000。
// ---------------------------------------------------------------------------

// HairColorDef is one hair color and its recruit-roll weight (‱).
type HairColorDef struct {
	Key    string
	Name   string
	Hex    string
	Weight int
}

// HairColors is the 10-color lifetime pool: 黑 99%, nine rare "传说同事"
// colors sharing 1%. Order matches the spec table top to bottom.
var HairColors = []HairColorDef{
	{"black", "深棕黑", "#3b2f2f", 9900},
	{"brown", "棕", "#7a4a2b", 30},
	{"chestnut", "深栗", "#a8763e", 25},
	{"gray", "灰", "#57534e", 15},
	{"flax", "亚麻金", "#d9b380", 10},
	{"wine", "酒红", "#8c3b3b", 8},
	{"silver", "银白", AccentOffwhite, 6},
	{"blueblack", "蓝黑", "#2f3b46", 3},
	{"pink", "粉", "#e89bb0", 2},
	{"green", "绿", "#4a7d52", 1},
}

// ---------------------------------------------------------------------------
// 肤色（终身层）——§一 3 档。
// ---------------------------------------------------------------------------

// SkinColors is the lifetime skin tier — now homed in palette.go.

// ---------------------------------------------------------------------------
// 发型（终身层）——三轴派生网格 + 背面变体，52 款。
// ---------------------------------------------------------------------------

// HairStyleDef is one hairstyle as axis parameters over the base 圆盖头
// (design doc §二: 留海形状 × 鬓角长度 × 头顶蓬度, plus a back variant).
type HairStyleDef struct {
	Key      string
	Name     string
	Fringe   int // 0 无留海 1 平留海 2 侧分 3 曲齿
	Sideburn int // 0 标准 1 长鬓 2 及肩
	Volume   int // 0 平顶 1 标准 2 蓬松
	Back     int // 0 无 1 马尾 2 长发 3 尖刺 4 削薄
}

// Hair-style axis constants — each axis numbers from its own zero so the
// grid generator's 0-based loop indices line up with the values.
const (
	FringeNone = iota
	FringeStraight
	FringeSide
	FringeZig
)
const (
	SideStd = iota
	SideLong
	SideShoulder
)
const (
	VolFlat = iota
	VolStd
	VolFull
)
const (
	BackNone = iota
	BackPonytail
	BackLong
	BackSpiky
	BackBuzz
)

// HairStyles leads with the eight spec anchors F1–F8 (same keys as
// design/r05-avatar §二), then the 32-combo plain grid, then back
// variants — 52 total, appended by init below.
var HairStyles = []HairStyleDef{
	{"standard", "标准圆盖", FringeNone, SideStd, VolStd, BackNone},     // F1
	{"fringe", "平留海", FringeStraight, SideStd, VolStd, BackNone},    // F2
	{"sidepart", "左侧分缝", FringeSide, SideStd, VolStd, BackNone},     // F3
	{"buzz", "削薄板寸", FringeNone, SideStd, VolFlat, BackBuzz},        // F4
	{"bob", "齐耳波波", FringeStraight, SideShoulder, VolStd, BackNone}, // F5
	{"ponytail", "低马尾", FringeNone, SideStd, VolStd, BackPonytail},  // F6
	{"spiky", "头顶尖刺", FringeNone, SideStd, VolStd, BackSpiky},       // F7
	{"long", "披肩长发", FringeNone, SideShoulder, VolStd, BackLong},    // F8
}

func init() {
	vols := []string{"平顶", "标准", "蓬松"}
	frs := []string{"无留海", "平留海", "侧分", "曲齿"}
	sds := []string{"标准鬓", "长鬓", "及肩鬓"}
	// the four plain-grid cells the F1–F8 anchors already occupy
	taken := map[[3]int]bool{
		{FringeNone, SideStd, VolStd}:          true,
		{FringeStraight, SideStd, VolStd}:      true,
		{FringeSide, SideStd, VolStd}:          true,
		{FringeStraight, SideShoulder, VolStd}: true,
	}
	for f := 0; f < 4; f++ {
		for s := 0; s < 3; s++ {
			for v := 0; v < 3; v++ {
				if taken[[3]int{f, s, v}] {
					continue
				}
				HairStyles = append(HairStyles, HairStyleDef{
					fmt.Sprintf("f%ds%dv%d", f, s, v),
					vols[v] + "·" + frs[f] + "·" + sds[s], f, s, v, BackNone,
				})
			}
		}
	}
	HairStyles = append(HairStyles,
		HairStyleDef{"ponytail-fringe", "平留海马尾", FringeStraight, SideStd, VolStd, BackPonytail},
		HairStyleDef{"ponytail-side", "侧分马尾", FringeSide, SideStd, VolStd, BackPonytail},
		HairStyleDef{"ponytail-full", "蓬顶马尾", FringeNone, SideStd, VolFull, BackPonytail},
		HairStyleDef{"ponytail-longside", "长鬓低马尾", FringeNone, SideLong, VolStd, BackPonytail},
		HairStyleDef{"long-fringe", "留海披肩", FringeStraight, SideShoulder, VolStd, BackLong},
		HairStyleDef{"long-flat", "平顶披肩", FringeNone, SideShoulder, VolFlat, BackLong},
		HairStyleDef{"long-full", "蓬松披肩", FringeNone, SideShoulder, VolFull, BackLong},
		HairStyleDef{"spiky-fringe", "留海尖刺", FringeStraight, SideStd, VolStd, BackSpiky},
		HairStyleDef{"spiky-side", "侧分尖刺", FringeSide, SideStd, VolStd, BackSpiky},
		HairStyleDef{"buzz-longside", "长鬓板寸", FringeNone, SideLong, VolFlat, BackBuzz},
	)
}

// HairStyleByKey finds one hairstyle (nil = unknown key). Base table
// first, then plugin-contributed addons (v3 形态 A, addon.go) — same
// lookup surface, so the composer never learns where a style came from.
func HairStyleByKey(key string) *HairStyleDef {
	for i := range HairStyles {
		if HairStyles[i].Key == key {
			return &HairStyles[i]
		}
	}
	addonMux.RLock()
	defer addonMux.RUnlock()
	return addonHairStyleByKey(key)
}

// ---------------------------------------------------------------------------
// 上衣（每日层）——10 版型 × 10 色 = 100 件。
// ---------------------------------------------------------------------------

// TopStyleDef is one top's cut: three view patches over y10–15. Accent is
// the版型装饰色 (buttons / zip / pocket trim); empty = the style uses no
// accent rune.
type TopStyleDef struct {
	Key    string
	Name   string
	Accent string
	Down   []string
	Up     []string
	Right  []string
}

// TopStyles — 10 版型（r05-wardrobe-mass §一：T恤/POLO/卫衣/衬衫/马甲/
// 开衫/夹克/毛衣/工装/高领），× TopColors 10 色（palette.go）= 100 件。
// T1 tee's patches are byte-identical to the base charmap's y10–15 — the
// compatibility anchor: ComposeSpec{Top: T1, Bottom: P1, standard hair}
// must reproduce the legacy BuildAvatar exactly (spec §五.1).
var TopStyles = []TopStyleDef{
	{Key: "tee", Name: "圆领T恤", Accent: "",
		Down: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...STTTTTTS.....",
			"...STTTTTTS.....",
		}},
	{Key: "polo", Name: "POLO衫", Accent: "",
		Down: []string{
			"...TtTTTTTTtT...",
			"..TTTTTTtTTTTT..",
			"..TTTTTTtTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...TTTTTtTT.....",
			"...TTTTTtTT.....",
			"...TTTTTTTT.....",
			"...STTTTTTS.....",
			"...STTTTTTS.....",
		}},
	{Key: "sweater", Name: "圆领卫衣", Accent: "",
		Down: []string{
			"...TtTTTTTTtT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSttttttttttSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSttttttttttSS.",
		},
		Right: []string{
			"....TtTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...STTTTTTS.....",
			"...SttttttS.....",
		}},
	{Key: "shirt", Name: "扣领衬衫", Accent: AccentOffwhite,
		Down: []string{
			"...TTTTttTTTT...",
			"..TTTTTkkTTTTT..",
			"..TTTTTkTTTTTT..",
			"..TTTTTkTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...TTTTTkTT.....",
			"...TTTTTkTT.....",
			"...TTTTTkTT.....",
			"...STTTTTTS.....",
			"...STTTTTTS.....",
		}},
	{Key: "vest", Name: "针织马甲", Accent: "",
		Down: []string{
			"...STTTTTTTTS...",
			"..SSTTTTTTTTSS..",
			"..SSTTTTTTTTSS..",
			"..SSTTTTTTTTSS..",
			".SSSTTTTTTTTSSS.",
			".SSSTTTTTTTTSSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...STTTTTTT.....",
			"...STTTTTTT.....",
			"...STTTTTTT.....",
			"...STTTTTTS.....",
			"...STTTTTTS.....",
		}},
	{Key: "knit", Name: "麻花毛衣", Accent: "",
		Down: []string{
			"...tttttttttt...",
			"..TTTtTTTtTTTT..",
			"..TTtTTTTTtTTT..",
			"..TTTtTTTtTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSttttttttttSS.",
		},
		Up: []string{
			"...tttttttttt...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSttttttttttSS.",
		},
		Right: []string{
			"....ttttttt.....",
			"...TTtTTtTT.....",
			"...TTtTTtTT.....",
			"...TTtTTtTT.....",
			"...STTTTTTS.....",
			"...SttttttS.....",
		}},
	{Key: "jacket", Name: "拉链夹克", Accent: NeutralInk,
		Down: []string{
			"...TTTTkkTTTT...",
			"..TTTTTkkTTTTT..",
			"..TTTtTkkTtTTT..",
			"..TTTtTkkTtTTT..",
			".SSTTTTkkTTTTSS.",
			".SSttttkkttttSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSttttttttttSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...TTTTTkkTT....",
			"...TTTTTkkTT....",
			"...TTTTTkkTT....",
			"...STTTTkTS.....",
			"...SttttkkS.....",
		}},
	{Key: "cardigan", Name: "开衫", Accent: AccentOffwhite,
		Down: []string{
			"...TTTTttTTTT...",
			"..TTTTtkktTTTT..",
			"..TTTTtkktTTTT..",
			"..TTTTtkktTTTT..",
			".SSTTTtkktTTTSS.",
			".SSTTTtkktTTTSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...TTTTtkkTT....",
			"...TTTTtkkTT....",
			"...TTTTtkkTT....",
			"...STTTtkkS.....",
			"...STTTtkkS.....",
		}},
	{Key: "workshirt", Name: "工装衬衫", Accent: "",
		Down: []string{
			"...TTTTttTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTttTTTTttTT..",
			"..TTttTTTTttTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Up: []string{
			"...TTTTTTTTTT...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....TTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTtTTtTT.....",
			"...TTtTTtTT.....",
			"...STTTTTTS.....",
			"...STTTTTTS.....",
		}},
	{Key: "mockneck", Name: "高领衫", Accent: "",
		Down: []string{
			"...tttttttttt...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Up: []string{
			"...tttttttttt...",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			"..TTTTTTTTTTTT..",
			".SSTTTTTTTTTTSS.",
			".SSTTTTTTTTTTSS.",
		},
		Right: []string{
			"....ttttttt.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...TTTTTTTT.....",
			"...STTTTTTS.....",
			"...STTTTTTS.....",
		}},
}

// ---------------------------------------------------------------------------
// 下装（每日层，随上衣成套）——10 版型 × 5 色 = 50 件。
// ---------------------------------------------------------------------------

// BottomStyleDef is one pair of pants: three view patches over y17–23.
type BottomStyleDef struct {
	Key    string
	Name   string
	Accent string
	Down   []string
	Up     []string
	Right  []string
	// WalkB（r_13 t_167）：B 帧腿型变体（7 行替换 y17–23）。nil＝
	// legsTogether 兜底——straight 等裤款保持 nil，B 帧与现版逐字节一致。
	WalkB []string
}

// BottomStyles — 10 版型 × BottomColors 10 色（palette.go）= 100 件
// （任务配额 50+，超配：版型涵盖 r05-wardrobe-mass §一 的 5 型全集——
// 直筒 straight／锥形 jogger／阔腿 wide／工装 cargo／九分 rollup）。
var BottomStyles = []BottomStyleDef{
	{Key: "straight", Name: "直筒裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PPP.PPP.....",
			"....PP..PPP.....",
			"....PP..PPP.....",
			"....PP..PPP.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "shorts", Name: "短裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PPP.PPP.....",
			"....PP..PPP.....",
			"....SS..SSS.....",
			"....SS..SSS.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
	},
	{Key: "cargo", Name: "工装裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...pPPP..PPPp...",
			"...pPPP..PPPp...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....pPP.PPP.....",
			"....pPP.PPP.....",
			"....PP..PPP.....",
			"....PP..PPP.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "slacks", Name: "西裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PpPP..PPpP...",
			"...PpP....PpP...",
			"...PpP....PpP...",
			"...PpP....PpP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PpPP..PPpP...",
			"...PpP....PpP...",
			"...PpP....PpP...",
			"...PpP....PpP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PpP.PPP.....",
			"....PP..PpP.....",
			"....PP..PpP.....",
			"....PP..PpP.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "jogger", Name: "束脚裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"...ppp....ppp...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...PPP....PPP...",
			"...ppp....ppp...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PPP.PPP.....",
			"....PP..PPP.....",
			"....PP..PPP.....",
			"....pp..ppp.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "wide", Name: "阔腿裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "skinny", Name: "窄脚裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"....PP....PP....",
			"....PP....PP....",
			"....PP....PP....",
			"....PP....PP....",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"....PP....PP....",
			"....PP....PP....",
			"....PP....PP....",
			"....PP....PP....",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PP...PP.....",
			"....PP...PP.....",
			"....PP...PP.....",
			"....PP...PP.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "rollup", Name: "卷边裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...ppp....ppp...",
			"...ppp....ppp...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPP..PPPP...",
			"...PPP....PPP...",
			"...ppp....ppp...",
			"...ppp....ppp...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PPP.PPP.....",
			"....PP..PPP.....",
			"....pp..PPP.....",
			"....pp..ppp.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "corduroy", Name: "灯芯绒裤", Accent: "",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPpP..PpPP...",
			"...PPp....pPP...",
			"...PPp....pPP...",
			"...PPp....pPP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPpP..PpPP...",
			"...PPp....pPP...",
			"...PPp....pPP...",
			"...PPp....pPP...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....PpP.PPP.....",
			"....Pp..PPp.....",
			"....Pp..PPp.....",
			"....Pp..PPp.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
	{Key: "track", Name: "运动短裤", Accent: AccentOffwhite,
		Down: []string{
			"...PPPPPPPPPP...",
			"...jPPP..PPPj...",
			"...jPPP..PPPj...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...jPPP..PPPj...",
			"...jPPP..PPPj...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"....jPP.PPP.....",
			"....jPP.PPP.....",
			"....SS..SSS.....",
			"....SS..SSS.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		}},
}

// ---------------------------------------------------------------------------
// 裙（10 款）——与裤同占 y17–23，引擎里替换 Bottom 即穿裙。
// ---------------------------------------------------------------------------

// SkirtDef is one skirt: a bottom patch set plus its default colorway.
type SkirtDef struct {
	BottomStyleDef
	Color string
}

var Skirts = []SkirtDef{
	{BottomStyleDef{Key: "aline-mini", Name: "A字短裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"....SSSSSS......",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"....PPPPPP.....",
			"....PPPPPP.....",
			"....PPPPPP.....",
			"....SSSSSS.....",
			"...OOOOOO......",
			"...OOOOOO......",
		},
	}, "#2f343c"},
	{BottomStyleDef{Key: "aline-maxi", Name: "A字长裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"..PPPPPPPPPP....",
			"..PPPPPPPPPP....",
			"..PPPPPPPPPP....",
			"..PPPPPPPPPP....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..OOOOOOOOOOOO..",
			"..OOOOOOOOOOOO..",
		},
	}, "#8c3b3b"},
	{BottomStyleDef{Key: "pleats", Name: "百褶裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PpPPpPPpPP...",
			"..PpPPpPPpPPp...",
			"..PpPPpPPpPPp...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PpPPpPPpPP...",
			"..PpPPpPPpPPp...",
			"..PpPPpPPpPPp...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PpPPPpPP.....",
			"..PpPPPpPPpP....",
			"..PpPPPpPPpP....",
			"....SSSSSS......",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....PPPPPPPP....",
			"....PPPPPPPP....",
			"...OOOOOOOO.....",
			"...OOOOOOOO.....",
		},
	}, "#33415c"},
	{BottomStyleDef{Key: "pencil", Name: "铅笔裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"....SSSSSS......",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....PPPPPPPP....",
			"....PPPPPPPP....",
			"....SSSSSS......",
			"...OOOOOO.......",
			"...OOOOOO.......",
		},
	}, "#4a4a4f"},
	{BottomStyleDef{Key: "skater", Name: "蓬蓬裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			".PPPPPPPPPPPPPP.",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			".PPPPPPPPPPPPPP.",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"..PPPPPPPPPP....",
			".PPPPPPPPPPP....",
			"..PPPPPPPPPP....",
			"....SSSSSS......",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....SSSSSS......",
			"...OOOOOO.......",
			"...OOOOOO.......",
		},
	}, "#a8763e"},
	{BottomStyleDef{Key: "wrap", Name: "罩裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPp...",
			"..PPPPPPPPpPPP..",
			"..PPPPPPPpPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPPp....",
			"...PPPPPPpPP....",
			"...PPPPPpPPP....",
			"....SSSSSS......",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....PPPPPPPP....",
			"....SSSSSS......",
			"...OOOOOO.......",
			"...OOOOOO.......",
		},
	}, "#55603a"},
	{BottomStyleDef{Key: "tiered", Name: "蛋糕裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..pPPPPPPPPPPp..",
			"..PPPPPPPPPPPP..",
			".pPPPPPPPPPPPPp.",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..pPPPPPPPPPPp..",
			"..PPPPPPPPPPPP..",
			".pPPPPPPPPPPPPp.",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"..pPPPPPPPPp....",
			"..PPPPPPPPPP....",
			".pPPPPPPPPPp....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....PPPPPPPP....",
			"...OOOOOOOO.....",
			"...OOOOOOOO.....",
		},
	}, "#8c3b3b"},
	{BottomStyleDef{Key: "micro", Name: "迷你裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"....SS..SSS.....",
			"....SS..SSS.....",
			"....SS..SSS.....",
			"...OOOO.OOOO....",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"....PPPPPPPP....",
			"....SSSSSS......",
			"....SSSSSS......",
			"....SSSSSS......",
			"...OOOOOO.......",
			"...OOOOOO.......",
		},
	}, "#5856d6"},
	{BottomStyleDef{Key: "midi", Name: "中长裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"...SSS....SSS...",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"...PPPPPPPP.....",
			"....SSSSSS......",
			"...OOOO.OOOO....",
		},
		WalkB: []string{
			"...PPPPPPPPPP...",
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..PPPPPPPPPPPP..",
			"..OOOOOOOOOO....",
			"..OOOOOOOOOO....",
		},
	}, "#2f343c"},
	{BottomStyleDef{Key: "gypsy", Name: "大摆长裙",
		Down: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			"..OOOO....OOOO..",
		},
		Up: []string{
			"...PPPPPPPPPP...",
			"..PPPPPPPPPPPP..",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			".PPPPPPPPPPPPPP.",
			"..OOOO....OOOO..",
		},
		Right: []string{
			"....PPPPPPP.....",
			"...PPPPPPPP.....",
			"..PPPPPPPPPP....",
			".PPPPPPPPPPP....",
			".PPPPPPPPPPP....",
			".PPPPPPPPPPP....",
			"...OOOO.OOOO....",
		}}, "#7a4a2b"},
}

// ---------------------------------------------------------------------------
// 饰品（终身层）——挂点×形状×色，52 件。
// ---------------------------------------------------------------------------

// Accessory slots (§三挂点契约): G 眼镜 R 耳环 B 发饰 W 工牌 K 领带,
// plus C 围巾 (task r_05 body lists 围巾 explicitly; anchor = neck y9–10,
// proposed here for 小鹿/小猿 ratification alongside the doc's five).
const (
	SlotGlasses = "G"
	SlotEarring = "R"
	SlotHairOrn = "B"
	SlotBadge   = "W"
	SlotTie     = "K"
	SlotScarf   = "C"
)

// AccView is one accessory's patch in one view: Rows of '1' (主色) and
// '2' (辅色) blitted at (X, Y) on the 16×24 legacy grid. Views present =
// drawn; a view absent = the item hides there (glasses have no Up patch).
// "left" derives from "right" by mirroring — no separate patch needed.
type AccView struct {
	X, Y int
	Rows []string
}

// AccessoryDef is one accessory item. Hex2 doubles as the stripe/knot
// secondary color; empty = the shape uses only the main color.
type AccessoryDef struct {
	Key   string
	Name  string
	Slot  string
	Hex   string
	Hex2  string
	Views map[string]AccView
}

// Glasses shapes (§三: 眼镜挂点 (5,5)–(10,7), 双眼 y5–7 鼻梁 x8 — eyes sit
// at cols 6/10, frames flank them so the eyes stay visible; side view =
// temple arm from the ear to a lens edge flanking the single eye).
var accGlassesBox = map[string]AccView{
	"down":  {X: 5, Y: 5, Rows: []string{"1111111", "1.1.1.1"}},
	"right": {X: 4, Y: 5, Rows: []string{"1111.1", "...1.1"}},
}
var accGlassesRound = map[string]AccView{
	"down":  {X: 5, Y: 5, Rows: []string{".11111.", "1.1.1.1"}},
	"right": {X: 4, Y: 5, Rows: []string{".111.1", "...1.1"}},
}
var accGlassesRimless = map[string]AccView{
	"down":  {X: 5, Y: 5, Rows: []string{"...1...", "1.1.1.1"}},
	"right": {X: 4, Y: 5, Rows: []string{"1111...", "......."}},
}
var accGlassesHalf = map[string]AccView{
	"down":  {X: 5, Y: 6, Rows: []string{"1111111"}},
	"right": {X: 4, Y: 5, Rows: []string{"11", "..11111"}},
}
var accGlassesSolid = map[string]AccView{
	"down":  {X: 5, Y: 5, Rows: []string{"1111111", "1111111"}},
	"right": {X: 4, Y: 5, Rows: []string{"1111111", "...111"}},
}

// Earrings (§三: 左耳 (3,7) 右耳 (12,7) 各 1px; 坠式 +1px below; side
// view hangs below the ear at the head's west edge).
var accEarringStud = map[string]AccView{
	"down":  {X: 3, Y: 7, Rows: []string{"1........1"}},
	"right": {X: 4, Y: 7, Rows: []string{"1"}},
}
var accEarringDrop = map[string]AccView{
	"down":  {X: 3, Y: 7, Rows: []string{"1........1", "1........1"}},
	"right": {X: 4, Y: 7, Rows: []string{"1", "1"}},
}

// Hair ornaments (§三: 头顶偏左 (6,1) 2×1; bow = 3-wide knot).
var accHairClip = map[string]AccView{
	"down":  {X: 6, Y: 1, Rows: []string{"11"}},
	"right": {X: 4, Y: 1, Rows: []string{"11"}},
	"up":    {X: 6, Y: 1, Rows: []string{"11"}},
}
var accHairBow = map[string]AccView{
	"down":  {X: 5, Y: 1, Rows: []string{"1.1", "111"}},
	"right": {X: 4, Y: 1, Rows: []string{"1.1", "111"}},
	"up":    {X: 5, Y: 1, Rows: []string{"1.1", "111"}},
}
var accHairTie = map[string]AccView{
	"down":  {X: 6, Y: 1, Rows: []string{"22"}},
	"right": {X: 4, Y: 1, Rows: []string{"22"}},
	"up":    {X: 6, Y: 1, Rows: []string{"22"}},
}

// Badges (§三: 胸口 (7,12)–(8,15), 挂绳 1px 到领口).
var accBadge = map[string]AccView{
	"down": {X: 7, Y: 11, Rows: []string{"1", "11", "12", "11", "11"}},
}

// Ties (§三: 领口 (7,11) 垂 3px; 条纹 = 1/2 行交替).
var accTieSolid = map[string]AccView{
	"down":  {X: 7, Y: 11, Rows: []string{"11", "11", "11", "1."}},
	"right": {X: 6, Y: 11, Rows: []string{"11", "11", "1."}},
}
var accTieStripe = map[string]AccView{
	"down":  {X: 7, Y: 11, Rows: []string{"11", "22", "11", "2."}},
	"right": {X: 6, Y: 11, Rows: []string{"11", "22", "1."}},
}

// Scarves (proposed slot C: 绕颈 y9–10 + 垂尾; up 视图尾巴垂背上).
var accScarf = map[string]AccView{
	"down":  {X: 5, Y: 9, Rows: []string{"111111", "111111", "....11", "....11", "....1."}},
	"right": {X: 4, Y: 9, Rows: []string{"111111", "111111", ".....1", ".....1"}},
	"up":    {X: 5, Y: 9, Rows: []string{"111111", "111111", "1.....", "1.....", "1....."}},
}

func acc(key, name, slot, hex, hex2 string, views map[string]AccView) AccessoryDef {
	return AccessoryDef{Key: key, Name: name, Slot: slot, Hex: hex, Hex2: hex2, Views: views}
}

// Accessories — 52 items: 10 glasses + 8 earrings + 14 hair ornaments +
// 10 ties + 8 scarves + 2 badges. Colors steer clear of the shirt
// palette's bright end so the clash-shift rule (§三) rarely fires.
var Accessories = []AccessoryDef{
	// G 眼镜 ×10
	acc("glasses-box-black", "黑框方镜", SlotGlasses, NeutralInk, "", accGlassesBox),
	acc("glasses-round-black", "黑框圆镜", SlotGlasses, NeutralInk, "", accGlassesRound),
	acc("glasses-box-tortoise", "玳瑁方镜", SlotGlasses, "#7a4a2b", "", accGlassesBox),
	acc("glasses-round-brown", "棕框圆镜", SlotGlasses, "#8a6a52", "", accGlassesRound),
	acc("glasses-rimless-gold", "金丝无框", SlotGlasses, MetalGold, "", accGlassesRimless),
	acc("glasses-half-silver", "银丝半框", SlotGlasses, MetalSilver, "", accGlassesHalf),
	acc("glasses-solid-black", "墨镜", SlotGlasses, NeutralDark, "", accGlassesSolid),
	acc("glasses-box-red", "红框方镜", SlotGlasses, "#b03030", "", accGlassesBox),
	acc("glasses-box-blue", "蓝框方镜", SlotGlasses, "#3355aa", "", accGlassesBox),
	acc("glasses-round-gold", "金丝圆镜", SlotGlasses, MetalGold, "", accGlassesRound),
	// R 耳环 ×8
	acc("earring-gold-stud", "金豆耳钉", SlotEarring, MetalGold, "", accEarringStud),
	acc("earring-silver-stud", "银豆耳钉", SlotEarring, MetalSilver, "", accEarringStud),
	acc("earring-pearl-stud", "珍珠耳钉", SlotEarring, MetalPearl, "", accEarringStud),
	acc("earring-gold-drop", "金坠耳环", SlotEarring, MetalGold, "", accEarringDrop),
	acc("earring-silver-drop", "银坠耳环", SlotEarring, MetalSilver, "", accEarringDrop),
	acc("earring-ruby-stud", "红宝耳钉", SlotEarring, "#a83a3a", "", accEarringStud),
	acc("earring-jade-stud", "翡翠耳钉", SlotEarring, "#4a9a6a", "", accEarringStud),
	acc("earring-rosegold-stud", "玫瑰金耳钉", SlotEarring, MetalRose, "", accEarringStud),
	// B 发饰 ×14
	acc("clip-blue", "蓝发夹", SlotHairOrn, "#3370ff", "", accHairClip),
	acc("clip-red", "红发夹", SlotHairOrn, "#f54a45", "", accHairClip),
	acc("clip-green", "绿发夹", SlotHairOrn, "#34c724", "", accHairClip),
	acc("clip-yellow", "黄发夹", SlotHairOrn, "#ffcc00", "", accHairClip),
	acc("clip-purple", "紫发夹", SlotHairOrn, "#9e6bff", "", accHairClip),
	acc("clip-pink", "粉发夹", SlotHairOrn, "#f75cb4", "", accHairClip),
	acc("clip-white", "白发夹", SlotHairOrn, MetalPearl, "", accHairClip),
	acc("clip-black", "黑发夹", SlotHairOrn, NeutralInk, "", accHairClip),
	acc("tie-band-black", "黑头绳", SlotHairOrn, NeutralInk, "", accHairTie),
	acc("tie-band-brown", "棕头绳", SlotHairOrn, "#7a4a2b", "", accHairTie),
	acc("bow-red", "红蝴蝶结", SlotHairOrn, "#c03030", "", accHairBow),
	acc("bow-blue", "蓝蝴蝶结", SlotHairOrn, "#3355aa", "", accHairBow),
	acc("bow-white", "白蝴蝶结", SlotHairOrn, MetalPearl, "", accHairBow),
	acc("bow-green", "绿蝴蝶结", SlotHairOrn, "#4a7d52", "", accHairBow),
	// K 领带 ×10
	acc("tie-navy", "藏青领带", SlotTie, "#33415c", "", accTieSolid),
	acc("tie-wine", "酒红领带", SlotTie, "#8c3b3b", "", accTieSolid),
	acc("tie-forest", "墨绿领带", SlotTie, "#2f5a3a", "", accTieSolid),
	acc("tie-charcoal", "炭灰领带", SlotTie, "#4a4a4f", "", accTieSolid),
	acc("tie-black", "黑领带", SlotTie, NeutralInk, "", accTieSolid),
	acc("tie-stripe-blue", "蓝白条纹领带", SlotTie, "#3355aa", MetalPearl, accTieStripe),
	acc("tie-stripe-red", "红白条纹领带", SlotTie, "#b03030", MetalPearl, accTieStripe),
	acc("tie-stripe-gold", "灰金条纹领带", SlotTie, "#4a4a4f", MetalGold, accTieStripe),
	acc("tie-stripe-green", "绿白条纹领带", SlotTie, "#4a7d52", MetalPearl, accTieStripe),
	acc("tie-stripe-black", "黑金条纹领带", SlotTie, NeutralInk, MetalGold, accTieStripe),
	// C 围巾 ×8
	acc("scarf-red", "红围巾", SlotScarf, "#b03030", "", accScarf),
	acc("scarf-blue", "蓝围巾", SlotScarf, "#3355aa", "", accScarf),
	acc("scarf-gray", "灰围巾", SlotScarf, "#6a6a70", "", accScarf),
	acc("scarf-green", "绿围巾", SlotScarf, "#4a7d52", "", accScarf),
	acc("scarf-mustard", "芥黄围巾", SlotScarf, "#c9973c", "", accScarf),
	acc("scarf-purple", "紫围巾", SlotScarf, "#6a4a8c", "", accScarf),
	acc("scarf-stripe-red", "红灰条纹围巾", SlotScarf, "#b03030", AccentStripe, accScarf),
	acc("scarf-stripe-blue", "蓝白条纹围巾", SlotScarf, "#3355aa", MetalPearl, accScarf),
	// W 工牌 ×2
	acc("badge-blue", "蓝工牌", SlotBadge, "#3370ff", MetalPearl, accBadge),
	acc("badge-red", "红工牌", SlotBadge, "#c03030", MetalPearl, accBadge),
}

// AccessoryByKey finds one accessory (nil = unknown key).
func AccessoryByKey(key string) *AccessoryDef {
	for i := range Accessories {
		if Accessories[i].Key == key {
			return &Accessories[i]
		}
	}
	return nil
}
