package dispatch

// gitlink.go — 版本管理（v2.7）的调度器半场：两条腿。
//
// ① BranchSwitched（切分支告知）：房主在面板切了工作区分支，服务端
// 的 GitBranchSwitched 钩子经 Fleet 路由到这里——正干活的成员脚下
// 的文件刚被换掉，TA 理应第一个知道。告知走 background 背景车道
// （Announce 静默发布的同款纪律）：不打断进行中的回合，缀在每位成
// 员下一条点名消息前头送达，无人被凭空唤醒。
//
// ② gitLineForLanded（任务发布的开工行）：【任务发布】注入的尾部带
// 一行「工作区分支：dev（领先远端 2）· 版本 v1 · 提交请走规范」——
// 开工时刻正好是送达分支事实与提交规范的最佳时机。非 git 项目（探
// 测不到仓库）静默省略，不惩罚不声明。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// BranchSwitched parks the branch-switch note in every SHARED-workspace
// member's background lane（主树切换只动共享者的脚下——隔离成员的专
// 属树纹丝不动，不瞎喊）. true = there are managed members at all;
// false = empty room（服务器把这变成日志里的一行，切分支本身早已
// 完成）.
func (d *Dispatcher) BranchSwitched(from, to string) bool {
	d.mu.Lock()
	names := make([]string, 0, len(d.members))
	for name := range d.members {
		names = append(names, name)
	}
	d.mu.Unlock()
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if d.BranchOf(name) != "" {
			continue // 隔离成员：文件没变，不打扰
		}
		d.background(name, branchSwitchedNote(from, to))
	}
	return true
}

// branchSwitchedNote renders the background note（背景车道，无需回
// 应的纪律与公告静默发布同款）。
func branchSwitchedNote(from, to string) string {
	return fmt.Sprintf(
		"【版本】房主把工作区分支从 %s 切到 %s——工作区文件已随之切换，后续工作以新分支为准（提交请带 t_NN/r_NN 号）。无需回应。",
		branchWord(from), branchWord(to))
}

// WorkspaceUpdated is the pull's member leg（v2.14 远端同步）：房主把
// 共享主工作区快进到上游最新——共享成员脚下的文件变了，与切分支同
// 一性质的事实通报，背景车道送到每位主树成员（不唤醒回合）；隔离成
// 员的专属树纹丝不动，不打扰。true = there are managed members at
// all; false = empty room（服务器把这变成日志里的一行，快进本身早已
// 落定）。
func (d *Dispatcher) WorkspaceUpdated(branch string, incoming int) bool {
	d.mu.Lock()
	names := make([]string, 0, len(d.members))
	for name := range d.members {
		names = append(names, name)
	}
	d.mu.Unlock()
	if len(names) == 0 {
		return false
	}
	note := fmt.Sprintf(
		"【版本】房主把工作区在分支 %s 上快进到远端最新（%d 个提交进来）——脚下文件已更新，继续手头工作前请留意新内容（提交请带 t_NN/r_NN 号）。无需回应。",
		branchWord(branch), incoming)
	for _, name := range names {
		if d.BranchOf(name) != "" {
			continue // 隔离成员：文件没变，不打扰
		}
		d.background(name, note)
	}
	return true
}

// branchWord renders a branch name for member notes（游离 HEAD 如实说）。
func branchWord(b string) string {
	if b == "" {
		return "游离 HEAD"
	}
	return b
}

