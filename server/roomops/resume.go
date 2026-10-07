package roomops

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/util"
)

// ResumeNotes is the distilled context (三段，引用式) one interrupted
// seat carries through the snapshot.
type ResumeNotes struct {
	Files     []string // 读什么：git status --short + diff --name-only 合并去重 ≤10
	Commits   []string // 记得什么：git log -3 消息原文
	Notes     []string // 干什么：任务日志最近 3 条 note 原文
	FilesAt   int64    // 文件源时刻（快照采集时）
	CommitsAt int64    // 最新 commit 时刻
	NotesAt   int64    // 最新 note 时刻
}

// GitSources is the raw input the extractor reads (pure function —
// tests feed constructed inputs, the t_187 pattern).
type GitSources struct {
	StatusShort string // `git status --short` 输出
	DiffNames   string // `git diff --name-only` 输出
	Log         string // `git log -3 --format=%s|%ct` 输出（消息|时间戳 每行）
}

const (
	resumeMaxFiles   = 10
	resumeMaxCommits = 3
	resumeMaxNotes   = 3
	ResumeTotalCap   = 500 // rune 计（§四外帽——内帽之和通常已 <500）
)

// ExtractResumeNotes is the pure core: sources + task notes → notes.
// 选择不改写：文件路径原样、commit 消息原样、note 原样（§一）。
func ExtractResumeNotes(g GitSources, taskNotes []string, now int64) ResumeNotes {
	out := ResumeNotes{FilesAt: now}
	// 读什么：status 的改动文件＋diff 的名字合并去重（保 status 先序）
	seen := map[string]bool{}
	addFile := func(line string) {
		f := strings.TrimSpace(line)
		if f == "" || seen[f] {
			return
		}
		seen[f] = true
		out.Files = append(out.Files, f)
	}
	for _, line := range strings.Split(g.StatusShort, "\n") {
		// status --short：XY <path>——路径在第 3 列起
		if len(line) > 3 {
			addFile(line[3:])
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(g.DiffNames), "\n") {
		addFile(line)
	}
	if len(out.Files) > resumeMaxFiles {
		out.Files = out.Files[len(out.Files)-resumeMaxFiles:] // 保最新（尾部）
	}
	// 记得什么：log 的消息原文（每行 消息|时间戳）
	for _, line := range strings.Split(strings.TrimSpace(g.Log), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		msg := parts[0]
		if len(parts) == 2 {
			var ts int64
			fmt.Sscanf(parts[1], "%d", &ts)
			if ts > 0 {
				out.CommitsAt = ts
			}
		}
		out.Commits = append(out.Commits, msg)
	}
	if len(out.Commits) > resumeMaxCommits {
		out.Commits = out.Commits[len(out.Commits)-resumeMaxCommits:]
	}
	// 干什么：note 原样（调用方传最近 3 条，尾部最新）
	out.Notes = taskNotes
	if len(out.Notes) > resumeMaxNotes {
		out.Notes = out.Notes[len(out.Notes)-resumeMaxNotes:]
	}
	return out
}

// RenderResumeInjection is the frozen three-段 template (§二): every
// 段带来源时刻，成员可判要点新鲜度。Empty 全源 → 退化泛模板（§四
// 极端兜底——现有行为零回退）。
func RenderResumeInjection(project string, notes ResumeNotes, interruptedAt int64, now int64) string {
	if len(notes.Files) == 0 && len(notes.Commits) == 0 && len(notes.Notes) == 0 {
		return "【接续要点】读任务详情与最近上下文接着干（本次无三段要点——新会话或无在途痕迹）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "【接续要点】now=%d 项目=%s。你上一回合被重启中断（%s），从这三段找回现场，无需重读全部：\n",
		now, project, stamp(interruptedAt))
	fmt.Fprintf(&b, "\n▸ 读什么（你手上的文件，采于 %s）：\n", stamp(notes.FilesAt))
	trunc := 0
	for i, f := range notes.Files {
		fmt.Fprintf(&b, "  %s\n", f)
		_ = i
	}
	if trunc > 0 {
		fmt.Fprintf(&b, "  …等 %d 个（保最新，旧的略）\n", trunc)
	}
	fmt.Fprintf(&b, "\n▸ 记得什么（最近落库的决策，最新于 %s）：\n", stamp(notes.CommitsAt))
	for _, c := range notes.Commits {
		fmt.Fprintf(&b, "  %s\n", c)
	}
	fmt.Fprintf(&b, "\n▸ 干什么（你留下的下一步线索，最新于 %s）：\n", stamp(notes.NotesAt))
	for _, n := range notes.Notes {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	b.WriteString("\n规则：按线索接着干；要点的任何一条与你记忆冲突时，以台账为准（要点只是路标，台账是事实）；确认已读后从「干什么」段的第一条继续。")
	out := b.String()
	// 500 字外帽（§四最后保险——内帽之和通常已 <500）
	if runes := []rune(out); len(runes) > ResumeTotalCap {
		out = string(runes[:ResumeTotalCap]) + "…（超 500 字帽截断，保最新在前文）"
	}
	return out
}

func stamp(unix int64) string {
	if unix <= 0 {
		return "未知时刻"
	}
	return time.Unix(unix, 0).Format("15:04:05")
}

// collectGitSources shells the three read-only git probes in the
// project workspace (best-effort: a missing repo yields empty
// sources — the extractor degrades to the 泛模板).
func collectGitSources(ctx context.Context, dir string) GitSources {
	var g GitSources
	run := func(args ...string) string {
		c := util.HideConsole(exec.CommandContext(ctx, "git", args...))
		c.Dir = dir
		out, err := c.Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	g.StatusShort = run("status", "--short")
	g.DiffNames = run("diff", "--name-only")
	g.Log = run("log", "-3", "--format=%s|%ct")
	return g
}

// distillResumeNotes gathers one interrupted seat's three sources at
// farewell-snapshot time (§五·2: 先采要点再 teardown): the project
// workspace's git (best-effort — a bare repo yields empties) and the
// task ledger's recent notes for this member. Empty sources are fine:
// the injection side degrades to the 泛模板.
func (rc *Face) DistillResumeNotes(h *chat.Hub, name string) string {
	// 项目 key：hub 本身不知道（Registry 管）——从 opts.Registry 反查
	// 或退进程 cwd（大厅）。简化：直接用 Server 的 hub 判定（大厅）；
	// 项目房的 snapshot 走 writeSnapshotTo 的同款路径，项目 key 由
	// 调用侧语境给——这里先以 cwd 兜底（大厅即工作室根）。
	dir := "."
	if rc.Registry != nil {
		for key, hh := range rc.Registry.Rooms() {
			if hh == h && key != "default" {
				if rc.Stores.ProjectStore != nil {
					if p, ok := rc.Stores.ProjectStore.Get(key); ok && p.Workspace != "" {
						dir = p.Workspace
					}
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g := collectGitSources(ctx, dir)
	// 任务 note：引擎最近 3 条该成员的 note 原文
	var notes []string
	var notesAt int64
	if rc.Stores.Engine != nil {
		for _, t := range rc.Stores.Engine.ListFiltered("", "", "", name) {
			if t.Status == "done" || t.Status == "cancelled" {
				continue
			}
			if n := t.Progress; n != "" {
				notes = append(notes, n)
				if t.UpdatedTS > notesAt {
					notesAt = t.UpdatedTS
				}
			}
		}
	}
	// note 顺序：ListFiltered 最新在前——模板要「最近 3 条」原样，反转保
	// 时间正序（模板尾部最新）
	if len(notes) > 0 {
		// 反转为旧→新
		for i, j := 0, len(notes)-1; i < j; i, j = i+1, j-1 {
			notes[i], notes[j] = notes[j], notes[i]
		}
	}
	notes = lastN(notes, resumeMaxNotes)
	rn := ExtractResumeNotes(g, notes, time.Now().Unix())
	rn.NotesAt = notesAt
	// 序列化（快照存 string——注入侧解析或直接存模板？定稿说 contextNotes
	// 是「引用式提炼调用」——存结构化 JSON，注入时 Render）
	b, err := json.Marshal(rn)
	if err != nil {
		return ""
	}
	return string(b)
}

func lastN(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return ss[len(ss)-n:]
}

// ParseResumeNotes is the load leg: snapshot string → notes (empty on
// any malformed payload — never refuse the resume).
func ParseResumeNotes(s string) ResumeNotes {
	var rn ResumeNotes
	if s == "" {
		return rn
	}
	_ = json.Unmarshal([]byte(s), &rn)
	return rn
}
