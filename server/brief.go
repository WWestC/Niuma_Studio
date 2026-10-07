package server

// 任务简报（task brief）：领单/续做时的「上下文装配点」。一单的全貌
// 散在五处——任务台账、需求库、黑板文档（含评审纪要）、分支绑定表、
// 工作区——过去要靠 agent 自己想齐 task show＋req＋kb doc 的多跳拉取；
// 这里把它装成一页 text/plain（GET /kb/tasks/{id}/brief，CLI `task
// brief t_NN` 同脸），开工或续做前一次拉全。装配纪律与手册「按需取
// 读」同源：每节有独立预算（超限截断并标注，截断是预算不是丢失），
// 尾节固定给「按需自取」指针——重要的在场上，次要的有索引。

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// brief 的节预算（rune 计）。desc 与需求正文是最长的两节，1600 字装得
// 下绝大多数单子；纪要只给摘录——骨架可读，全文走 kb doc；台账/子单/
// 兄弟单按行数收口。各节合计的量级约 6–8K rune，是一次注入能吃下的
// 上下文体量，不需要再叠总帽。
const (
	briefFieldCap   = 1600 // desc / 需求正文的节帽
	briefMinutesCap = 600  // 评审纪要摘录帽
	briefLogTail    = 6    // 近期台账行数
	briefLineMax    = 10   // 子任务/兄弟单行帽
	briefDocsMax    = 6    // 文中点名文档条帽
	briefCommitMax  = 8    // 工件节的提交行帽
)

// handleKBTaskBrief serves one task's assembled brief. Id resolution
// follows handleKBTask exactly: ?project= scopes to that shelf (a
// cross-shelf id simply does not exist there — v2.10 号段分家), absent
// = the host's studio-wide read, ambiguous across shelves = 404.
func (s *Server) handleKBTaskBrief(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stores.Engine == nil {
		http.NotFound(w, r)
		return
	}
	var t tasks.Task
	var ok bool
	if project := s.roomScopeQuery(r); project != "" {
		t, ok = s.opts.Stores.Engine.GetIn(project, r.PathValue("id"))
	} else {
		t, ok = s.opts.Stores.Engine.Get(r.PathValue("id"))
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(s.taskBrief(t)))
}