// gitLineForLanded renders the 任务发布 tail line: the assignee's own
// branch（v2.7.1 分支隔离——隔离成员读其专属树，共享成员读主工作
// 区）with ahead note, the landed tasks' version when they carry one,
// and the commit-convention one-liner. ""（非 git 项目、探测失败、游离
// HEAD）= 不追加——一行都不多话。
func (d *Dispatcher) gitLineForLanded(assignee string, landed []*tasks.Task) string {
	ws := d.cfg.Workspace
	if assignee != "" {
		if wt, ok := d.TreeOf(assignee); ok {
			ws = wt
		}
	}
	if ws == "" {
		return ""
	}
	ctx := context.Background()
	repo, err := vcs.Detect(ctx, ws)
	if err != nil {
		return ""
	}
	st, err := repo.Status(ctx)
	if err != nil || st.Branch == "" {
		return ""
	}
	branch := st.Branch
	if st.Ahead > 0 {
		branch += fmt.Sprintf("（领先远端 %d）", st.Ahead)
	}
	line := "工作区分支：" + branch
	if v := landedVersion(landed); v != "" {
		line += " · 版本 " + v
	}
	line += " · 提交请走 type(scope): 摘要，带上任务号/需求号（t_NN/r_NN）"
	return line
}

// --- 分支隔离（v2.7.1：按成员的 git worktree）--------------------------------
//
// 座位 Branch 非空即隔离：会话生在 ~/.niuma/wt/<项目>/<人名> 的专属工
// 作树、驻该分支。两个关键语义：①会话 cwd 固定在目录上——树内换分支
// 不动会话；②跨树迁移（Branch 空↔非空）必须换会话——Rebirth 编排
// 「解绑旧会话→新会话生在树上」，历史/任务/车道无损续接。

// birthWorkspace resolves where this member's next session should be
// born: the isolated worktree when the seat carries a Branch, the
// shared project workspace otherwise（v1 无编制形态恒共享）。建树失
// 败如实报错——绝不静默落回共享主树。
func (d *Dispatcher) birthWorkspace(name string) (string, error) {
	if d.staff == nil {
		return d.cfg.Workspace, nil
	}
	row, ok := d.staff.Get(d.projectKey, name)
	if !ok || !row.Occupying() {
		return d.cfg.Workspace, nil
	}
	if row.Branch == "" {
		// 自动驻分支（AutoSeat 主动档）：座位空且开关开着 → 出生即各
		// 驻专属枝 wt/<人名>（自基线切出）。落账后建树，建树失败回滚
		// 落账（SeatBranch 的「账实不符比失败更糟」同款）；绝不静默
		// 落回共享主树——「以为隔离其实共享」是最坏状态。
		if d.cfg.AutoSeatProbe != nil {
			if on, base := d.cfg.AutoSeatProbe(); on {
				branch := "wt/" + name
				if _, err := d.staff.SetBranch(d.projectKey, name, branch); err != nil {
					return "", err
				}
				wt, err := d.memberTreeEnsure(name, branch, base)
				if err != nil {
					_, _ = d.staff.SetBranch(d.projectKey, name, "") // 回滚落账
					return "", err
				}
				return wt, nil
			}
		}
		return d.cfg.Workspace, nil // 座位空且未开自动驻枝：照旧共享主树
	}
	// 总开关关着＝分支隔离整面停：座位还挂着分支就出生，是「以为隔
	// 离其实共享」的最坏状态——如实拒绝并指路（同 memberTree 的非仓
	// 库路障口径：出路写进行里）。
	if d.cfg.GitOffProbe != nil && d.cfg.GitOffProbe() {
		return "", errors.New(i18n.Sf("项目已关闭 git 版本管理，%s 的分支隔离（%s）无法落树——到设置中心·项目页重新打开总开关，或把 %s 设回主工作区", name, row.Branch, name))
	}
	return d.memberTree(name, row.Branch)
}

// memberTreeEnsure = memberTree ＋ 缺失分支自基线切出（自动驻枝与补驻
// 的出生路径用；手动设分支的路径分支在座位面已确保存在）。基线缺位
// 时先自 HEAD 切基线（ensureSeatBranch 同款）。
func (d *Dispatcher) memberTreeEnsure(name, branch, base string) (string, error) {
	if base == "" {
		base = "main"
	}
	repo, err := vcs.Detect(context.Background(), d.cfg.Workspace)
	if err == nil {
		ctx := context.Background()
		if _, berr := repo.EnsureBranch(ctx, base, ""); berr != nil {
			return "", berr
		}
		if _, berr := repo.EnsureBranch(ctx, branch, base); berr != nil {
			return "", berr
		}
	} else if !errors.Is(err, vcs.ErrNotRepo) {
		return "", err // 非仓库形态交给 memberTree 出它的指路文案
	}
	return d.memberTree(name, branch)
}

