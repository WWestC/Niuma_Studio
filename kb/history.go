// history.go — r_26（t_212）：成员交付史的 JSON 出口。定稿 §四契约：
// GET /kb/people/{name}/history → {name, joined, lines[]（按需求线分组，
// done 时间倒序，每线一哈希色）}。履历页与未来消费者（人事/驾驶舱）的
// 共同数据面——复用台账 store 零新查询，纯读。
//
// 「履历非考核」边界（§三写死）：本端点只出分组事实，不出任何排序键
// 之外的度量（无速度/通过率/跨成员对比字段——r_18 红线在墙上）。
package kb

import (
	"sort"

	"github.com/WWestC/Niuma_Studio/util"
)

// HistoryDone is one delivered task inside a requirement line.
type HistoryDone struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	TS    int64  `json:"ts"` // done 的台账时刻（UpdatedTS）
}

// HistoryLine is one requirement line's delivery group (组内按 done 时间倒序).
type HistoryLine struct {
	Req     string        `json:"req"` // r_XX；空 = 未挂线的单（挂「其他」组）
	Project string        `json:"project,omitempty"`
	Title   string        `json:"title,omitempty"`
	Color   string        `json:"color"` // 需求号哈希色（跨页跨时间稳定——memberColor 同思路）
	Done    []HistoryDone `json:"done"`
}

// PersonHistory is the member's delivery dossier (定稿 §四形状).
type PersonHistory struct {
	Name   string        `json:"name"`
	Joined int64         `json:"joined,omitempty"` // 入职锚：首条台账时刻（该成员最早出现的事件）
	Lines  []HistoryLine `json:"lines"`
}

// historyPalette mirrors ui/dom.js SHIRT_PALETTE (chat.MemberColor 的
// 需求线版)：fnv1a-32 over the req id, same hex family——同线同色。
var historyPalette = []string{
	"#3370ff", "#00a9ff", "#00b392", "#34c724", "#7ac70c",
	"#ff8800", "#f54a45", "#f75cb4", "#9e6bff", "#5856d6",
}

// ReqColor hashes a requirement id to one stable palette hex (the
// 徽带色表's source — 前端与服务端同哈希同色，跨页跨时间稳定).
func ReqColor(req string) string {
	h := uint32(0x811c9dc5)
	for _, b := range []byte(req) {
		h ^= uint32(b)
		h *= 0x01000193
	}
	return historyPalette[h%uint32(len(historyPalette))]
}

// HistoryOf builds one member's delivery history off the task ledger:
// every done task grouped by its requirement line, newest delivery
// first inside a group, lines ordered by the group's latest delivery.
// joined is the member's earliest ledger moment (any task that names
// them — created/assigned/done 任一首次出现即入职锚的数据面近似).
func HistoryOf(ledger TaskLedger, reqs ReqTitler, name string) (PersonHistory, bool) {
	if ledger == nil {
		return PersonHistory{}, false
	}
	owned := ledger.WireTasksOf(name)
	if len(owned) == 0 {
		// 台账无痕也算合法（新成员）：空史也是史，joined 取 0 由前端降级
		return PersonHistory{Name: name, Lines: []HistoryLine{}}, true
	}
	joined := int64(0)
	byReq := map[string]*HistoryLine{}
	order := []string{}
	for _, t := range owned {
		if joined == 0 || (t.CreatedTS > 0 && t.CreatedTS < joined) {
			joined = t.CreatedTS
		}
		if t.Status != statusDone {
			continue
		}
		// v2.10：需求号分项目号段，履历按（项目, 需求）分组——两个项目
		// 各自的 r_01 是两条线，不并键；色哈希也吃限定键防同号同色。
		proj := shelfKey(t.ProjectKey)
		key := t.Req
		if key != "" {
			key = proj + "/" + t.Req
		}
		line, ok := byReq[key]
		if !ok {
			line = &HistoryLine{Req: t.Req, Project: proj, Color: ReqColor(key)}
			if t.Req != "" && reqs != nil {
				line.Title = reqs.ReqTitle(t.ProjectKey, t.Req)
			}
			byReq[key] = line
			order = append(order, key)
		}
		line.Done = append(line.Done, HistoryDone{ID: t.ID, Title: t.Title, TS: t.UpdatedTS})
	}
	// 组内 done 倒序；线序按组内最新交付（履历叙事：最近的线在前）
	out := make([]HistoryLine, 0, len(order))
	for _, k := range order {
		l := byReq[k]
		sort.SliceStable(l.Done, func(i, j int) bool { return l.Done[i].TS > l.Done[j].TS })
		out = append(out, *l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := int64(0), int64(0)
		if len(out[i].Done) > 0 {
			ti = out[i].Done[0].TS
		}
		if len(out[j].Done) > 0 {
			tj = out[j].Done[0].TS
		}
		return ti > tj
	})
	if joined == 0 {
		joined = util.Now() // 无 created_ts 的旧档：不造假锚，降级由前端处理
	}
	return PersonHistory{Name: name, Joined: joined, Lines: out}, true
}

// ReqTitler resolves one requirement id to its title inside its project
// (the requirements store's narrow face — ids are per-project shelves
// since v2.10; nil keeps titles empty, groups still render).
type ReqTitler interface {
	ReqTitle(projectKey, id string) string
}
