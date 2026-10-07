package gitops

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// gitBootResult is the bootstrap's honest receipt（随 POST /projects 应
// 答回给表单）：做了什么/跳过了什么/为什么，一句不瞒。
type BootResult struct {
	OK        bool     `json:"ok"`                    // 引导按计划完成（无计划＝true）
	Repo      bool     `json:"repo"`                  // 引导后工作区是不是（成了）git 仓库
	Root      string   `json:"root,omitempty"`        // 仓库根（外层仓拒绝时不给）
	Base      string   `json:"base_branch,omitempty"` // 生效的基线分支
	Initiated bool     `json:"initiated,omitempty"`   // 本次新建了仓库（含空首提交）
	Hooked    bool     `json:"hooked,omitempty"`      // 直装了两枚自家钩子（仅新建仓）
	Notes     []string `json:"notes,omitempty"`       // 跳过/失败的原因，逐条如实
}

func (r *BootResult) note(format string, args ...any) {
	r.Notes = append(r.Notes, i18n.Sf(format, args...))
}

// bootstrapProjectGit runs the create-time git plan against the project's
// workspace. 引导失败不炸立项：草稿已在册，结果如实带回 notes（表单
// toast 给房主看），该手工补的步骤一目了然。
func (g *Face) BootstrapProjectGit(p projects.Project) BootResult {
	res := BootResult{OK: true}
	plan := p.GitPlan
	ctx := context.Background()

	// 两级门顶层：总开关关着的项目引导整档跳过（git 面在面板开开关后
	// 才可用；届时可经 POST /p/{key}/git/settings 补配置）。
	if p.GitDisabled {
		res.note("git 版本管理未开启——立项引导跳过（面板打开总开关后可再补）")
		return res
	}

	fi, err := os.Stat(p.Workspace)
	switch {
	case err != nil && errors.Is(err, os.ErrNotExist):
		if plan == nil || !plan.InitIfMissing {
			res.note("目录还不存在——未选「自动 init」，引导跳过（可先建目录再开张，或编辑项目补引导）")
			return res
		}
		repo, ierr := vcs.InitRepo(ctx, p.Workspace, plan.BaseOrDefault(), i18n.Sf("chore: 项目初始化（%s，牛马工作室立项）", p.Key))
		if ierr != nil {
			res.OK = false
			res.note("自动 init 未成：%v", ierr)
			return res
		}
		res.Repo, res.Root, res.Base, res.Initiated = true, repo.Root, plan.BaseOrDefault(), true
		// 新仓全是自家地界（不可能有外来钩子位之争）：直装两枚钩子，
		// 提交播报与关单验提交从第一笔提交就接线。
		if _, _, werr := writeGitHooks(repo); werr != nil {
			res.note("钩子直装未成（可在版本管理面板补装）：%v", werr)
		} else {
			res.Hooked = true
		}
		return res
	case err != nil:
		res.OK = false
		res.note("目录读不了：%v", err)
		return res
	case !fi.IsDir():
		res.OK = false
		res.note("该路径不是目录（是个文件？）——引导无从落地")
		return res
	}

	repo, err := vcs.Detect(ctx, p.Workspace)
	if err != nil {
		if !errors.Is(err, vcs.ErrNotRepo) {
			res.OK = false
			res.note("仓库探测失败：%v", err)
			return res
		}
		// 目录在、仓库无：自动 init 或如实跳过。
		if plan != nil && plan.InitIfMissing {
			repo, err = vcs.InitRepo(ctx, p.Workspace, plan.BaseOrDefault(), i18n.Sf("chore: 项目初始化（%s，牛马工作室立项）", p.Key))
			if err != nil {
				res.OK = false
				res.note("自动 init 未成：%v", err)
				return res
			}
			res.Repo, res.Root, res.Base, res.Initiated = true, repo.Root, plan.BaseOrDefault(), true
			if _, _, werr := writeGitHooks(repo); werr != nil {
				res.note("钩子直装未成（可在版本管理面板补装）：%v", werr)
			} else {
				res.Hooked = true
			}
			return res
		}
		res.note("工作区不是 git 仓库——分支隔离与合并流不可用（编辑项目补引导，或终端手工 git init）")
		return res
	}

	// 仓库在。红线①：根必须恰是工作区、且非链接工作树——否则这是别人
	// 的地界（外层大仓可能装着用户的其他项目），自动操作一概不做。
	if !repoOwnedByWorkspace(repo, p.Workspace) {
		res.OK = false
		res.note("工作区位于外层 git 仓库内（根：%s）——自动引导不动外层仓库（那可能是别的项目的地界）；分支隔离请为本项目单独建仓，或继续手工安排", repo.Root)
		return res
	}
	res.Repo, res.Root = true, repo.Root
	if plan == nil {
		return res // 只带出仓库事实，不动任何东西
	}
	res.Base = plan.BaseOrDefault()
	// 只 EnsureBranch 基线：不切分支（用户在用的工作树一个文件不动）、
	// 已存在则幂等跳过；失败如实带回（基线缺位时座位建枝会再撞）。
	st, serr := repo.Status(ctx)
	if serr == nil && st.Branch == res.Base {
		return res
	}
	created, berr := repo.EnsureBranch(ctx, res.Base, "")
	if berr != nil {
		res.OK = false
		res.note("基线分支 %s 未成：%v", res.Base, berr)
	} else if created {
		res.note("基线分支 %s 已自当前 HEAD 切出（工作树未动，未切分支）", res.Base)
	}
	return res
}

