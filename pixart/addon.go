package pixart

// addon.go — v3 插件系统（形态 A）在渲染侧的贡献入口：插件发型条目
// 登记进 HairStyleByKey 的查表面（基表 HairStyles 不动）。发色不需要
// 这一层——hex 值直通渲染；掷骰侧的合并见 staffing/lookaddon.go。
//
// 冲突口径：键与基表或其他来源撞车即跳过该条（登记方在 plugins 包
// 已先行校验，这里是最后防线）。同一来源重复登记整体替换，幂等。

import "sync"

var (
	addonMux       sync.RWMutex
	addonHairBySrc = map[string][]HairStyleDef{}
)

// RegisterHairStyleAddons 整体替换一个来源（source）贡献的发型；
// 返回实际收下的键列表（撞键跳过的不在内）。传空切片即撤下该来源。
func RegisterHairStyleAddons(source string, defs []HairStyleDef) []string {
	addonMux.Lock()
	defer addonMux.Unlock()
	delete(addonHairBySrc, source)
	taken := map[string]bool{}
	for i := range HairStyles {
		taken[HairStyles[i].Key] = true
	}
	for _, list := range addonHairBySrc {
		for _, d := range list {
			taken[d.Key] = true
		}
	}
	kept := make([]HairStyleDef, 0, len(defs))
	keys := make([]string, 0, len(defs))
	for _, d := range defs {
		if taken[d.Key] {
			continue
		}
		taken[d.Key] = true
		kept = append(kept, d)
		keys = append(keys, d.Key)
	}
	if len(kept) > 0 {
		addonHairBySrc[source] = kept
	}
	return keys
}

// HairStyleKeyTaken reports whether key is claimed by the base table or
// any addon source other than skipSource ("" checks every source) —
// the plugins package uses it to pre-check conflicts without fighting
// its own previous registration.
func HairStyleKeyTaken(skipSource, key string) bool {
	addonMux.RLock()
	defer addonMux.RUnlock()
	for i := range HairStyles {
		if HairStyles[i].Key == key {
			return true
		}
	}
	for src, list := range addonHairBySrc {
		if src == skipSource {
			continue
		}
		for _, d := range list {
			if d.Key == key {
				return true
			}
		}
	}
	return false
}

// addonHairStyleByKey 在插件贡献里查一个发型键（调用方不持锁）。
func addonHairStyleByKey(key string) *HairStyleDef {
	for _, list := range addonHairBySrc {
		for i := range list {
			if list[i].Key == key {
				return &list[i]
			}
		}
	}
	return nil
}