// memberTree ensures the member's isolated worktree exists AT branch
// and returns its path: 幂等建树（已是工作树直接用），树已在但驻枝不
// 同→树内 switch（脏树/抢枝由 git 拒绝，文案透传）。
func (d *Dispatcher) memberTree(name, branch string) (string, error) {
	if d.cfg.WorktreeRoot == "" {
		return "", errors.New(i18n.Sf("分支隔离未配置工作树根目录——%s（分支 %s）的会话无法落树", name, branch))
	}
	ctx := context.Background()
	repo, err := vcs.Detect(ctx, d.cfg.Workspace)
	if err != nil {
		if errors.Is(err, vcs.ErrNotRepo) {
			// 出路写进行里：座位分支槽悬在已非仓库的工作区上时，这是
			// 该成员出生的唯一路障——指到能解开它的那两个开关。
			return "", errors.New(i18n.Sf("项目工作区不是 git 仓库，%s 的分支隔离（%s）无从落树——先到版本管理面板把 %s 设回主工作区，或把工作区恢复成 git 仓库", name, branch, name))
		}
		return "", err
	}
	wt := filepath.Join(d.cfg.WorktreeRoot, d.projectKey, name)
	if err := repo.AddWorktree(ctx, wt, branch, ""); err != nil {
		return "", errors.New(i18n.Sf("为 %s 建工作树失败：%v", name, err))
	}
	// v2.9 项目隔离：新树即刻记进工作区→项目绑定表——这棵树上的会话
	// CLI 从 cwd 查表得到本项目缺省范围。失败只影响读面缺省，不挡建树。
	if err := util.BindWorkspaceProject(wt, d.projectKey); err != nil {
		log.Printf("[调度] %s 的新工作树 %s 绑定失败（只影响 CLI 读面缺省）：%v", name, wt, err)
	}
	// 树已在但驻枝不同 → 树内 switch：会话 cwd 指向目录，分支随驻。
	if wtr, err := vcs.Detect(ctx, wt); err == nil {
		if st, serr := wtr.Status(ctx); serr == nil && st.Branch != branch {
			if cerr := wtr.Checkout(ctx, branch); cerr != nil {
				return "", errors.New(i18n.Sf("把 %s 的工作树切到 %s 失败：%v", name, branch, cerr))
			}
		}
	}
	return wt, nil
}

// TreeOf returns the member's isolated worktree path when it exists on
// disk（只查不建——seats 读面/发布行取分支用）。
func (d *Dispatcher) TreeOf(name string) (string, bool) {
	if d.cfg.WorktreeRoot == "" || d.staff == nil {
		return "", false
	}
	row, ok := d.staff.Get(d.projectKey, name)
	if !ok || row.Branch == "" {
		return "", false
	}
	wt := filepath.Join(d.cfg.WorktreeRoot, d.projectKey, name)
	if st, err := os.Stat(wt); err == nil && st.IsDir() {
		return wt, true
	}
	return "", false
}

// BranchOf returns the seat's branch slot（""=共享主工作树）。
func (d *Dispatcher) BranchOf(name string) string {
	if d.staff == nil {
		return ""
	}
	if row, ok := d.staff.Get(d.projectKey, name); ok {
		return row.Branch
	}
	return ""
}