// repoOwnedByWorkspace is 红线①的判定：仓库根恰是工作区（穿软链对齐
// ——macOS 的 /var ↔ /private/var 不算两棵树，否则同一棵树会被误判成
// 外层仓库、自动引导整档误停），且工作区不是某仓的链接工作树
// （worktree 的分支住在母仓账上，动它就是动母仓）。
func repoOwnedByWorkspace(repo *vcs.Repo, workspace string) bool {
	if repo == nil || repo.IsWorktree() {
		return false
	}
	return vcs.SamePath(repo.Root, workspace)
}

// gitFacts is the preflight's repo block（立项表单自动填的数据源）。
type gitFacts struct {
	IsRepo    bool   `json:"is_repo"`
	Root      string `json:"root,omitempty"`
	Subdir    bool   `json:"subdir,omitempty"`   // 工作区在外层仓库的子目录
	Worktree  bool   `json:"worktree,omitempty"` // 工作区本身是链接工作树
	Branch    string `json:"branch,omitempty"`   // 工作区当前分支（基线的自动填值）
	OwnsRepo  bool   `json:"owns_repo"`          // 根恰是工作区且非 worktree（自动引导可用）
	BaseFill  string `json:"base_fill,omitempty"`
	OuterRepo bool   `json:"outer_repo,omitempty"` // 外层仓库形态（自动引导停用的那档）
}

// probeGitFacts reads the workspace's repo state for the preflight face.
// 探测失败按「不是仓库」如实报，不猜。
func ProbeGitFacts(ctx context.Context, workspace string) gitFacts {
	f := gitFacts{}
	repo, err := vcs.Detect(ctx, workspace)
	if err != nil {
		return f
	}
	f.IsRepo, f.Root = true, repo.Root
	f.Subdir = !vcs.SamePath(repo.Root, workspace) // 软链形状不算外层（同一棵树）
	f.Worktree = repo.IsWorktree()
	f.OwnsRepo = !f.Subdir && !f.Worktree
	f.OuterRepo = !f.OwnsRepo
	if st, serr := repo.Status(ctx); serr == nil {
		f.Branch = st.Branch
	}
	if f.OwnsRepo {
		f.BaseFill = f.Branch // 自动引导可用的形态：当前分支就是基线的缺省值
	}
	return f
}

// ensureSeatBranch honors the project's AutoSeatBranch plan (v2.14) on
// the seat face: a branch that doesn't exist yet is cut from the plan's
// baseline instead of refused. 红线同上：仓库根必须恰是工作区（外层
// 仓库＝别的项目的地界，一概拒绝）；只建不删。refusal 的 error 文案
// 直接面向房主（plan 关着时回到既有口径，指路版本管理/立项开关）。
func (g *Face) ensureSeatBranch(key string, repo *vcs.Repo, branch string) (bool, error) {
	p, ok := g.Stores.ProjectStore.Get(key)
	if !ok {
		return false, errProjectNotFound(key)
	}
	plan := p.GitPlan
	if plan == nil || !plan.AutoSeatBranch {
		return false, errors.New(i18n.Sf("分支 %q 不在该仓库里（本地与远端都没有）——先在版本管理建枝，或立项时打开「成员分支自动新建」", branch))
	}
	if !repoOwnedByWorkspace(repo, p.Workspace) {
		return false, errors.New(i18n.S("工作区位于外层 git 仓库内——自动建枝不动外层仓库（那可能是别的项目的地界）；请手工建枝后再设"))
	}
	ctx := context.Background()
	base := plan.BaseOrDefault()
	if created, err := repo.EnsureBranch(ctx, base, ""); err != nil {
		return false, err
	} else if created {
		log.Printf("[版本] %s: 基线分支 %s 缺失，已自当前 HEAD 切出（座位建枝前置）", key, base)
	}
	return repo.EnsureBranch(ctx, branch, base)
}
