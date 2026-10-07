package server

import (
	"fmt"
	"testing"
)

// t_127：dayHash 的确定性——同人同日同衣、异日异衣（大概率）、同人不同日独立。
func TestDayHashDeterministic(t *testing.T) {
	a := dayHash("小猿", "2026-10-02")
	b := dayHash("小猿", "2026-10-02")
	if a != b {
		t.Fatalf("同人同日应同衣: %d != %d", a, b)
	}
	// 100 天内每天的同仁衣索引分布——不卡具体值，卡「至少两天不同」
	seen := map[uint32]bool{}
	for d := 1; d <= 10; d++ {
		seen[dayHash("小猿", fmt.Sprintf("2026-10-%02d", d))%uint32(len(wardrobe))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("10 天里衣柜应当轮换（至少 2 件），实际 %d 件", len(seen))
	}
	// 不同成员同日不同衣（大概率，偶撞允许）
	diff := false
	for _, n := range []string{"小牛", "小马", "小狐", "小鹿"} {
		if dayHash(n, "2026-10-02")%uint32(len(wardrobe)) != dayHash("小猿", "2026-10-02")%uint32(len(wardrobe)) {
			diff = true
			break
		}
	}
	if !diff {
		t.Fatal("五名成员同日全撞同件衣服——衣柜哈希分布异常")
	}
}

// t_127：无 day 参数＝衣柜语义稳定（spec 合成不 panic 且上衣是合法色）。
func TestAvatarSpecForNoDay(t *testing.T) {
	s := &Server{}
	spec := s.avatarSpecFor("小猿", "")
	if spec.Shirt == "" || spec.Hair == "" {
		t.Fatalf("基础 spec 缺色: %+v", spec)
	}
	// 无档案成员：HairStyle 空（= F1 标准渲染）、无饰品
	if spec.HairStyle != "" || len(spec.Acc) != 0 {
		t.Fatalf("无档案成员应退标准型: %+v", spec)
	}
}

// t_128 并轨评审附验：wardrobe 达标 100 件（10 版型 × 10 色）且
// dayHash 分布覆盖足够宽（同人 30 天至少 8 件不同）。
func TestWardrobeHundred(t *testing.T) {
	if n := len(wardrobe); n != 100 {
		t.Fatalf("衣柜应 100 件（10 版型×10 色），实际 %d", n)
	}
	seen := map[int]bool{}
	for d := 1; d <= 30; d++ {
		seen[int(dayHash("小猿", fmt.Sprintf("2026-10-%02d", d)))%len(wardrobe)] = true
	}
	if len(seen) < 8 {
		t.Fatalf("30 天仅轮到 %d 件，分布过窄", len(seen))
	}
}