// SeatBranch is the 版本管理面板「设分支」的调度器执行体：落账
// （staffing.SetBranch）后按迁移类型执行——树内换枝不动会话（cwd 指
// 目录），跨树迁移（空↔非空）走 Rebirth 重生会话。落树/换枝失败回
// 滚落账（账实不符比失败更糟）；Rebirth 失败不回滚（树已就位、分支
// 已设定，重生成可以重试——错误文案指路）。
func (d *Dispatcher) SeatBranch(person, branch string) error {
	if d.staff == nil {
		return errors.New(i18n.S("无编制表（v1 形态不支持座位分支）"))
	}
	row, ok := d.staff.Get(d.projectKey, person)
	if !ok || !row.Occupying() {
		return errors.New(i18n.Sf("%s 不在 %s 的编制里", person, d.projectKey))
	}
	old := row.Branch
	if old == branch {
		return nil
	}
	if _, err := d.staff.SetBranch(d.projectKey, person, branch); err != nil {
		return err
	}
	rollback := func() {
		_, _ = d.staff.SetBranch(d.projectKey, person, old)
	}
	attached := d.lookup(person) != nil
	switch {
	case branch == "":
		// 回主树：旧工作树保留（未合并工作是数据），会话重生到主树。
		if attached {
			if err := d.Rebirth(person, fmt.Sprintf(
				"【迁移】你的工作区已迁回项目共享主工作树（%s）——原分支 %s 的工作树原样保留，需要时房主可再设回。", d.cfg.Workspace, old)); err != nil {
				return errors.New(i18n.Sf("分支已清、会话重生失败（可重试 dispatch birth，或再设回 %s）：%v", old, err))
			}
		}
		return nil
	case attached && old != "":
		// 树内换枝：会话不动。
		if _, err := d.memberTree(person, branch); err != nil {
			rollback()
			return err
		}
		d.background(person, fmt.Sprintf(
			"【版本】你的工作树已切到分支 %s（原 %s）——后续工作以新分支为准（提交请带 t_NN/r_NN 号）。无需回应。", branch, old))
		return nil
	default:
		// 跨树迁移：先备树，再重生（未挂会话时只备树——下次出生即落树）。
		if _, err := d.memberTree(person, branch); err != nil {
			rollback()
			return err
		}
		if attached {
			if err := d.Rebirth(person, fmt.Sprintf(
				"【迁移】你的工作区已迁到专属分支树（分支 %s）——此后文件改动只影响你自己，与其他成员互不踩脚；历史与任务原样都在。", branch)); err != nil {
				return errors.New(i18n.Sf("分支已设定、树已就位，但会话重生失败（可重试 dispatch birth，或改回共享主树）：%v", err))
			}
		}
		return nil
	}
}