// taskBrief assembles one task's pickup brief. Nil stores skip their
// sections silently (the same tolerance the /kb faces hold); sections
// stay deterministic — no clock, no randomness beyond the ledgers
// themselves, so the same task briefs the same twice.
func (s *Server) taskBrief(t tasks.Task) string {
	shelf := tasks.SlotKey(t.ProjectKey)
	var rq requirements.Req
	if t.Req != "" && s.opts.Stores.Requirements != nil {
		if r, ok := s.opts.Stores.Requirements.GetIn(s.subject(), shelf, t.Req); ok {
			rq = r
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "【任务简报｜%s「%s」】\n", t.ID, t.Title)
	s.briefHeader(&b, t, shelf)
	if t.Desc != "" {
		b.WriteString("描述：\n" + briefIndent(briefClip(t.Desc, briefFieldCap)) + "\n")
	}
	s.briefRequirement(&b, t, rq)
	s.briefParents(&b, t, shelf)
	s.briefDeps(&b, t, shelf)
	s.briefTree(&b, t, shelf)
	briefLog(&b, t)
	minutesKey := s.briefMinutes(&b, t, shelf)
	s.briefDocs(&b, t, rq, minutesKey)
	s.briefGit(&b, t, shelf)
	s.briefArtifacts(&b, t, shelf)
	briefTail(&b, t, shelf)
	return b.String()
}

// briefHeader renders the one-glance line(s): project, status, owner,
// priority, progress, and the scheduling fields that exist.
func (s *Server) briefHeader(b *strings.Builder, t tasks.Task, shelf string) {
	name := ""
	if s.opts.Stores.ProjectStore != nil {
		if p, ok := s.opts.Stores.ProjectStore.Get(shelf); ok {
			name = p.Name
		}
	}
	proj := shelf
	if name != "" {
		proj = fmt.Sprintf("%s（%s）", shelf, name)
	}
	owner := t.Assignee
	if owner == "" {
		owner = "任务池待认领"
	}
	line := fmt.Sprintf("项目 %s · %s · 负责人 %s", proj, tasks.StatusLabel(t.Status), owner)
	if p := tasks.PriorityLabel(t.Priority); p != "" {
		line += " · 优先级 " + p
	}
	switch {
	case t.Progress != "":
		line += " · 进度 " + t.Progress
	case t.ProgressPct > 0:
		line += fmt.Sprintf(" · 进度 %d%%", t.ProgressPct)
	}
	b.WriteString(line + "\n")
	var sched []string
	if t.StartTS > 0 {
		sched = append(sched, "起 "+briefStamp(t.StartTS))
	}
	if t.EndTS > 0 {
		sched = append(sched, "止 "+briefStamp(t.EndTS))
	}
	if t.Version != "" {
		sched = append(sched, "版本 "+t.Version)
	}
	if t.Milestone {
		sched = append(sched, "里程碑")
	}
	if t.PlanID != "" {
		sched = append(sched, "提案 "+t.PlanID)
	}
	if len(sched) > 0 {
		b.WriteString("排期：" + strings.Join(sched, " · ") + "\n")
	}
}

func briefStamp(ts int64) string { return time.Unix(ts, 0).Format("01-02 15:04") }

// briefRequirement renders the mothering requirement's full body (the
// acceptance criteria live there) — a dangling req id still names
// itself honestly instead of vanishing.
func (s *Server) briefRequirement(b *strings.Builder, t tasks.Task, rq requirements.Req) {
	if t.Req == "" {
		return
	}
	if rq.ID == "" {
		fmt.Fprintf(b, "来源需求 %s（台账已不可见——可能已被删除）\n", t.Req)
		return
	}
	fmt.Fprintf(b, "来源需求 %s「%s」（%s）\n", rq.ID, rq.Title, briefReqStatus(rq.Status))
	if rq.Body != "" {
		b.WriteString(briefIndent(briefClip(rq.Body, briefFieldCap)) + "\n")
	}
}

func briefReqStatus(st string) string {
	switch st {
	case requirements.StatusOpen:
		return "open·待评审"
	case requirements.StatusSplit:
		return "split·已拆解"
	case requirements.StatusClosed:
		return "closed·已闭环"
	}
	return st
}

// briefParents walks up the task tree (depth ≤3 by engine contract),
// one line per ancestor.
func (s *Server) briefParents(b *strings.Builder, t tasks.Task, shelf string) {
	id := t.Parent
	for n := 0; id != "" && n < 3; n++ {
		p, ok := s.opts.Stores.Engine.GetIn(shelf, id)
		if !ok {
			fmt.Fprintf(b, "父任务 %s（台账已不可见）\n", id)
			return
		}
		fmt.Fprintf(b, "父任务 %s「%s」[%s]——%s\n", p.ID, p.Title,
			tasks.StatusLabel(p.Status), briefOneLine(p.Desc))
		id = p.Parent
	}
}

func briefOneLine(desc string) string {
	if desc == "" {
		return "无描述"
	}
	return util.FirstLine(desc)
}

// briefDeps resolves every prerequisite with its live status — deps
// are a soft lock, so an unfinished one is flagged, not hidden.
func (s *Server) briefDeps(b *strings.Builder, t tasks.Task, shelf string) {
	if len(t.Deps) == 0 {
		return
	}
	var lines []string
	for _, id := range t.Deps {
		d, ok := s.opts.Stores.Engine.GetIn(shelf, id)
		if !ok {
			lines = append(lines, fmt.Sprintf("· %s（台账已不可见）", id))
			continue
		}
		mark := ""
		if d.Status != tasks.StatusDone && d.Status != tasks.StatusCancelled {
			mark = " ⚠ 前置未完（依赖是软锁：可开工，先看清依赖再动手）"
		}
		lines = append(lines, fmt.Sprintf("· %s「%s」[%s]%s", d.ID, d.Title,
			tasks.StatusLabel(d.Status), mark))
	}
	b.WriteString("依赖：\n" + strings.Join(lines, "\n") + "\n")
}

// briefTree scans the task's own shelf for its children (the aggregate
// view above the leaves) and its requirement siblings (who else works
// this line — the coordination surface).
func (s *Server) briefTree(b *strings.Builder, t tasks.Task, shelf string) {
	var kids, sibs []tasks.Task
	for _, o := range s.opts.Stores.Engine.ListFiltered("", "", shelf, "") {
		if o.ID == t.ID {
			continue
		}
		if o.Parent == t.ID {
			kids = append(kids, o)
		} else if t.Req != "" && o.Req == t.Req {
			sibs = append(sibs, o)
		}
	}
	if len(kids) > 0 {
		b.WriteString("子任务（非叶任务的进度是子任务聚合的派生态）：\n")
		briefTaskLines(b, kids)
	}
	if len(sibs) > 0 {
		b.WriteString("同需求其他单：\n")
		briefTaskLines(b, sibs)
	}
}

func briefTaskLines(b *strings.Builder, ts []tasks.Task) {
	for i, o := range ts {
		if i == briefLineMax {
			fmt.Fprintf(b, "· ……另有 %d 条（task list --project 过滤）\n", len(ts)-i)
			return
		}
		owner := o.Assignee
		if owner == "" {
			owner = "池"
		}
		fmt.Fprintf(b, "· %s「%s」[%s·%s]\n", o.ID, o.Title, tasks.StatusLabel(o.Status), owner)
	}
}

// briefLog tails the change journal — the "what happened lately" a
// resuming member needs first.
func briefLog(b *strings.Builder, t tasks.Task) {
	if len(t.Log) == 0 {
		return
	}
	start := 0
	if len(t.Log) > briefLogTail {
		start = len(t.Log) - briefLogTail
	}
	b.WriteString("近期台账：\n")
	for _, e := range t.Log[start:] {
		fmt.Fprintf(b, "· %s %s：%s\n", briefStamp(e.TS), e.By, util.FirstLine(e.Note))
	}
}

// briefMinutes surfaces the requirement's newest review minutes — the
// 「为什么这么拆」的原话。纪要跟房走（v2.9）：项目需求落 p/<key>/
// meetings/，大厅落 ops/meetings/；老档案可能还在大厅架上，两架都探、
// 序号大者为新。Returns the found key ("" = none) so the related-docs
// scan can skip it instead of double-listing.
func (s *Server) briefMinutes(b *strings.Builder, t tasks.Task, shelf string) string {
	if t.Req == "" || s.opts.Stores.Docs == nil {
		return ""
	}
	prefix := meeting.MinutesKey(t.Req, 1)
	prefix = prefix[:strings.LastIndex(prefix, "-")+1]
	shelves := []string{prefix}
	if shelf != chat.LobbyKey {
		shelves = append([]string{kb.ProjectDocKey(shelf, strings.TrimPrefix(prefix, "ops/"))}, prefix)
	}
	for _, p := range shelves {
		for seq := 8; seq >= 1; seq-- {
			key := fmt.Sprintf("%s%d", p, seq)
			d, err := s.opts.Stores.Docs.Get(key, 0)
			if err != nil {
				continue
			}
			fmt.Fprintf(b, "评审纪要（%s）：\n%s\n全文：kb doc %s\n", d.Title,
				briefIndent(briefClip(d.Body, briefMinutesCap)), key)
			return key
		}
	}
	return ""
}

// briefDocs lists active docs whose key is literally named in the
// task/requirement text — mechanical relevance, no guessing: 谁在单子
// 里被点名，谁上台面（点名引用是成员自己的相关性判断，装配器只照办）。
func (s *Server) briefDocs(b *strings.Builder, t tasks.Task, rq requirements.Req, skipKey string) {
	if s.opts.Stores.Docs == nil {
		return
	}
	hay := t.Title + "\n" + t.Desc
	if rq.ID != "" {
		hay += "\n" + rq.Title + "\n" + rq.Body
	}
	var hits []string
	for _, m := range s.opts.Stores.Docs.List() {
		if m.Key == skipKey || !strings.Contains(hay, m.Key) {
			continue
		}
		hits = append(hits, fmt.Sprintf("· %s（%s）—— kb doc %s", m.Key, m.Title, m.Key))
		if len(hits) >= briefDocsMax {
			break
		}
	}
	if len(hits) == 0 {
		return
	}
	b.WriteString("文中点名的黑板文档：\n" + strings.Join(hits, "\n") + "\n")
}

// briefGit renders the studio-side git context: branches bound to this
// task's requirement (the req-activity reverse of the branch table)
// and the workspace path — 项目专属约定在工作区自己的 AGENTS.md，
// 工作室手册只立通则。
func (s *Server) briefGit(b *strings.Builder, t tasks.Task, shelf string) {
	if s.opts.Stores.ProjectStore == nil {
		return
	}
	p, ok := s.opts.Stores.ProjectStore.Get(shelf)
	if !ok {
		return
	}
	if t.Req != "" && len(p.BranchLinks) > 0 {
		var branches []string
		for br, link := range p.BranchLinks {
			if link.Req == t.Req {
				branches = append(branches, br)
			}
		}
		sort.Strings(branches)
		if len(branches) > 0 {
			fmt.Fprintf(b, "绑定分支：%s（绑 %s——提交请带 %s/%s 号）\n",
				strings.Join(branches, "、"), t.Req, t.ID, t.Req)
		}
	}
	if p.Workspace != "" {
		fmt.Fprintf(b, "工作区 %s（项目专属约定见工作区 AGENTS.md）\n", p.Workspace)
	}
}

// briefArtifacts renders the「what already exists」face for code work:
// the shared workspace's live state (branch, dirt) plus every commit
// across branches naming this task or its requirement — 工件即状态，
// 已落地的提交就是最硬的进度，续做的人先看已经做了什么。非仓库工作
// 区整节静默（gitLineForLanded 的纪律：非 git 项目一行都不多话）；
// 探测/读失败同样静默——简报不因 git 半场打嗝而缺页。
func (s *Server) briefArtifacts(b *strings.Builder, t tasks.Task, shelf string) {
	if s.opts.Stores.ProjectStore == nil {
		return
	}
	_, repo, err := s.gitFace().ProjectGit(shelf)
	if err != nil || repo == nil {
		return
	}
	ctx := context.Background()
	var lines []string
	if st, err := repo.Status(ctx); err == nil && st.Branch != "" {
		state := "干净"
		if st.Staged+st.Modified > 0 || st.Untracked > 0 {
			state = fmt.Sprintf("有未提交改动（暂存 %d·修改 %d·未跟踪 %d）", st.Staged, st.Modified, st.Untracked)
		}
		lines = append(lines, fmt.Sprintf("· 工作区分支 %s——%s", st.Branch, state))
	}
	seen := map[string]bool{}
	commits := 0
	for _, token := range []string{t.ID, t.Req} {
		if token == "" {
			continue
		}
		msgs, err := repo.LogGrepAll(ctx, token, briefCommitMax)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if seen[m.SHA] || commits >= briefCommitMax {
				continue
			}
			seen[m.SHA] = true
			commits++
			lines = append(lines, fmt.Sprintf("· 提交 %s %s %s", vcs.ShortSHA(m.SHA),
				briefStamp(m.DateTS), vcs.SubjectOf(m.Body)))
		}
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("工件与提交：\n" + strings.Join(lines, "\n") + "\n")
}

// briefTail is the fixed 按需自取 footer: everything the brief chose
// not to inline, with the exact verb to fetch it.
func briefTail(b *strings.Builder, t tasks.Task, shelf string) {
	fmt.Fprintf(b, "──\n按需自取（别整篇吞）：任务全量 task show %s · 需求台账 req list --project %s · 文档索引 kb docs · 单条规矩 kb manual <关键词> · 房内讨论 GET /p/%s/history?q=<关键词> · 本房工作过程 GET /p/%s/trace\n",
		t.ID, shelf, shelf, shelf)
}

// briefClip 截到预算并标注——截断是预算不是丢失，尾节有指针。
func briefClip(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "……（截断——原文按尾节指针自取）"
	}
	return s
}

// briefIndent 每行缩进两格，多行字段读起来是引文块。
func briefIndent(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = "  " + ln
	}
	return strings.Join(lines, "\n")
}
