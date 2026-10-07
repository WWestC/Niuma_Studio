// vcs/lint.go — commit 规范的判定半场（v2.7）。规范全文（手册同款）：
//
//	<type>(<scope>): <一句话摘要>        ← 标题行，type 须在允许表内
//	                                  ← 空行
//	正文（可选，说清为什么）
//	                                  ← 空行
//	任务: t_103                           ← trailer：任务号/需求号至少其一
//	需求: r_12
//
// 三条判定：①标题须带合规 type 前缀（feat/fix/… 表可项目自定）；
// ②标题或正文任一处出现台账号（t_NN / r_NN，宽松匹配——AI 成员偶尔
// 把号写进标题同样是有效引用，按 RequireRef 策略决定要哪种）；③merge
// 提交豁免（两个父提交，message 是 git 拼的不是人的手笔）。纯函数、
// 无 IO——面板徽章、CLI lint、commit-msg 钩子三处共用同一套判定，
// 一处改三处同步。
package vcs

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// RequireRef 策略的四个取值：任务号、需求号、任一、不强制。空串按
// either 处理（项目没配策略时的默认宽松档）。
const (
	RefTask   = "task"
	RefReq    = "req"
	RefEither = "either"
	RefOff    = "off"
)

// DefaultCommitTypes 是 type 允许表的默认值（Conventional Commits
// 的七件套，AI 成员都熟）。
var DefaultCommitTypes = []string{"feat", "fix", "docs", "style", "refactor", "test", "chore"}

// Policy is the per-project commit-convention policy（projects.GitPolicy
// 的形状同源）。
type Policy struct {
	CommitTypes []string `json:"commit_types,omitempty"`
	RequireRef  string   `json:"require_ref,omitempty"` // task|req|either|off（空=either）
}

// Types returns the effective type allowlist.
func (p Policy) Types() []string {
	if len(p.CommitTypes) > 0 {
		return p.CommitTypes
	}
	return DefaultCommitTypes
}

// RequireRefEffective returns the effective ref requirement.
func (p Policy) RequireRefEffective() string {
	switch p.RequireRef {
	case RefTask, RefReq, RefEither, RefOff:
		return p.RequireRef
	default:
		return RefEither
	}
}

var (
	titlePattern = regexp.MustCompile(`^([a-z][a-z0-9_-]*)(\([^)]*\))?:\s+\S`)
	taskRefRe    = regexp.MustCompile(`\bt_[0-9]+`)
	reqRefRe     = regexp.MustCompile(`\br_[0-9]+`)
)

// Verdict is one commit message's lint result.
type Verdict struct {
	OK     bool     `json:"ok"`
	Issues []string `json:"issues,omitempty"`
}

// CheckCommit judges one commit message against the policy. isMerge 豁
// 免一切（merge 的 message 是 git 拼的）；空标题是硬伤。
func CheckCommit(msg string, p Policy, isMerge bool) Verdict {
	if isMerge {
		return Verdict{OK: true}
	}
	v := Verdict{OK: true}
	fail := func(format string, args ...any) {
		v.OK = false
		v.Issues = append(v.Issues, i18n.Sf(format, args...))
	}
	body := strings.TrimSpace(msg)
	if body == "" {
		fail("提交信息为空")
		return v
	}
	title := body
	if i := strings.Index(body, "\n"); i >= 0 {
		title = strings.TrimSpace(body[:i])
	}
	if m := titlePattern.FindStringSubmatch(title); m == nil {
		fail("标题缺 type 前缀——须形如 %s(scope): 摘要（可用 type：%s）",
			p.Types()[0], strings.Join(p.Types(), "|"))
	} else if !containsFold(p.Types(), m[1]) {
		fail("type %q 不在允许表内（可用：%s）", m[1], strings.Join(p.Types(), "|"))
	}
	switch p.RequireRefEffective() {
	case RefOff:
	case RefTask:
		if !taskRefRe.MatchString(body) {
			fail("提交未引用任务号（正文或标题须含 t_NN，如「任务: t_103」）")
		}
	case RefReq:
		if !reqRefRe.MatchString(body) {
			fail("提交未引用需求号（正文或标题须含 r_NN，如「需求: r_12」）")
		}
	default: // either
		if !taskRefRe.MatchString(body) && !reqRefRe.MatchString(body) {
			fail("提交未引用任务/需求号（正文或标题须含 t_NN 或 r_NN）")
		}
	}
	return v
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// HookScript renders the commit-msg hook installed by the 版本管理 panel.
// 钩子只做一件事：把提交信息文件交给本程序判定，非零退出即拦——判
// 定与面板/CLI 同一函数，永不各说各话。binPath 是本程序绝对路径
// （rebuild 原子换位不挪路径，钩子跨重启长期有效）。
func HookScript(binPath string) string {
	return fmt.Sprintf(`#!/bin/sh
# Niuma Studio commit-msg 钩子（版本管理 v2.7，面板「提交钩子」安装）。
# 校验失败非零退出，git commit 当场被拦；确需绕过用 --no-verify
# （版本管理面板的合规统计仍会如实亮出）。
exec "%s" vcs check-msg "$1"
`, binPath)
}