// Rebirth is the migration's session half: 座位（房间的在场）与会话
// （ZCode 的 cwd）本就是两回事——换树只动会话。新会话生在
// birthWorkspace 解析出的树上，就地换绑：折旧会话的侧栏行、座位绑
// 定指向新会话、事件桥重订阅；名册、车道、座位令牌全部原样不动（对
// 房间而言无人离开，历史与任务无损续接）。
func (d *Dispatcher) Rebirth(name, note string) error {
	if d.staff == nil {
		return errors.New(i18n.S("无编制表（v1 形态不支持迁移）"))
	}
	ws, err := d.birthWorkspace(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sessionID, err := d.bridge.CreateSession(ctx, ws, "yolo")
	if err != nil {
		return errors.New(i18n.Sf("为 %s 创建新会话失败：%v", name, err))
	}
	if _, err := d.bridge.Subscribe(ctx, sessionID); err != nil {
		return errors.New(i18n.Sf("订阅 %s 的新会话失败：%v", name, err))
	}
	d.mu.Lock()
	role := ""
	var fabric *capability.EffectiveFabric
	if m, ok := d.members[name]; ok {
		d.archiveTaskIndex(name, m.sessionID)
		m.sessionID = sessionID
		role, fabric = m.role, m.fab.fabric
		d.members[name] = m
	}
	d.mu.Unlock()
	if row, ok := d.staff.Get(d.projectKey, name); ok && role == "" {
		role = row.ProjectRole
	}
	d.selectBirthModel(ctx, sessionID, name, fabric, "", "")
	if _, err := d.staff.BindSession(d.projectKey, name, sessionID); err != nil {
		log.Printf("[调度] %s 迁移后会话绑定回写编制失败：%v", name, err)
	}
	d.registerTaskIndexAsync(name, role, sessionID, fabric)
	d.deliver(name, note, nil, nil)
	return nil
}

// MergeLanded is the merge's member leg（v2.8 gitflow）：工作室替房主
// 执行了 merge --no-ff 之后，把「你的分支已并入主线」送进该分支座位
// 主人的背景车道（不唤醒回合——收口是事实通报，不是新工作）。没有
// 座位驻在这根分支（已迁出/离职）时如实报错，调用方只记日志。
func (d *Dispatcher) MergeLanded(branch, into string) error {
	if d.staff == nil {
		return fmt.Errorf("无编制表（v1 形态无座位分支）")
	}
	person := ""
	for _, row := range d.staff.ListByProject(d.projectKey) {
		if row.Occupying() && row.Branch == branch {
			person = row.Person
			break
		}
	}
	if person == "" {
		return fmt.Errorf("没有座位驻在分支 %s（可能已迁出）", branch)
	}
	d.background(person, fmt.Sprintf(
		"【版本】你的分支 %s 已并入主线 %s（房主验收通过，合并提交已落库）——工作树无需动作，后续新工作可继续在此分支。无需回应。", branch, into))
	return nil
}

// patrolGitLines renders the patrol's 版本收口 section（v2.8 gitflow，
// L4）：两类信号——① 领先主线且主人无在途任务的座位分支（活干完了
// 该收口，该提交合并提案）；② 脏工作区（有未提交改动——含共享主树
// 自己）。非 git 项目一行不多话；读库失败静默跳过（巡检是提醒不是
// 审计）。
func (d *Dispatcher) patrolGitLines() string {
	if d.cfg.Workspace == "" {
		return ""
	}
	ctx := context.Background()
	repo, err := vcs.Detect(ctx, d.cfg.Workspace)
	if err != nil {
		return ""
	}
	st, err := repo.Status(ctx)
	if err != nil || st.Branch == "" {
		return ""
	}
	into := st.Branch
	var lines []string
	if dirty := st.TrackedDirty(); dirty > 0 {
		lines = append(lines, fmt.Sprintf("共享主树（%s）有 %d 处未提交改动", into, dirty))
	}
	if d.staff != nil {
		busy := map[string]bool{} // 有在途（todo/doing）任务的成员
		if d.cfg.Tasks != nil {
			for _, t := range d.cfg.Tasks.ListFiltered("", "", d.projectKey, "") {
				if (t.Status == tasks.StatusTodo || t.Status == tasks.StatusDoing) && t.Assignee != "" {
					busy[t.Assignee] = true
				}
			}
		}
		for _, row := range d.staff.ListByProject(d.projectKey) {
			if !row.Occupying() || row.Branch == "" || row.Branch == into {
				continue
			}
			merged, merr := repo.MergedInto(ctx, row.Branch, into)
			if merr != nil || merged {
				continue
			}
			n, aerr := repo.AheadCount(ctx, into, row.Branch)
			if aerr != nil || n <= 0 {
				continue
			}
			tag := "主人已无在途任务，该收口"
			if busy[row.Person] {
				tag = "主人尚有在途任务"
			}
			lines = append(lines, fmt.Sprintf("%s 的分支 %s 领先 %s %d 提交未并入（%s）", row.Person, row.Branch, into, n, tag))
			if wt, ok := d.TreeOf(row.Person); ok {
				if wrepo, werr := vcs.Detect(ctx, wt); werr == nil {
					if wst, wserr := wrepo.Status(ctx); wserr == nil && wst.TrackedDirty() > 0 {
						lines = append(lines, fmt.Sprintf("%s 的工作树有 %d 处未提交改动", row.Person, wst.TrackedDirty()))
					}
				}
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "；版本收口（未合并分支/脏工作区）：" + strings.Join(lines, "、")
}

// landedVersion picks the first non-empty version name among the landed
// tasks（同批多版本是建模少见态，取首个足够指路）。
func landedVersion(landed []*tasks.Task) string {
	for _, t := range landed {
		if t != nil && t.Version != "" {
			return t.Version
		}
	}
	return ""
}
