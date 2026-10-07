package pixart

// food.go — the 食品资产库 (r_07, t_136): 20+ snacks + 10+ fruits, three
// forms each — shelf (货架陈列 8×8), handheld (手持 5×5, the A12 carry
// anchor's food kind) and residue (残骸, the crumb the litter leaves).
// Built on the wardrobe pipeline's own language: character-grid patches
// over the legacy 16×24 space with '1' main / '2' accent colors, entries
// parameterized 型×包装色×图案 (the same shape-color-variant
// multiplication the 52 accessories ride). Every entry carries its
// residue key (残骸自带, the t_136 review's ruling) — no separate residue
// category exists.

import (
	"image"
)

// FoodForm is one food's three-form geometry.
type FoodForm struct {
	Key     string   // 条目键（food-xxx）
	Name    string   // 中文名
	Hex     string   // 主色（包装色/果色）
	Hex2    string   // 辅色（图案/果柄）
	Shelf   []string // 货架陈列 8×8（'1' 主 '2' 辅）
	Hand    []string // 手持 5×5（贴手缩形）
	Residue string   // 残骸键（res-xxx；零值＝无残骸——水果核/包装共用画法族）
	ResRows []string // 残骸形 6×4
}

// foods —— 零食 22 条（四型：袋装/罐装/盒装/棒状 × 包装色 × 图案）。
// 袋装：锯齿顶封口（上缘锯齿＋袋身图案窗）；罐装：圆柱罐＋拉环；
// 盒装：方盒＋侧面色带；棒状：细长条＋两端包装捻头。
var foods = []FoodForm{
	// ── 袋装 ×6（锯齿顶 + 图案窗）──
	{Key: "food-bag-red", Name: "红袋薯片", Hex: "#d95f4e", Hex2: "#f2c14e",
		Shelf:   []string{"2.2.2.2.", "11111111", "11221111", "11111111", "11122111", "11111111", "1111111.", "22222222"},
		Hand:    []string{"2.2.2", "11111", "11211", "11111", "11111"},
		Residue: "res-crushed-bag", ResRows: []string{"..11..", ".1112.", "112111", ".1.11."}},
	{Key: "food-bag-blue", Name: "蓝袋虾条", Hex: "#5b8bbd", Hex2: "#fbfbf8",
		Shelf:   []string{".2.2.2.2", "11111111", "11122111", "11111111", "11221111", "11111111", ".1111111", "22222222"},
		Hand:    []string{".2.2", "11111", "12111", "11111", "11111"},
		Residue: "res-crushed-bag", ResRows: []string{"..11..", ".2111.", "111211", ".11.1."}},
	{Key: "food-bag-gold", Name: "金袋米果", Hex: "#d9b380", Hex2: "#8c3b3b",
		Shelf:   []string{"2.2.2.2.", "11111111", "11211211", "11111111", "11111111", "11211211", "1111111.", "22222222"},
		Hand:    []string{"2.2.2", "11111", "11211", "11111", "11111"},
		Residue: "res-crushed-bag", ResRows: []string{"..21..", ".1111.", "112112", ".1..1."}},
	{Key: "food-bag-green", Name: "绿袋海苔", Hex: "#4a7d52", Hex2: "#fbfbf8",
		Shelf:   []string{".2.2.2.2", "11111111", "11111111", "11222211", "11222211", "11111111", ".1111111", "22222222"},
		Hand:    []string{".2.2", "11111", "11111", "12221", "11111"},
		Residue: "res-crushed-bag", ResRows: []string{"..11..", ".1121.", "121111", ".1.11."}},
	{Key: "food-bag-orange", Name: "橙袋饼干", Hex: "#ff8800", Hex2: "#7a4a2b",
		Shelf:   []string{"2.2.2.2.", "11111111", "11211211", "11211211", "11111111", "11111111", "1111111.", "22222222"},
		Hand:    []string{"2.2.2", "11111", "12121", "11111", "11111"},
		Residue: "res-crushed-bag", ResRows: []string{"..12..", ".1111.", "111211", ".11.2."}},
	{Key: "food-bag-purple", Name: "紫袋葡萄干", Hex: "#8c6b9c", Hex2: "#f2c14e",
		Shelf:   []string{".2.2.2.2", "11111111", "11111111", "11211111", "11121111", "11111111", ".1111111", "22222222"},
		Hand:    []string{".2.2", "11111", "11111", "11211", "11111"},
		Residue: "res-crushed-bag", ResRows: []string{"..11..", ".1112.", "111121", ".1.1."}},
	// ── 罐装 ×5（圆柱罐 + 拉环）──
	{Key: "food-can-soda", Name: "汽水罐", Hex: "#aab4bd", Hex2: "#3370ff",
		Shelf:   []string{".222222.", "11111111", "11221111", "11111111", "11111111", "11221111", "11111111", ".222222."},
		Hand:    []string{".222.", "11111", "12111", "11111", "11111"},
		Residue: "res-empty-can", ResRows: []string{".222.", "11.11", "11111", ".222."}},
	{Key: "food-can-coffee", Name: "咖啡罐", Hex: "#7a4a2b", Hex2: "#d9b380",
		Shelf:   []string{".222222.", "11111111", "11122111", "11111111", "11221111", "11111111", "11111111", ".222222."},
		Hand:    []string{".222.", "11111", "11211", "11111", "11111"},
		Residue: "res-empty-can", ResRows: []string{".222.", "11211", "11111", ".222."}},
	{Key: "food-can-tea", Name: "茶饮料", Hex: "#4a7d52", Hex2: "#fbfbf8",
		Shelf:   []string{".222222.", "11111111", "11211211", "11111111", "11111111", "11211211", "11111111", ".222222."},
		Hand:    []string{".222.", "11111", "12121", "11111", "11111"},
		Residue: "res-empty-can", ResRows: []string{".222.", "11111", "12111", ".222."}},
	{Key: "food-can-cola", Name: "可乐罐", Hex: "#3b2f2f", Hex2: "#d95f4e",
		Shelf:   []string{".222222.", "11111111", "11211111", "11111111", "11121111", "11111111", "11111111", ".222222."},
		Hand:    []string{".222.", "11111", "11211", "11111", "11111"},
		Residue: "res-empty-can", ResRows: []string{".222.", "11.11", "11121", ".222."}},
	{Key: "food-can-juice", Name: "果汁罐", Hex: "#f2a3b8", Hex2: "#ff8800",
		Shelf:   []string{".222222.", "11111111", "11122111", "11111111", "11221111", "11111111", "11111111", ".222222."},
		Hand:    []string{".222.", "11111", "12111", "11111", "11111"},
		Residue: "res-empty-can", ResRows: []string{".222.", "11211", "11111", ".222."}},
	// ── 盒装 ×6（方盒 + 侧面色带）──
	{Key: "food-box-choc", Name: "巧克力盒", Hex: "#7a4a2b", Hex2: "#d9b380",
		Shelf:   []string{"22222222", "11111111", "11111111", "22222222", "11111111", "11111111", "22222222", "11111111"},
		Hand:    []string{"22222", "11111", "11111", "22222", "11111"},
		Residue: "res-crushed-box", ResRows: []string{".111.", "11111", "21112", ".111."}},
	{Key: "food-box-cook", Name: "曲奇盒", Hex: "#d9b380", Hex2: "#8c3b3b",
		Shelf:   []string{"22222222", "11111111", "11221111", "22222222", "11111111", "11122111", "22222222", "11111111"},
		Hand:    []string{"22222", "11111", "11211", "22222", "11111"},
		Residue: "res-crushed-box", ResRows: []string{".111.", "11121", "11211", ".111."}},
	{Key: "food-box-candy", Name: "糖果盒", Hex: "#f2a3b8", Hex2: "#fbfbf8",
		Shelf:   []string{"22222222", "11111111", "12112111", "22222222", "11111111", "11121121", "22222222", "11111111"},
		Hand:    []string{"22222", "11111", "12111", "22222", "11111"},
		Residue: "res-crushed-box", ResRows: []string{".121.", "11111", "11121", ".111."}},
	{Key: "food-box-nut", Name: "坚果盒", Hex: "#a8763e", Hex2: "#57534e",
		Shelf:   []string{"22222222", "11111111", "11111111", "22222222", "11111111", "11111111", "22222222", "11111111"},
		Hand:    []string{"22222", "11111", "11111", "22222", "11111"},
		Residue: "res-crushed-box", ResRows: []string{".111.", "11111", "11111", ".111."}},
	{Key: "food-box-cereal", Name: "谷物盒", Hex: "#f2c14e", Hex2: "#4a7d52",
		Shelf:   []string{"22222222", "11111111", "12222111", "22222222", "11111111", "11222211", "22222222", "11111111"},
		Hand:    []string{"22222", "11111", "12221", "22222", "11111"},
		Residue: "res-crushed-box", ResRows: []string{".221.", "11111", "11112", ".111."}},
	{Key: "food-box-seaweed", Name: "海苔盒", Hex: "#3d6339", Hex2: "#4a7d52",
		Shelf:   []string{"22222222", "11111111", "11111111", "22222222", "11111111", "11111111", "22222222", "11111111"},
		Hand:    []string{"22222", "11111", "11111", "22222", "11111"},
		Residue: "res-crushed-box", ResRows: []string{".111.", "11111", "11111", ".111."}},
	// ── 棒状 ×5（细长条 + 两端捻头）──
	{Key: "food-stick-pretzel", Name: "椒盐棒", Hex: "#d9b380", Hex2: "#7a4a2b",
		Shelf:   []string{"2......2", "11111111", "11111111", "11221111", "11111111", "11111111", "11111111", "2......2"},
		Hand:    []string{"2...2", "11111", "11111", "11111", "2...2"},
		Residue: "res-stick-foil", ResRows: []string{".....", "11...", ".111.", "...11"}},
	{Key: "food-stick-biscuit", Name: "威化棒", Hex: "#e8d5b0", Hex2: "#8c3b3b",
		Shelf:   []string{"2......2", "11111111", "11221111", "11111111", "11111111", "11122111", "11111111", "2......2"},
		Hand:    []string{"2...2", "11111", "12111", "11111", "2...2"},
		Residue: "res-stick-foil", ResRows: []string{".....", ".11..", "..111", "...11"}},
	{Key: "food-stick-choc", Name: "巧克力棒", Hex: "#7a4a2b", Hex2: "#d9b380",
		Shelf:   []string{"2......2", "11111111", "11111111", "12112111", "11111111", "11111111", "11111111", "2......2"},
		Hand:    []string{"2...2", "11111", "11111", "12111", "2...2"},
		Residue: "res-stick-foil", ResRows: []string{".....", "..11.", ".1111", "..1.."}},
	{Key: "food-stick-cheese", Name: "芝士条", Hex: "#f2c14e", Hex2: "#ff8800",
		Shelf:   []string{"2......2", "11111111", "11122111", "11111111", "11111111", "11221111", "11111111", "2......2"},
		Hand:    []string{"2...2", "11111", "11211", "11111", "2...2"},
		Residue: "res-stick-foil", ResRows: []string{".....", "11...", "111..", "..11."}},
	{Key: "food-stick-mint", Name: "薄荷糖棒", Hex: "#4a7d52", Hex2: "#fbfbf8",
		Shelf:   []string{"2......2", "11111111", "11111111", "11111111", "12211111", "11111111", "11111111", "2......2"},
		Hand:    []string{"2...2", "11111", "11111", "11111", "2...2"},
		Residue: "res-stick-foil", ResRows: []string{".....", ".1...", ".111.", "...1."}},
	// ── 水果 ×10（圆形果 + 叶柄）──
	{Key: "food-apple", Name: "苹果", Hex: "#d95f4e", Hex2: "#4a7d52",
		Shelf:   []string{"....2...", "...22...", "..1111..", ".111111.", "11111111", "11111111", ".111111.", "..1111.."},
		Hand:    []string{"..2..", ".111.", "11111", "11111", ".111."},
		Residue: "res-apple-core", ResRows: []string{"..1..", ".121.", "1.2.1", "..1.."}},
	{Key: "food-orange", Name: "橘子", Hex: "#ff8800", Hex2: "#4a7d52",
		Shelf:   []string{"...2....", "..1111..", ".111111.", "11111111", "11111111", "11111111", ".111111.", "..1111.."},
		Hand:    []string{"..2..", ".111.", "11111", "11111", ".111."},
		Residue: "res-peel", ResRows: []string{".1.1.", "1.1.1", ".1.1.", "1..1."}},
	{Key: "food-banana", Name: "香蕉", Hex: "#f2c14e", Hex2: "#7a4a2b",
		Shelf:   []string{"......2.", ".....122", "..111112", ".111111.", "111111..", "11111...", "2211....", ".2......"},
		Hand:    []string{"...2.", "..112", "1111.", "111..", ".1..."},
		Residue: "res-peel", ResRows: []string{"1..1.", ".11..", "11...", ".1..."}},
	{Key: "food-grape", Name: "葡萄", Hex: "#8c6b9c", Hex2: "#4a7d52",
		Shelf:   []string{"....2...", "..1121..", ".121121.", "11211211", "12112112", ".112111.", "..1111..", "...11..."},
		Hand:    []string{"..2..", ".121.", "11211", ".111.", "..1.."},
		Residue: "res-peel", ResRows: []string{".1.1.", "1.1.1", ".1.1.", "..1.."}},
	{Key: "food-pear", Name: "梨", Hex: "#c9e6c8", Hex2: "#7a4a2b",
		Shelf:   []string{"....2...", "...11...", "..1111..", ".111111.", "11111111", "11111111", ".111111.", "..1111.."},
		Hand:    []string{"..2..", ".111.", "11111", "11111", ".111."},
		Residue: "res-apple-core", ResRows: []string{"..1..", ".1.1.", "1.1.1", "..1.."}},
	{Key: "food-peach", Name: "桃子", Hex: "#f2a3b8", Hex2: "#4a7d52",
		Shelf:   []string{"...2....", "..1211..", ".111111.", "11111111", "11111111", "11111111", ".111111.", "..1111.."},
		Hand:    []string{"..2..", ".121.", "11111", "11111", ".111."},
		Residue: "res-peel", ResRows: []string{"..1..", ".1.1.", "1.1.1", ".1.1."}},
	{Key: "food-lemon", Name: "柠檬", Hex: "#f2c14e", Hex2: "#4a7d52",
		Shelf:   []string{"....2...", "..11111.", ".111111.", "11111111", "11111111", "11111111", ".111111.", "..1111.."},
		Hand:    []string{"..2..", ".111.", "11111", "11111", ".111."},
		Residue: "res-peel", ResRows: []string{".11..", "1.11.", ".11..", "..1.."}},
	{Key: "food-watermelon", Name: "西瓜", Hex: "#4a7d52", Hex2: "#d95f4e",
		Shelf:   []string{"...22...", "..2112..", ".211112.", "21111112", "21111112", ".211112.", "..2112..", "...22..."},
		Hand:    []string{"..2..", ".121.", "11111", "11111", ".111."},
		Residue: "res-rind", ResRows: []string{".1.1.", "11.11", ".111.", "1.1.1"}},
	{Key: "food-strawberry", Name: "草莓", Hex: "#d95f4e", Hex2: "#4a7d52",
		Shelf:   []string{"...2....", "..121...", ".11111..", "1111111.", "1111111.", ".11111..", "..111...", "...1...."},
		Hand:    []string{"..2..", ".121.", "11111", ".111.", "..1.."},
		Residue: "res-peel", ResRows: []string{"..1..", ".1.1.", "..1..", "....."}},
	{Key: "food-mango", Name: "芒果", Hex: "#ff8800", Hex2: "#d9b380",
		Shelf:   []string{"...2....", "..1121..", ".111121.", "11111121", "1111111.", ".111111.", "..1111..", "...11..."},
		Hand:    []string{"..2..", ".111.", "11111", "11111", ".111."},
		Residue: "res-peel", ResRows: []string{"..1..", ".11..", "11...", "....."}},
}

