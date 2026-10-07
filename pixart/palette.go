package pixart

// palette.go — t_128 量产色板的统一之家（kb design/r05-avatar §四「新增
// 色一律进 palette.go 统一管理」＋ r05-wardrobe-mass §二.2 色板审计口径）。
// 这里只放「板」：跨品类复用的色列与金属/中性色常量；每件饰品自带的主色
// 是条目属性（同 TopStyleDef.Accent 的地位），留在 wardrobe.go 条目上。

// TopColors is the daily top colorway — the same ten hexes, in the same
// order, as chat/hub.go's shirtPalette and web/ui/dom.js's SHIRT_PALETTE
// (三方孪生：一人一色的 UI 传统延续到衣柜).
var TopColors = []string{
	"#3370ff", "#00a9ff", "#00b392", "#34c724", "#7ac70c",
	"#ff8800", "#f54a45", "#f75cb4", "#9e6bff", "#5856d6",
}

// BottomColors is the pants colorway — an independent cool/earthy board
// (r05-wardrobe-mass §一: 裤色独立板), steering clear of the shirt
// palette's bright end so a outfit's halves never smear together.
var BottomColors = []string{
	"#2f343c", // 炭黑
	"#33415c", // 藏青
	"#b09a6b", // 卡其
	"#4a6b8a", // 牛仔蓝
	"#55603a", // 军绿
	"#4a4a4f", // 烟灰
	"#6b5a45", // 咖啡
	"#8a8073", // 浅灰褐
	"#3d5a4a", // 深森绿
	"#5c4a5c", // 藕紫灰
}

// SkinColors is the lifetime skin tier (r05-avatar §一: 3 档).
var SkinColors = []struct{ Key, Name, Hex string }{
	{"light", "浅肤", "#f0c8a0"},
	{"mid", "中肤", "#e8b88c"},
	{"deep", "深肤", "#d9a878"},
}

// Accessory metal & neutral shared hexes — 金/银/珍珠/炭黑/漂白，饰品
// 条目引用这些常量而非各写一份字面量（hex 无重复口径）。
const (
	MetalGold      = "#c9a84c"
	MetalSilver    = "#b8bcc2"
	MetalPearl     = "#ece7de"
	MetalRose      = "#c98a7a"
	NeutralInk     = "#2e2e2e"
	NeutralDark    = "#1b1b1b"
	AccentOffwhite = "#e8e4de"
	AccentStripe   = "#8a8a90"
)
