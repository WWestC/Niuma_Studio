// projects/gitlinks.go — 版本管理（v2.7）的绑定半场：分支是 git 里的
// 活物（fetch 就多、删掉就少），需求/版本是工作室台账里的实体；两者
// 的关联不该写进任何一边的本来账本，落在项目注册表这个「第三方」身
// 上正合适——BranchLinks 以分支名为键存工作室侧元数据，分支真身由
// git 面兜底（绑了一个后来删掉的分支只是显示层的一行孤儿，面板给
// 「清理」）。GitPolicy 同居于此：commit 规范的允许 type 表与引用强
// 制档，判定引擎在 vcs.Policy（面板徽章/CLI/钩子三处共用）。
package projects

import (
	"errors"
	"regexp"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// maxNoteRunes 是绑定备注的展示文本帽（house style：展示文本夹断、
// 标识符拒绝）。
const maxNoteRunes = 200

var reqKeyPattern = regexp.MustCompile(`^r_[0-9]+$`)

// BranchLink is one branch's studio-side binding: 需求（r_NN）与/或项
// 目版本（时间窗名）。全空的绑定没有意义，BindBranch 会拒绝；绑定的
// Version 名必须在该项目当前的 Versions 里（版本删了绑定会在下次
// 保存时被拒绝重写——显示层容忍存量，写入层不放新账）。
type BranchLink struct {
	Req     string `json:"req,omitempty"`     // 需求号 r_NN（可选）
	Version string `json:"version,omitempty"` // 项目版本名（可选，须在 p.Versions 内）
	Note    string `json:"note,omitempty"`
	BoundTS int64  `json:"bound_ts,omitempty"`
	BoundBy string `json:"bound_by,omitempty"` // 谁绑的（房主名，展示用）
}

// Link returns the branch's binding (nil when unbound) — 读侧便捷口。
func (p Project) Link(branch string) *BranchLink {
	if bl, ok := p.BranchLinks[branch]; ok {
		bl := bl
		return &bl
	}
	return nil
}

// clone returns the project with its BranchLinks map copied — Store 交
// 出与送回的实体都不与账本共享底层 map，外部改写串不进来（GitPolicy
// 是指针但它按不可变对待，写入永远整只替换）。
func (p Project) clone() Project {
	if p.BranchLinks == nil {
		return p
	}
	links := make(map[string]BranchLink, len(p.BranchLinks))
	for k, v := range p.BranchLinks {
		links[k] = v
	}
	p.BranchLinks = links
	return p
}

// BindBranch records (replaces wholesale) one branch's binding. 语义是
// 整条替换而非字段合并——前端浮层本来就编辑全量，合并只会让「解绑需
// 求」这一动作无从表达。
func (s *Store) BindBranch(key, branch string, link BranchLink) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	p := s.list[i]
	if p.Status == StatusArchived {
		return Project{}, errors.New(i18n.Sf("项目 %s 已归档，全只读", key))
	}
	if !vcs.ValidRefName(branch) {
		return Project{}, vcs.RefNameError("分支名", branch)
	}
	link.Req = strings.TrimSpace(link.Req)
	link.Version = strings.TrimSpace(link.Version)
	link.Note = clampRunes(strings.TrimSpace(link.Note), maxNoteRunes)
	link.BoundBy = sanitizeBy(link.BoundBy)
	if link.Req == "" && link.Version == "" && link.Note == "" {
		return Project{}, errors.New(i18n.S("绑定内容为空（需求/版本/备注至少其一）"))
	}
	if link.Req != "" && !reqKeyPattern.MatchString(link.Req) {
		return Project{}, errors.New(i18n.Sf("需求号须形如 r_12: %q", link.Req))
	}
	if link.Version != "" {
		found := false
		for _, v := range p.Versions {
			if v.Name == link.Version {
				found = true
				break
			}
		}
		if !found {
			return Project{}, errors.New(i18n.Sf("版本 %q 不在项目 %s 的版本列表里", link.Version, key))
		}
	}
	link.BoundTS = util.Now()
	links := make(map[string]BranchLink, len(p.BranchLinks)+1)
	for k, v := range p.BranchLinks { // 复制再写：Get/List 交出的浅拷贝不与账本共享底层数组
		links[k] = v
	}
	links[branch] = link
	p.BranchLinks = links
	s.list[i] = p
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}