// Foods exports the catalog (32 entries: 22 snacks + 10 fruits).
var Foods = foods

// FoodByKey looks one entry up (nil when unknown).
func FoodByKey(key string) *FoodForm {
	for i := range foods {
		if foods[i].Key == key {
			return &foods[i]
		}
	}
	return nil
}

// DrawFood paints one food form's rows at (x, y) legacy-units with the
// entry's two colors — the shared raster for shelf/hand/residue forms
// ('1' → Hex, '2' → Hex2, '.' transparent). One row char = S×S native
// pixels (the office's legacy grid unit).
func DrawFood(img *image.NRGBA, rows []string, hex1, hex2 string, x, y int) {
	c1 := C(hex1)
	c2 := C(hex2)
	for ry, row := range rows {
		for rx := 0; rx < len(row); rx++ {
			ch := row[rx]
			if ch == '.' {
				continue
			}
			col := c1
			if ch == '2' {
				col = c2
			}
			FillRect(img, (x+rx)*S, (y+ry)*S, S, S, col)
		}
	}
}

// FoodCount reports the catalog size (sanity: 22 snacks + 10 fruits).
// 零食＝bag/can/box/stick 四型前缀；水果＝其余（apple/orange…）。
func FoodCount() (snacks, fruits int) {
	for _, f := range foods {
		pre := f.Key[len("food-"):]
		switch {
		case len(pre) > 4 && (pre[:4] == "bag-" || pre[:4] == "can-" || pre[:4] == "box-"):
			snacks++
		case len(pre) > 6 && pre[:6] == "stick-":
			snacks++
		default:
			fruits++
		}
	}
	return
}
