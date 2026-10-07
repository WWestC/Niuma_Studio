package vcs

// flow.go — 版本管理（v2.8 gitflow）的新原语半场：把「提交可见」与「合
// 并回主线」补进 v2.7 只读的面。四类原语：
//
//   - 看一笔提交：Show（消息＋逐文件红绿行）——提交播报卡的数据源；
//   - 量一段差异：DiffNumStat / AheadCount / MergedInto / LogRange——
//     合并提案的卡片与可并判定；
//   - 并回主线：MergeNoFF（--no-ff 保留分支拓扑，冲突即 abort 回滚，
//     绝不把半合并状态留给房主收拾）；
//   - 低风险预判：AdditiveOnly（三点差异里全是新增文件——与演化后的
//     主线不可能冲突的形状，autopilot 代收的准入线）。
//
// 纪律与 git.go 同源：一切经 runGit（超时兜墙钟、拒绝文案原文透传、
// 机器可读格式解析绝不猜）；写操作动工作区文件，超时同 switch 档。

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// FileChange is one path's diff row（--numstat 的一行）：Added/Removed 是
// 行数，Binary 标记二进制（numstat 给 "-\t-"）。
type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

// Show reads one commit's full message plus its per-file numstat — the
// commit-broadcast card's data source (谁提交的、说了什么、动了哪些文
// 件各多少行)。两次快照读（log -1 拿消息、show --numstat 拿文件行），
// 互不掺和。merge 提交的 numstat 是 combined 视角（冲突文件才出现），
// 如实呈现不修饰。
func (r *Repo) Show(ctx context.Context, sha string) (CommitMsg, []FileChange, error) {
	if !shaPattern.MatchString(sha) {
		return CommitMsg{}, nil, errors.New(i18n.Sf("非法提交号: %q（7–40 位十六进制）", sha))
	}
	msgs, err := parseLogMessages(runGit(ctx, r.Workspace, readTimeout,
		"log", "-1", "--format="+msgLogFormat, sha))
	if err != nil {
		return CommitMsg{}, nil, err
	}
	if len(msgs) == 0 {
		return CommitMsg{}, nil, errors.New(i18n.Sf("提交 %s 不在本仓库里（写错了、被 rebase 掉了，或不属于本仓库）", clipSHA(sha)))
	}
	rows, err := parseNumStat(runGit(ctx, r.Workspace, readTimeout,
		"show", "--numstat", "--no-renames", "--format=", sha))
	if err != nil {
		return msgs[0], nil, err
	}
	return msgs[0], rows, nil
}

// DiffNumStat aggregates the three-dot diff from...to（自 merge-base 到
// to 的净差异——「这根分支带来了什么」的口径，不含主线自己的演
// 进）。
func (r *Repo) DiffNumStat(ctx context.Context, from, to string) ([]FileChange, error) {
	if err := checkRefPair(from, to); err != nil {
		return nil, err
	}
	return parseNumStat(runGit(ctx, r.Workspace, readTimeout,
		"diff", "--numstat", "--no-renames", from+"..."+to))
}

// AheadCount is how many commits to holds beyond from（from..to 的
// rev-list 计数）——「领先主线 N 提交」的 N。
func (r *Repo) AheadCount(ctx context.Context, from, to string) (int, error) {
	if err := checkRefPair(from, to); err != nil {
		return 0, err
	}
	out, err := runGit(ctx, r.Workspace, readTimeout,
		"rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 0 {
		return 0, errors.New(i18n.Sf("git rev-list 返回了无法解读的计数（%q）——请升级 git 或反馈此问题", strings.TrimSpace(out)))
	}
	return n, nil
}

// MergedInto reports whether every commit of branch is reachable from
// into（branch ^into 的 rev-list 计数为 0）——座位回收与合并提案共用的
// 「已收口」判定。branch 本就无独立提交时同样为 true。
func (r *Repo) MergedInto(ctx context.Context, branch, into string) (bool, error) {
	if err := checkRefPair(branch, into); err != nil {
		return false, err
	}
	out, err := runGit(ctx, r.Workspace, readTimeout,
		"rev-list", "--count", branch, "^"+into)
	if err != nil {
		return false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 0 {
		return false, errors.New(i18n.Sf("git rev-list 返回了无法解读的计数（%q）——请升级 git 或反馈此问题", strings.TrimSpace(out)))
	}
	return n == 0, nil
}

// LogRange lists up to n commits reachable from to but not from（提
// 案卡片里「这根分支带着哪几笔提交」的清单）。
func (r *Repo) LogRange(ctx context.Context, from, to string, n int) ([]CommitMsg, error) {
	if err := checkRefPair(from, to); err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 30
	}
	return parseLogMessages(runGit(ctx, r.Workspace, readTimeout,
		"log", fmt.Sprintf("-%d", n), "--format="+msgLogFormat, from+".."+to))
}

