package server

// gitflow_test.go — 版本管理流程半场（v2.8 gitflow）的钉子：关单验提交
// （成员无证据拒/有证据过＋对账注记与房间行/房主越过）、提交播报
// （/internal/git/commit → git 帧）、合并提案全生命周期（提交盖章/房主
// 专属/预检拒收不消费/落地 merge 节点/冲突还原如实报）、座位回收的未
// 并入护栏。

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// startGitflowStudio boots a studio whose lobby project sits on a git
// fixture, with the task engine and merge store wired（gitflow 三件事的
// 公共夹具）。
func startGitflowStudio(t *testing.T) (*Server, *chat.Client, string, *tasks.Engine, *merge.Engine) {
	t.Helper()
	needGitTool(t)
	repo := newGitRepo(t)
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projStore.EnsureLobby(repo); err != nil {
		t.Fatal(err)
	}
	eng := tasks.MustOpenMemory("房主")
	mstore := merge.OpenMemory()
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			Engine:       eng,
			MergeStore:   mstore,
		},
		LocalName:       "房主",
		GitWorktreeRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, lobby.AttachObserver(chat.LobbyKey, false, 0), repo, eng, mstore
}

// gitflowMakeCommit lands one compliant commit on branch in repo.
func gitflowMakeCommit(t *testing.T, repo, branch, file, body, msg string) {
	t.Helper()
	gitRun(t, repo, "switch", branch)
	if err := os.WriteFile(filepath.Join(repo, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", msg)
}

func TestGitflowDoneGate(t *testing.T) {
	s, c, repo, eng, _ := startGitflowStudio(t)

	// 任务 A：成员关单，库里没有引用该号的提交 → 私拒（文案指路）。
	// 夹具种子提交自带 t_100/t_101 引用——先烧掉两个号，正主从
	// t_102 起，证据面才干净。
	for i := 0; i < 2; i++ {
		if burn := eng.Create("", "房主", "烧号", "", ""); burn.Denied {
			t.Fatal(burn.Reason)
		}
	}
	out := eng.Create("", "房主", "接好流程", "", "小猿")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	id := out.Task.ID
	if id == "t_100" || id == "t_101" {
		t.Fatalf("测试假设任务号避开夹具引用（≥t_102），得 %s", id)
	}
	// 成员关单、库里没有引用该号的提交 → 判定面直接拒（文案指路；
	// 私拒帧走 SendTo 只达在座客户端，observer 收不到，故断言判定不
	// 断言帧）。
	if _, ok, reason := s.gitFace().DoneGate("小猿", "", id, tasks.Patch{Status: tasks.StatusDone}); ok || !strings.Contains(reason, "关单须先落提交") {
		t.Fatalf("无提交证据应拒并指路：ok=%v reason=%q", ok, reason)
	}
	s.gitFace().TaskUpdateGateOp(s.hub, c, "小猿", id, tasks.Patch{Status: tasks.StatusDone})
	for _, m := range drainGitFrames(c) {
		if m.Type == chat.MsgTask && m.Event == "updated" {
			t.Fatalf("被拒的关单不应广播 updated：%+v", m)
		}
	}
	if t1, _ := eng.Get(id); t1.Status == tasks.StatusDone {
		t.Fatal("任务不应被关掉")
	}

	// 任务 B：房主无提交直接关 → 过（rank 99 是机制的出处，不是机制
	// 的俘虏）。
	if _, ok, _ := s.gitFace().DoneGate("房主", "", id, tasks.Patch{Status: tasks.StatusDone}); !ok {
		t.Fatal("房主应越过关单门")
	}
	out2 := eng.Create("", "房主", "房主亲手关", "", "小猿")
	if out2.Denied {
		t.Fatal(out2.Reason)
	}
	s.gitFace().TaskUpdateGateOp(s.hub, c, "房主", out2.Task.ID, tasks.Patch{Status: tasks.StatusDone})
	hostOK := false
	for _, m := range drainGitFrames(c) {
		if m.Type == chat.MsgTask && m.Event == "updated" && m.Task != nil && m.Task.Status == tasks.StatusDone {
			hostOK = true
		}
	}
	if !hostOK {
		t.Fatal("房主应能无提交直接关单（广播 updated/done）")
	}

	// 任务 A 正例：成员落了带号的提交 → 关单过，台账带对账注记、房间
	// 有对账行。
	gitflowMakeCommit(t, repo, "main", "flow.txt", "x\n",
		"feat(flow): 接好\n\n任务: "+id)
	s.gitFace().TaskUpdateGateOp(s.hub, c, "小猿", id, tasks.Patch{Status: tasks.StatusDone})
	drained := drainGitFrames(c)
	updated, recorded := false, false
	for _, m := range drained {
		if m.Type == chat.MsgTask && m.Event == "updated" && m.Task != nil && m.Task.Status == tasks.StatusDone {
			updated = true
		}
		if m.Type == chat.MsgSystem && strings.Contains(m.Text, "关单对账") && strings.Contains(m.Text, id) {
			recorded = true
		}
	}
	if !updated || !recorded {
		t.Fatalf("有证据的关单应放行＋对账行，updated=%v recorded=%v：%+v", updated, recorded, drained)
	}
	got, _ := eng.Get(id)
	noted := false
	for _, ln := range got.Log {
		if ln.By == "系统" && strings.Contains(ln.Note, "关单提交对账") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("台账应有系统对账注记：%+v", got.Log)
	}
}

func TestGitflowCommitNotify(t *testing.T) {
	s, c, repo, _, _ := startGitflowStudio(t)
	gitflowMakeCommit(t, repo, "main", "n.txt", "new\n",
		"feat(notify): 播报\n\n任务: t_101")
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := r.HeadSHA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"sha": sha, "root": r.CommonDir})
	resp, err := http.Post(addrHTTP(s)+"/internal/git/commit", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("播报应 200：%v %v", err, resp)
	}
	resp.Body.Close()
	drained := drainGitFrames(c)
	var frame *chat.Message
	for i := range drained {
		if drained[i].Type == chat.MsgGit && drained[i].Event == "commit" {
			frame = &drained[i]
		}
	}
	if frame == nil {
		t.Fatalf("应广播 git{commit} 帧：%+v", drained)
	}
	if frame.Git == nil || frame.Git.SHA != sha || !strings.Contains(frame.Git.Subject, "播报") {
		t.Fatalf("帧载荷不合预期：%+v", frame.Git)
	}
	if len(frame.Git.Files) == 0 || !containsGitflowStr(frame.Git.Refs, "t_101") {
		t.Fatalf("文件行与引用集应在：%+v", frame.Git)
	}
	// 陌生仓库 → 404（钩子侧静默吞，回执如实）。
	body2, _ := json.Marshal(map[string]string{"sha": sha, "root": t.TempDir()})
	resp2, _ := http.Post(addrHTTP(s)+"/internal/git/commit", "application/json", bytes.NewReader(body2))
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("陌生 root 应 404，得 %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestGitflowMergeProposalLifecycle(t *testing.T) {
	s, c, repo, _, mstore := startGitflowStudio(t)
	// feat-x 上落一笔领先提交（newGitRepo 里 feat-x 建在 main 头上）。
	gitflowMakeCommit(t, repo, "feat-x", "fx.txt", "x\n",
		"feat(x): 分支成果\n\n任务: t_102")
	gitRun(t, repo, "switch", "main")

	// 非编排者的成员提交 → 私拒。
	denied := s.gitFace().MergeSubmitAs(s.hub, c, "小猿", &merge.Merge{Branch: "feat-x"})
	if denied.Event != "denied" {
		t.Fatalf("成员不应能提交合并提案：%+v", denied)
	}
	// 房主代提（isOrchestrator 认房主）→ submitted，卡片数据快照齐全。
	msg := s.gitFace().MergeSubmitAs(s.hub, c, "房主", &merge.Merge{Branch: "feat-x"})
	if msg.Event != "submitted" || msg.Merge == nil {
		t.Fatalf("房主代提要成：%+v", msg)
	}
	m := msg.Merge
	if m.ID != "m_01" || m.Into != "main" || m.Commits != 1 || !containsGitflowStr(m.Tasks, "t_102") {
		t.Fatalf("快照不合预期：%+v", m)
	}
	// 房主专属：成员 accept → 拒；房主 accept → merged。
	if bad := s.gitFace().MergeAcceptAs(s.hub, c, "小猿", m.ID, ""); bad.Event != "denied" {
		t.Fatalf("成员不应能验收合并：%+v", bad)
	}
	drainGitFrames(c)
	ok := s.gitFace().MergeAcceptAs(s.hub, c, "房主", m.ID, "")
	if ok.Event != "merged" {
		t.Fatalf("房主验收应落地：%+v", ok)
	}
	if mstore.Pending("", "default") != nil {
		t.Fatal("落地后槽应空")
	}
	if out := gitRun(t, repo, "log", "-1", "--format=%s"); !strings.Contains(out, "merge: feat-x") || !strings.Contains(out, "m_01") {
		t.Fatalf("main 头上应是带提案号的 merge 节点：%q", out)
	}
	// 二次 accept：槽已空 → 拒。
	if again := s.gitFace().MergeAcceptAs(s.hub, c, "房主", m.ID, ""); again.Event != "denied" {
		t.Fatalf("重复 accept 应拒：%+v", again)
	}
}

func TestGitflowMergePrechecksRefuseWithoutConsuming(t *testing.T) {
	s, c, repo, _, mstore := startGitflowStudio(t)
	gitflowMakeCommit(t, repo, "feat-x", "fx.txt", "x\n",
		"feat(x): 又一笔\n\n任务: t_102")
	gitRun(t, repo, "switch", "main")
	msg := s.gitFace().MergeSubmitAs(s.hub, c, "房主", &merge.Merge{Branch: "feat-x"})
	if msg.Event != "submitted" {
		t.Fatalf("提交要成：%+v", msg)
	}
	drainGitFrames(c)
	// 脏主树 → 拒收且不消费槽件（世界变了重新对账即可）。脏在「已跟
	// 踪文件的未提交改动」——未跟踪新文件不挡合并（与切分支护栏同一
	// 口径），改 a.txt 才是真脏。
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if bad := s.gitFace().MergeAcceptAs(s.hub, c, "房主", msg.Merge.ID, ""); bad.Event != "denied" || !strings.Contains(bad.Text, "未提交改动") {
		t.Fatalf("脏树应拒收：%+v", bad)
	}
	if mstore.Pending("", "default") == nil {
		t.Fatal("预检拒收不应消费槽件")
	}
	// 收干净（还原 a.txt 为 HEAD 版）→ 照常落地。
	gitRun(t, repo, "checkout", "--", "a.txt")
	if ok := s.gitFace().MergeAcceptAs(s.hub, c, "房主", msg.Merge.ID, ""); ok.Event != "merged" {
		t.Fatalf("干净后应落地：%+v", ok)
	}
}

func TestGitflowMergeConflictAbortsAndReports(t *testing.T) {
	s, c, repo, _, mstore := startGitflowStudio(t)
	// 两边改同一文件 → 冲突。
	gitflowMakeCommit(t, repo, "feat-x", "a.txt", "branch side\n",
		"feat(x): 分支侧\n\n任务: t_102")
	gitRun(t, repo, "switch", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "feat: 主线侧\n\n任务: t_103")
	msg := s.gitFace().MergeSubmitAs(s.hub, c, "房主", &merge.Merge{Branch: "feat-x"})
	if msg.Event != "submitted" {
		t.Fatalf("提交要成：%+v", msg)
	}
	drainGitFrames(c)
	bad := s.gitFace().MergeAcceptAs(s.hub, c, "房主", msg.Merge.ID, "")
	if bad.Event != "denied" || !strings.Contains(bad.Text, "已还原现场") {
		t.Fatalf("冲突应如实报并还原：%+v", bad)
	}
	// 冲突路径槽件已消费（Take 在 merge 前——plan 病态中拒先例），但树必须干净。
	if mstore.Pending("", "default") != nil {
		t.Fatal("冲突路径槽件应已消费")
	}
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if st, serr := r.Status(context.Background()); serr != nil || st.TrackedDirty() != 0 {
		t.Fatalf("冲突还原后主树应净：%+v %v", st, serr)
	}
}

func TestGitflowSeatCleanupUnmergedGuard(t *testing.T) {
	s, _, repo, _, _ := startGitflowStudio(t)
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	// 孤儿树：驻在领先分支上（人不在编制）。
	gitflowMakeCommit(t, repo, "feat-x", "fx.txt", "x\n", "feat(x): 孤儿\n\n任务: t_102")
	gitRun(t, repo, "switch", "main")
	wt := filepath.Join(s.gitFace().WTRoot(), "default", "前员工")
	if err := r.AddWorktree(context.Background(), wt, "feat-x", ""); err != nil {
		t.Fatal(err)
	}
	if _, e := gitPostJSON(t, s, "/p/default/git/seat-cleanup", map[string]any{"person": "前员工"}, nil); e == "" || !strings.Contains(e, "未并入主线") {
		t.Fatalf("未并入的树应收 409 指路合并，得 %q", e)
	}
	// force 显式推翻 → 收掉。
	if code, e := gitPostJSON(t, s, "/p/default/git/seat-cleanup", map[string]any{"person": "前员工", "force": true}, nil); code != http.StatusOK || e != "" {
		t.Fatalf("force 应收掉：%d %q", code, e)
	}
	// 已并入的分支照常直收：重建树 → 合并提案落地 → 回收无护栏拦。
	if err := r.AddWorktree(context.Background(), wt, "feat-x", ""); err != nil {
		t.Fatal(err)
	}
	if msg := s.gitFace().MergeSubmitAs(s.hub, nil, "房主", &merge.Merge{Branch: "feat-x"}); msg.Event != "submitted" {
		t.Fatalf("提交合并提案失败：%+v", msg)
	} else if ok := s.gitFace().MergeAcceptAs(s.hub, nil, "房主", msg.Merge.ID, ""); ok.Event != "merged" {
		t.Fatalf("先并回主线：%+v", ok)
	}
	if code, e := gitPostJSON(t, s, "/p/default/git/seat-cleanup", map[string]any{"person": "前员工"}, nil); code != http.StatusOK || e != "" {
		t.Fatalf("已并入的树应直收：%d %q", code, e)
	}
}

func containsGitflowStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
