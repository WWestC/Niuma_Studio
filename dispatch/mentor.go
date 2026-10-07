// mentor.go — r_28（t_221）：导师指派的代码化判定。定稿（HR 手册 rev
// 11「新成员到岗引导」节）三条规则：
//   - 同岗位在岗成员自动为导师；多人时选**最近完工者**（台账最后一次
//     done——刚走完全流程记忆最新鲜，比随机/资历排序有实质理由）；
//   - 无同岗 → HR 兼任（文案带 HR 行动指引：主动 @新人 一次问首单
//     理解与卡点——兼任不是挂名是兜底动作）；
//   - 一次性无状态：结果只进 birth 注入文本，不建数据结构不持久化
//     （「答一次疑不陪跑」的轻定义决定了它不值得存储）。
//
// 放 dispatch 侧的三个理由（评审定案）：birth 注入组装就在本包（指派
// 结果直进注入文本零跨层传递）；roster 查询（lookup）现成；autopilot
// 补员走同一通道——代码化判定放这里才能被自动补员复用（HR 也是 AI，
// 手册指引不可执行）。
package dispatch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// pickMentor scans the live roster for a same-role member (excluding the
// newcomer themselves). Same-role many → the most recent done's assignee
// (the freshest memory of the whole flow). hrFallback reports the
// no-same-role branch (the caller renders the HR-兼任 line).
func (d *Dispatcher) pickMentor(newcomer, role string) (mentor string, hrFallback bool) {
	if role == "" {
		return "", true // no role to match — the HR fallback is the only mentor
	}
	candidates := []string{}
	d.mu.Lock()
	for _, m := range d.members {
		if m.name == newcomer || m.role != role {
			continue
		}
		candidates = append(candidates, m.name)
	}
	d.mu.Unlock()
	if len(candidates) == 0 {
		return "", true
	}
	if len(candidates) == 1 {
		return candidates[0], false
	}
	// 多人：最近完工者（台账最后一次 done 的 UpdatedTS；无 done 记录
	// 的沉底）。平手按名字序稳定（测试可复现）。
	lastDone := map[string]int64{}
	if d.cfg.Tasks != nil {
		for _, t := range d.cfg.Tasks.ListFiltered("", tasks.StatusDone, "", "") {
			if ts := t.UpdatedTS; ts > lastDone[t.Assignee] {
				lastDone[t.Assignee] = ts
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		ti, tj := lastDone[candidates[i]], lastDone[candidates[j]]
		if ti != tj {
			return ti > tj
		}
		return candidates[i] < candidates[j]
	})
	return candidates[0], false
}

// mentorLine renders the mentor assignment for the birth injection:
// the newcomer's first-input carries who to ask (once, before their
// first task starts — 不陪跑), or the HR-兼任 line with the HR's own
// action spelled out (兼任不是挂名).
func (d *Dispatcher) mentorLine(newcomer, mentor, role string) string {
	if mentor != "" {
		return fmt.Sprintf(
			"【导师】你的导师是 @%s（同岗位在岗前辈，最近完工者）——第一张单开工前可问一次疑（导师只答疑不陪跑）。答疑后导师会在 room-log 留痕。",
			mentor)
	}
	// HR 兼任：文案面向双方——新人知道找谁，HR（也是 AI）读得到自己
	// 该干嘛（主动 @新人 一次）。找在岗 HR 名字；无在岗 HR 也不空挂——
	// 指路到组呼。
	hrName := ""
	d.mu.Lock()
	for _, m := range d.members {
		if m.role == agents.HRRole && m.name != newcomer {
			hrName = m.name
			break
		}
	}
	d.mu.Unlock()
	if hrName != "" {
		return fmt.Sprintf(
			"【导师】本岗位暂无在岗前辈，由 HR @%s 兼任导师——HR 会主动来 @你 一次（问首单理解与卡点）；你也可以在第一张单开工前直接问 %s。导师只答疑不陪跑。",
			hrName, hrName)
	}
	return strings.Join([]string{
		"【导师】本岗位暂无在岗前辈、HR 也不在岗——导师暂缺。",
		"卡住时：编排找 @排期编排组、验收口径查 manual 验收纪律库；导师位在 HR 到岗后补上。",
	}, "\n")
}
