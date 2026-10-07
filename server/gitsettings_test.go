package server

// gitsettings_test.go — 两级门顶层（git 版本管理总开关）的 HTTP 面钉
// 子：设置写面与回执、总开关关闭后读写的 409 一致性（projectGit 咽喉
// ＋绕行面补钉）、提交播报/合并清单的停摆、关单验提交的透传、PATCH
// 镜像、立项引导整档跳过。

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
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/vcs"
)

func TestGitSettingsFaceAndMasterGate(t *testing.T) {
	s, _, _ := startGitStudio(t)

	// 缺省开：存量口径先钉住。
	var sum0 struct {
		Enabled        bool
		BaseBranch     string `json:"base_branch"`
		AutoSeatBranch bool   `json:"auto_seat_branch"`
		Repo           *struct{ Root string }
	}
	if code := gitGetJSON(t, s, "/p/default/git", &sum0); code != http.StatusOK || !sum0.Enabled {
		t.Fatalf("缺省应 enabled=true：code=%d enabled=%v", code, sum0.Enabled)
	}

	// 关掉总开关（顺带治本面：基线＋自动建枝立项后可改）。
	var rec struct {
		Disabled       bool   `json:"disabled"`
		BaseBranch     string `json:"base_branch"`
		AutoSeatBranch bool   `json:"auto_seat_branch"`
	}
	if code, e := gitPostJSON(t, s, "/p/default/git/settings",
		map[string]any{"disabled": true, "base_branch": "dev", "auto_seat_branch": true}, &rec); code != http.StatusOK || e != "" {
		t.Fatalf("设置写面应 200：%d %q", code, e)
	}
	if !rec.Disabled || rec.BaseBranch != "dev" || !rec.AutoSeatBranch {
		t.Fatalf("回执不合预期: %+v", rec)
	}

	// summary：enabled=false 最优先；基线/自动建枝作为预填面恒给。
	var sum struct {
		Enabled        bool
		BaseBranch     string `json:"base_branch"`
		AutoSeatBranch bool   `json:"auto_seat_branch"`
	}
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK || sum.Enabled {
		t.Fatalf("关闭后 summary 应 enabled=false：code=%d %+v", code, sum)
	}
	if sum.BaseBranch != "dev" || !sum.AutoSeatBranch {
		t.Fatalf("预填面应带生效值: %+v", sum)
	}

	// 写面 409 一致性：咽喉（fetch/建枝）＋绕行补钉（unbind/policy/init）。
	if code, e := gitPostJSON(t, s, "/p/default/git/fetch", map[string]any{}, nil); code != http.StatusConflict || !strings.Contains(e, "已关闭") {
		t.Fatalf("fetch 应 409 带指路：%d %q", code, e)
	}
	if code, e := gitPostJSON(t, s, "/p/default/git/branch", map[string]any{"branch": "feat-y"}, nil); code != http.StatusConflict {
		t.Fatalf("建枝应 409：%d %q", code, e)
	}
	if code, e := gitPostJSON(t, s, "/p/default/git/unbind", map[string]any{"branch": "feat-x"}, nil); code != http.StatusConflict {
		t.Fatalf("unbind 应 409：%d %q", code, e)
	}
	if code, e := gitPostJSON(t, s, "/p/default/git/policy", map[string]any{"reset": true}, nil); code != http.StatusConflict {
		t.Fatalf("policy 应 409：%d %q", code, e)
	}
	if code, e := gitPostJSON(t, s, "/p/default/git/init", map[string]any{}, nil); code != http.StatusConflict {
		t.Fatalf("init 应 409：%d %q", code, e)
	}
	// 读面同口径（branches 经咽喉）。
	if code := gitGetJSON(t, s, "/p/default/git/branches", nil); code != http.StatusConflict {
		t.Fatalf("branches 应 409：%d", code)
	}

	// 重新打开：写面即恢复（fetch 不再 409）。
	if code, e := gitPostJSON(t, s, "/p/default/git/settings",
		map[string]any{"disabled": false, "base_branch": "dev", "auto_seat_branch": true}, nil); code != http.StatusOK {
		t.Fatalf("重开应 200：%d %q", code, e)
	}
	if code, _ := gitPostJSON(t, s, "/p/default/git/fetch", map[string]any{}, nil); code != http.StatusOK {
		t.Fatalf("重开后 fetch 应放行，得 %d", code)
	}

	// 非法基线名 400。
	if code, e := gitPostJSON(t, s, "/p/default/git/settings",
		map[string]any{"disabled": false, "base_branch": "bad..name", "auto_seat_branch": false}, nil); code != http.StatusBadRequest || !strings.Contains(e, "基线分支名") {
		t.Fatalf("非法基线应 400：%d %q", code, e)
	}
}

func TestGitSettingsMergeAndNotifyRefused(t *testing.T) {
	s, c, repo, _, _ := startGitflowStudio(t)
	gitflowMakeCommit(t, repo, "main", "off.txt", "x\n", "feat(off): 关闭态\n\n任务: t_101")
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := r.HeadSHA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Stores.ProjectStore.SetGitSettings("default", true, "", false, false); err != nil {
		t.Fatal(err)
	}
	// 合并清单随总开关停。
	if code := gitGetJSON(t, s, "/p/default/merge", nil); code != http.StatusConflict {
		t.Fatalf("merge 清单应 409，得 %d", code)
	}
	// 提交播报拒播：钩子侧静默吞，房间无 git 帧。
	body, _ := json.Marshal(map[string]string{"sha": sha, "root": r.CommonDir})
	resp, err := http.Post(addrHTTP(s)+"/internal/git/commit", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("播报应 409：%v %v", err, resp)
	}
	resp.Body.Close()
	for _, m := range drainGitFrames(c) {
		if m.Type == chat.MsgGit {
			t.Fatalf("关闭态不应广播 git 帧：%+v", m)
		}
	}
}

