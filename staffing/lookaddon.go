package staffing

// lookaddon.go — v3 插件系统（形态 A）的终身层贡献入口：插件往掷骰池
// 里加发色/发型。基表（hairColorTable / hairStylePool）保持不动，插件
// 条目按来源（source）登记、整体替换——同一插件重复 Apply 幂等，卸载
// 换装不会残留。键值合法性由调用方（plugins 包）把关，这里只管并表。
//
// 并发口径：掷骰（RollLook / RollHairColor）与 /plugins.json 触发的
// 重新登记可能交错，lookAddonMux 统一护住——掷骰走快照（浅拷贝）后
// 无锁完成，登记独占写。

import "sync"

// HairColorAddon 是一条插件发色：万分比权重与基表同口径。
type HairColorAddon struct {
	Name      string
	Hex       string
	PerMyriad int
}

// HairStyleAddon 是一条发型掷骰权重（Key 是 pixart 衣橱键）。
type HairStyleAddon struct {
	Key    string
	Weight int
}

type lookAddons struct {
	colors []HairColorAddon
	styles []HairStyleAddon
}

var (
	lookAddonMux     sync.RWMutex
	lookAddonSources = map[string]*lookAddons{}
)

// RegisterLookAddons 整体替换一个来源的发色/发型贡献（source 通常
// 形如 "plugin:<id>:<相对路径>"）。传空切片即撤下该来源。
func RegisterLookAddons(source string, colors []HairColorAddon, styles []HairStyleAddon) {
	lookAddonMux.Lock()
	defer lookAddonMux.Unlock()
	lookAddonSources[source] = &lookAddons{colors: colors, styles: styles}
}

// HairHexTaken reports whether a hair hex is already in the base table
// or any addon source other than skipSource ("" checks every source) —
// duplicate hexes would double their roll weight, so contributors get
// pre-checked instead.
func HairHexTaken(skipSource, hex string) bool {
	lookAddonMux.RLock()
	defer lookAddonMux.RUnlock()
	for _, t := range hairColorTable {
		if t.Hex == hex {
			return true
		}
	}
	for src, a := range lookAddonSources {
		if src == skipSource {
			continue
		}
		for _, c := range a.colors {
			if c.Hex == hex {
				return true
			}
		}
	}
	return false
}

// styleRollPool 快照基表＋全部插件贡献的发型掷骰池。
func styleRollPool() []struct {
	Key    string
	Weight int
} {
	lookAddonMux.RLock()
	defer lookAddonMux.RUnlock()
	out := make([]struct {
		Key    string
		Weight int
	}, 0, len(hairStylePool)+8)
	out = append(out, hairStylePool...)
	for _, src := range lookAddonSources {
		for _, s := range src.styles {
			out = append(out, struct {
				Key    string
				Weight int
			}{s.Key, s.Weight})
		}
	}
	return out
}

// colorRollTable 快照基表＋全部插件贡献的发色概率表。
func colorRollTable() []struct {
	Hex       string
	PerMyriad int
} {
	lookAddonMux.RLock()
	defer lookAddonMux.RUnlock()
	out := make([]struct {
		Hex       string
		PerMyriad int
	}, 0, len(hairColorTable)+8)
	out = append(out, hairColorTable...)
	for _, src := range lookAddonSources {
		for _, c := range src.colors {
			out = append(out, struct {
				Hex       string
				PerMyriad int
			}{c.Hex, c.PerMyriad})
		}
	}
	return out
}