// UnbindBranch drops one branch's binding. 分支不存在绑定也算成功
// （幂等）——与 git 删分支赛跑时，输掉的那次不该报假错。
func (s *Store) UnbindBranch(key, branch string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	p := s.list[i]
	if p.Status == StatusArchived {
		return Project{}, errors.New(i18n.Sf("项目 %s 已归档，全只读", key))
	}
	if _, ok := p.BranchLinks[branch]; !ok {
		return p.clone(), nil
	}
	links := make(map[string]BranchLink, len(p.BranchLinks))
	for k, v := range p.BranchLinks {
		if k != branch {
			links[k] = v
		}
	}
	if len(links) == 0 {
		links = nil // 归零时收走空 map，projects.json 不落 {} 噪音
	}
	p.BranchLinks = links
	s.list[i] = p
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}

// SetGitPolicy stores the project's commit-convention policy (nil 重置
// 回默认档——默认表与 either 见 vcs.Policy)。
func (s *Store) SetGitPolicy(key string, policy *vcs.Policy) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	p := s.list[i]
	if p.Status == StatusArchived {
		return Project{}, errors.New(i18n.Sf("项目 %s 已归档，全只读", key))
	}
	if policy != nil {
		if len(policy.CommitTypes) > 16 {
			return Project{}, errors.New(i18n.S("type 允许表过长（上限 16 个）"))
		}
		for _, t := range policy.CommitTypes {
			t = strings.TrimSpace(t)
			if len(t) == 0 || len(t) > 24 || !regexp.MustCompile(`^[a-z][a-z0-9_-]*$`).MatchString(t) {
				return Project{}, errors.New(i18n.Sf("type 名 %q 不合法（小写字母开头，[a-z0-9_-]，≤24 字符）", t))
			}
		}
		switch policy.RequireRef {
		case "", vcs.RefTask, vcs.RefReq, vcs.RefEither, vcs.RefOff:
		default:
			return Project{}, errors.New(i18n.Sf("require_ref 须是 task/req/either/off 之一: %q", policy.RequireRef))
		}
	}
	p.GitPolicy = policy
	s.list[i] = p
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}

// SetGitSettings writes the project's two-level git gate (总开关＋基线/
// 自动建枝/自动驻枝)：disabled 落 GitDisabled（顶层门），baseBranch 与
// autoSeatBranch/autoSeat 落 GitPlan 的三个运行期字段——这治了 v2.14
// 的缺口：GitPlan 此前立项时一次写死，老项目（功能落地前立项的）没有
// 任何口子补开「成员分支自动新建」。InitIfMissing 是立项时一次性动作，
// 这里刻意不碰（存量值原样保留）。归档全只读同 SetGitPolicy 家法。
func (s *Store) SetGitSettings(key string, disabled bool, baseBranch string, autoSeatBranch, autoSeat bool) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.findIndexLocked(key)
	if i < 0 {
		return Project{}, errors.New(i18n.Sf("项目不存在: %s", key))
	}
	p := s.list[i]
	if p.Status == StatusArchived {
		return Project{}, errors.New(i18n.Sf("项目 %s 已归档，全只读", key))
	}
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch != "" && !vcs.ValidRefName(baseBranch) {
		return Project{}, vcs.RefNameError(i18n.S("基线分支名"), baseBranch)
	}
	// 已有计划总能改（关自动建枝也要落账）；全空值不物化新计划——
	// nil=未配置 的账面语义留给从没碰过设置面的项目。
	if p.GitPlan != nil || baseBranch != "" || autoSeatBranch || autoSeat {
		plan := p.GitPlan
		if plan == nil {
			plan = &GitPlan{}
		}
		plan.BaseBranch = baseBranch
		plan.AutoSeatBranch = autoSeatBranch
		plan.AutoSeat = autoSeat
		p.GitPlan = plan
	}
	p.GitDisabled = disabled
	s.list[i] = p
	s.rev++
	s.saveLocked()
	return p.clone(), nil
}