func TestGitflowDoneGateDisabledPasses(t *testing.T) {
	s, _, _, eng, _ := startGitflowStudio(t)
	if _, err := s.opts.Stores.ProjectStore.SetGitSettings("default", true, "", false, false); err != nil {
		t.Fatal(err)
	}
	out := eng.Create("", "房主", "关单透传", "", "小猿")
	if out.Denied {
		t.Fatal(out.Reason)
	}
	// 总开关关闭＝非 git 项目同口径：库里没有引用该号的提交也放行。
	if _, ok, reason := s.gitFace().DoneGate("小猿", "", out.Task.ID, tasks.Patch{Status: tasks.StatusDone}); !ok {
		t.Fatalf("关闭态关单应透传放行：%s", reason)
	}
}

func TestBootstrapSkipsWhenGitDisabled(t *testing.T) {
	s, _, _ := startGitStudio(t)
	dir := filepath.Join(t.TempDir(), "fresh")
	res := s.gitFace().BootstrapProjectGit(projects.Project{
		Key: "p1", Workspace: dir, GitDisabled: true,
		GitPlan: &projects.GitPlan{InitIfMissing: true},
	})
	if !res.OK || len(res.Notes) == 0 || !strings.Contains(strings.Join(res.Notes, "；"), "跳过") {
		t.Fatalf("关闭态引导应整档跳过并如实带回：%+v", res)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("引导跳过＝连目录都不该被建（更不该 init）")
	}
}

func TestPatchMirrorsGitDisabled(t *testing.T) {
	s, _, _ := startGitStudio(t)
	patch := func(v bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"git_disabled": v})
		req, err := http.NewRequest(http.MethodPatch, addrHTTP(s)+"/p/default", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH git_disabled=%v 应 200：%v %v", v, err, resp)
		}
		resp.Body.Close()
	}
	patch(true)
	var sum struct{ Enabled bool }
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK || sum.Enabled {
		t.Fatalf("PATCH 关闭后 summary 应 enabled=false：code=%d %+v", code, sum)
	}
	patch(false)
	if code := gitGetJSON(t, s, "/p/default/git", &sum); code != http.StatusOK || !sum.Enabled {
		t.Fatalf("PATCH 重开后 summary 应 enabled=true：code=%d %+v", code, sum)
	}
}

func TestAutoSeatBackfillOnEnable(t *testing.T) {
	s, store := startBootStudio(t)
	repoDir := newGitRepo(t)
	if _, err := store.Create(projects.Project{Key: "autoseat", Name: "丁", Workspace: repoDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Stores.StaffStore.Join("autoseat", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Stores.StaffStore.Join("autoseat", "小马", "后端开发", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Stores.StaffStore.SetBranch("autoseat", "小马", "feat-x"); err != nil {
		t.Fatal(err)
	}
	// 打开主动档：存量无分支成员立即补驻（退化形态无 Fleet——只落账+
	// 切枝，树在下次出生就位）；已驻者不动。
	var rec struct {
		AutoSeat bool     `json:"auto_seat"`
		Backfill []string `json:"backfilled"`
	}
	if code, e := gitPostJSON(t, s, "/p/autoseat/git/settings",
		map[string]any{"disabled": false, "auto_seat_branch": true, "auto_seat": true}, &rec); code != http.StatusOK || e != "" {
		t.Fatalf("开启应 200：%d %q", code, e)
	}
	if !rec.AutoSeat || len(rec.Backfill) != 1 || rec.Backfill[0] != "小猿" {
		t.Fatalf("补驻名单应只有小猿：%+v", rec)
	}
	if row, ok := s.opts.Stores.StaffStore.Get("autoseat", "小猿"); !ok || row.Branch != "wt/小猿" {
		t.Fatalf("小猿应已驻 wt/小猿：%+v", row)
	}
	repo, _ := vcs.Detect(context.Background(), repoDir)
	bs, _ := repo.Branches(context.Background())
	has := false
	for _, b := range bs {
		if b.Name == "wt/小猿" {
			has = true
		}
	}
	if !has {
		t.Fatal("分支 wt/小猿 应已自基线切出")
	}
	// 幂等：原样再存一次不重复补驻（名单空——没有成员再被迁移）。
	var rec2 struct {
		Backfill []string `json:"backfilled"`
	}
	if code, _ := gitPostJSON(t, s, "/p/autoseat/git/settings",
		map[string]any{"disabled": false, "auto_seat_branch": true, "auto_seat": true}, &rec2); code != http.StatusOK {
		t.Fatalf("重复保存应 200，得 %d", code)
	}
	if len(rec2.Backfill) != 0 {
		t.Fatalf("已开着不应重复补驻：%+v", rec2.Backfill)
	}
}

func TestAutoSeatRefusesNonRepoWorkspace(t *testing.T) {
	s, store := startBootStudio(t)
	if _, err := store.Create(projects.Project{Key: "nogit", Name: "丙", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if code, e := gitPostJSON(t, s, "/p/nogit/git/settings",
		map[string]any{"auto_seat": true}, nil); code != http.StatusBadRequest || !strings.Contains(e, "自有的 git 仓库") {
		t.Fatalf("非仓库工作区开自动驻枝应 400 指路：%d %q", code, e)
	}
}