// MergeNoFF merges branch into the branch the workspace currently holds,
// with --no-ff（即使能快进也留一个 merge 节点——分支的拓扑与 t_NN 引
// 用链在主线上有形可循）and --no-verify（调用方是评审门后的工作室代
// 执行——commit-msg 格式钩子是给成员手笔的，不拦自家的合并）。消息
// 由调用方携带提案号与任务号供反查（merge 本就豁免规范判定）。调用
// 方负责三道前置：工作区驻在目标分支、树净、分支未并入。冲突时 git
// 非零退出——这里立刻 merge --abort 把树还原，错误原文带回（半合并
// 状态绝不外泄）。
func (r *Repo) MergeNoFF(ctx context.Context, branch, msg string) error {
	if !ValidRefName(branch) {
		return RefNameError(i18n.S("分支名"), branch)
	}
	_, err := runGit(ctx, r.Workspace, switchTimeout,
		"merge", "--no-ff", "--no-verify", "-m", msg, branch)
	if err == nil {
		return nil
	}
	// 还原现场：没有进行中的 merge 时 abort 自己也会报错，忽略之——
	// 要带回的是原始合并失败的原因。
	_, _ = runGit(ctx, r.Workspace, switchTimeout, "merge", "--abort")
	return errors.New(i18n.Sf("合并 %s 失败（已还原现场）：%v", branch, err))
}

// AdditiveOnly reports whether the three-dot diff into...branch is
// exclusively NEW files（--diff-filter=A 的行数与全量相等——改一个已有
// 文件哪怕纯增行也不算）。纯新增形状与演化后的主线不可能正面冲突
// （除非两边新增了同路径，那会合并失败并被安全回滚）——autopilot 代
// 收合并提案的准入线；其余形状永远留给房主亲手过目。
func (r *Repo) AdditiveOnly(ctx context.Context, into, branch string) (bool, error) {
	if err := checkRefPair(into, branch); err != nil {
		return false, err
	}
	total, err := parseNumStat(runGit(ctx, r.Workspace, readTimeout,
		"diff", "--numstat", "--no-renames", into+"..."+branch))
	if err != nil {
		return false, err
	}
	if len(total) == 0 {
		return false, nil // 无差异（已并入或空分支）不构成「纯新增」
	}
	added, err := parseNumStat(runGit(ctx, r.Workspace, readTimeout,
		"diff", "--numstat", "--no-renames", "--diff-filter=A", into+"..."+branch))
	if err != nil {
		return false, err
	}
	return len(added) == len(total), nil
}

// HeadSHA reads the workspace's current commit（合并落地后的 merge 节点
// 回执）。
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	out, err := runGit(ctx, r.Workspace, readTimeout, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !shaPattern.MatchString(sha) {
		return "", errors.New(i18n.Sf("git rev-parse HEAD 返回了无法解读的输出（%q）——请升级 git 或反馈此问题", clipSHA(sha)))
	}
	return sha, nil
}

// MergeHeadLive reports whether a merge is in progress（MERGE_HEAD 可解
// 析）——commit-msg 钩子的 merge 豁免判定：合并提交的 message 是 git 拼
// 的（或工作室评审门后带着提案号），规范判定放行；否则装了钩子的仓库
// 里 git merge 会被「缺 type 前缀」误拦。
func (r *Repo) MergeHeadLive(ctx context.Context) bool {
	out, err := runGit(ctx, r.Workspace, readTimeout, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil && strings.TrimSpace(out) != ""
}

// shaPattern admits full or abbreviated hex shas（Show/HeadSHA 的入参与
// 出路口径）。
var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// checkRefPair validates a from/to ref pair before range syntax glues
// them（范围拼接前的最后一道注入防线）。
func checkRefPair(from, to string) error {
	if !ValidRefName(from) {
		return RefNameError(i18n.S("ref 名"), from)
	}
	if !ValidRefName(to) {
		return RefNameError(i18n.S("ref 名"), to)
	}
	return nil
}

// clipSHA renders a short sha for messages.
func clipSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// parseNumStat splits --numstat output into rows. 非表格行（空行、--format
// 残段）跳过；二进制的 "-\t-" 记 Binary。解析绝不猜。
func parseNumStat(out string, err error) ([]FileChange, error) {
	if err != nil {
		return nil, err
	}
	var rows []FileChange
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(ln, "\r"), "\t")
		if len(f) != 3 || f[2] == "" {
			continue
		}
		row := FileChange{Path: f[2]}
		if f[0] == "-" || f[1] == "-" {
			row.Binary = true
		} else {
			add, errA := strconv.Atoi(f[0])
			rem, errR := strconv.Atoi(f[1])
			if errA != nil || errR != nil {
				continue
			}
			row.Added, row.Removed = add, rem
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ShortSHA clips a full sha to the 7-char display form（空串原样返回）。
func ShortSHA(sha string) string { return clipSHA(sha) }

// PostHookScript renders the post-commit hook installed by the 版本管理
// panel（提交播报）：提交落库后把这笔提交报给工作室——房间对话流出一
// 张提交卡。与 commit-msg 钩子同一纪律：钩子只做一件事（exec 本程
// 序），判定与播报都在程序里；post-commit 的退出码 git 本就不看，CLI
// 侧对任何失败也静默退 0——播报是锦上添花，绝不给提交本身添堵。
func PostHookScript(binPath string) string {
	return fmt.Sprintf(`#!/bin/sh
# Niuma Studio post-commit 钩子（版本管理 v2.8，面板「提交钩子」安装）。
# 提交播报：这笔提交报给工作室，房间对话流出一张提交卡。失败静默。
exec "%s" vcs post-commit
`, binPath)
}
