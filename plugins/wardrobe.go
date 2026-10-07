package plugins

// wardrobe.go — 形态 A（数据插件）的衣橱贡献：一个 JSON 文件声明发色
// 与发型条目，应用时把发型并进 pixart 渲染查表面（RegisterHairStyle-
// Addons）＋staffing 掷骰池（RegisterLookAddons），发色只并掷骰表
// （hex 值直通渲染）。键/色冲突与超界条目逐条跳过并记入 Problems——
// 一件坏衣服不打翻一整包。
//
// 语义提醒（manual 同口径）：终身层在成员入编/首次入座时掷骰并终身
// 锁定，插件衣橱只影响之后的新面孔；已落档的牛马不换脸。

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/pixart"
	"github.com/WWestC/Niuma_Studio/staffing"
)

var hexRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type wardrobeFile struct {
	HairColors []wardrobeHairColor `json:"hair_colors"`
	HairStyles []wardrobeHairStyle `json:"hair_styles"`
}

type wardrobeHairColor struct {
	Key    string `json:"key"`    // 条目身份（审计/去重用；掷骰表以 hex 为准）
	Name   string `json:"name"`   // 展示名（≤12 字）
	Hex    string `json:"hex"`    // #rrggbb
	Weight int    `json:"weight"` // 万分比，1–9999（黑 9900 的口径）
}

type wardrobeHairStyle struct {
	Key      string `json:"key"` // pixart 衣橱键，唯一
	Name     string `json:"name"`
	Fringe   int    `json:"fringe"`   // 留海 0 无 1 平 2 侧分 3 曲齿
	Sideburn int    `json:"sideburn"` // 鬓角 0 标准 1 长鬓 2 及肩
	Volume   int    `json:"volume"`   // 蓬度 0 平顶 1 标准 2 蓬松
	Back     int    `json:"back"`     // 背面 0 无 1 马尾 2 长发 3 尖刺 4 削薄
	Weight   int    `json:"weight"`   // 掷骰权重，1–99（基表锚点款 6–26、派生款 1）
}

// applyWardrobeFile loads one wardrobe JSON and registers it under src.
// Re-applying the same src replaces its previous contribution wholesale;
// every failure path withdraws first (a broken file must not leave the
// previous good version running).
func applyWardrobeFile(src, path string) (problems []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		withdrawWardrobe(src)
		return []string{i18n.Sf("衣橱文件 %s 不可读：%v", path, err)}
	}
	var wf wardrobeFile
	if err := json.Unmarshal(data, &wf); err != nil {
		withdrawWardrobe(src)
		return []string{i18n.Sf("衣橱文件 %s 不是合法 JSON：%v", path, err)}
	}
	if len(wf.HairColors) == 0 && len(wf.HairStyles) == 0 {
		withdrawWardrobe(src)
		return []string{i18n.Sf("衣橱文件 %s 没有任何条目", path)}
	}

	var pixDefs []pixart.HairStyleDef
	var styles []staffing.HairStyleAddon
	for i, h := range wf.HairStyles {
		where := fmt.Sprintf("hair_styles[%d]", i)
		switch {
		case !itemKeyRE.MatchString(h.Key):
			problems = append(problems, i18n.Sf("%s.key %q 不合法（小写字母/数字/连字符，≤32）", where, h.Key))
			continue
		case pixart.HairStyleKeyTaken(src, h.Key):
			problems = append(problems, i18n.Sf("%s：发型键 %q 与内置或其他插件撞车，跳过", where, h.Key))
			continue
		case h.Fringe < 0 || h.Fringe > 3 || h.Sideburn < 0 || h.Sideburn > 2 ||
			h.Volume < 0 || h.Volume > 2 || h.Back < 0 || h.Back > 4:
			problems = append(problems, i18n.Sf("%s：轴参数超界（留海0-3 鬓角0-2 蓬度0-2 背面0-4）", where))
			continue
		case h.Weight <= 0 || h.Weight > 99:
			problems = append(problems, i18n.Sf("%s：weight 需在 1–99（建议 1–3，别盖过锚点款）", where))
			continue
		}
		pixDefs = append(pixDefs, pixart.HairStyleDef{
			Key: h.Key, Name: h.Name,
			Fringe: h.Fringe, Sideburn: h.Sideburn, Volume: h.Volume, Back: h.Back,
		})
		styles = append(styles, staffing.HairStyleAddon{Key: h.Key, Weight: h.Weight})
	}

	var colors []staffing.HairColorAddon
	for i, c := range wf.HairColors {
		where := fmt.Sprintf("hair_colors[%d]", i)
		switch {
		case !itemKeyRE.MatchString(c.Key):
			problems = append(problems, i18n.Sf("%s.key %q 不合法", where, c.Key))
			continue
		case !hexRE.MatchString(c.Hex):
			problems = append(problems, i18n.Sf("%s：hex 需形如 #rrggbb，得到 %q", where, c.Hex))
			continue
		case staffing.HairHexTaken(src, c.Hex):
			problems = append(problems, i18n.Sf("%s：发色 %s 与内置或其他插件重复，跳过", where, c.Hex))
			continue
		case c.Weight <= 0 || c.Weight > 9999:
			problems = append(problems, i18n.Sf("%s：weight 需在 1–9999（万分比；稀有建议 1–30）", where))
			continue
		}
		colors = append(colors, staffing.HairColorAddon{Name: c.Name, Hex: c.Hex, PerMyriad: c.Weight})
	}

	// Render table first: its accepted-key list tells the roll pool what
	// actually landed (a race could steal a key between check and register).
	accepted := pixart.RegisterHairStyleAddons(src, pixDefs)
	ok := map[string]bool{}
	for _, k := range accepted {
		ok[k] = true
	}
	rolled := styles[:0]
	for _, s := range styles {
		if ok[s.Key] {
			rolled = append(rolled, s)
		}
	}
	staffing.RegisterLookAddons(src, colors, rolled)
	return problems
}

// withdrawWardrobe pulls one source's contribution out of both merge
// surfaces (uninstall / disable / broken-file paths).
func withdrawWardrobe(src string) {
	pixart.RegisterHairStyleAddons(src, nil)
	staffing.RegisterLookAddons(src, nil, nil)
}
