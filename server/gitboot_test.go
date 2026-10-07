package server

// gitboot_test.go — 立项 git 引导（v2.14）的行为面：bootstrap 对缺失目
// 录/无仓目录自动 init（基线分支＋空根提交＋钩子直装）；对已有仓库只
// EnsureBranch 基线（不切分支）；对外层仓库一概拒绝且分毫不动（红线：
// 那可能是用户别的项目的地界）。POST /projects 的 git_plan 落册与
// git_boot 回执；座位面 AutoSeatBranch 自动建枝（关着时回到旧拒绝口
// 径）；预检的 git 事实块（表单自动填的数据源）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/gitops"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/vcs"
)

// startBootStudio boots a minimal studio (loopback face, no registry)
// with in-memory stores — gitboot 的用例都不需要房间集。
func startBootStudio(t *testing.T) (*Server, *projects.Store) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过立项引导用例")
	}
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	staffStore, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Start(chat.NewHub(), Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			StaffStore:   staffStore,
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, projStore
}

func TestBootstrapAutoInitsMissingWorkspace(t *testing.T) {
	s, store := startBootStudio(t)
	ws := filepath.Join(t.TempDir(), "proj-ws") // 不存在：连目录一起建
	p, err := store.Create(projects.Project{Key: "autoinit", Name: "甲", Workspace: ws,
		GitPlan: &projects.GitPlan{InitIfMissing: true, BaseBranch: "main", AutoSeatBranch: true}})
	if err != nil {
		t.Fatal(err)
	}
	boot := s.gitFace().BootstrapProjectGit(p)
	if !boot.OK || !boot.Repo || !boot.Initiated || !boot.Hooked {
		t.Fatalf("自动 init 应全成：%+v", boot)
	}
	repo, err := vcs.Detect(context.Background(), ws)
	if err != nil {
		t.Fatalf("工作区应已是仓库：%v", err)
	}
	st, _ := repo.Status(context.Background())
	if st.Branch != "main" {
		t.Fatalf("基线分支应为 main，实为 %q", st.Branch)
	}
	if msgs, _ := repo.LogMessages(context.Background(), "", 5); len(msgs) != 1 {
		t.Fatalf("应恰有一笔根提交，实 %d 笔", len(msgs))
	}
	// 钩子直装：两枚都在位（自家水印）。
	for _, h := range []string{"commit-msg", "post-commit"} {
		b, err := os.ReadFile(filepath.Join(repo.CommonDir, "hooks", h))
		if err != nil || !strings.Contains(string(b), "Niuma Studio") {
			t.Fatalf("钩子 %s 应已直装：%v", h, err)
		}
	}
	// 计划入册：座位面照它自动建枝。
	got, _ := store.Get("autoinit")
	if got.GitPlan == nil || !got.GitPlan.AutoSeatBranch {
		t.Fatalf("git_plan 应入册：%+v", got.GitPlan)
	}
}

func TestBootstrapOnExistingRepoOnlyEnsuresBase(t *testing.T) {
	s, store := startBootStudio(t)
	repoDir := newGitRepo(t) // main 在 HEAD
	p, err := store.Create(projects.Project{Key: "onrepo", Name: "乙", Workspace: repoDir,
		GitPlan: &projects.GitPlan{BaseBranch: "dev-line"}})
	if err != nil {
		t.Fatal(err)
	}
	boot := s.gitFace().BootstrapProjectGit(p)
	if !boot.OK || !boot.Repo || boot.Initiated {
		t.Fatalf("已有仓库不得再 init：%+v", boot)
	}
	if boot.Base != "dev-line" {
		t.Fatalf("基线应记 dev-line：%+v", boot)
	}
	repo, _ := vcs.Detect(context.Background(), repoDir)
	bs, _ := repo.Branches(context.Background())
	has := map[string]bool{}
	for _, b := range bs {
		has[b.Name] = true
	}
	if !has["dev-line"] {
		t.Fatalf("基线分支 dev-line 应已切出：%+v", bs)
	}
	st, _ := repo.Status(context.Background())
	if st.Branch != "main" {
		t.Fatalf("用户在用的工作树不得被切走（现驻 %s）", st.Branch)
	}
}

func TestBootstrapRefusesOuterRepoUntouched(t *testing.T) {
	s, store := startBootStudio(t)
	outer := newGitRepo(t)
	ws := filepath.Join(outer, "subproj") // 外层仓库的子目录
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := store.Create(projects.Project{Key: "nested", Name: "丙", Workspace: ws,
		GitPlan: &projects.GitPlan{InitIfMissing: true, BaseBranch: "main"}})
	if err != nil {
		t.Fatal(err)
	}
	boot := s.gitFace().BootstrapProjectGit(p)
	if boot.OK || boot.Repo {
		t.Fatalf("外层仓库形态必须整体拒绝：%+v", boot)
	}
	if len(boot.Notes) == 0 || !strings.Contains(strings.Join(boot.Notes, "；"), "外层") {
		t.Fatalf("拒绝理由应说明外层仓库：%+v", boot.Notes)
	}
	// 红线实证：外层仓库一根分支都没多（subproj 下也没建仓）。
	repo, err := vcs.Detect(context.Background(), outer)
	if err != nil {
		t.Fatal(err)
	}
	bs, _ := repo.Branches(context.Background())
	if len(bs) != 2 { // newGitRepo 的 main＋feat-x，一根都不能多
		t.Fatalf("外层仓库被动了：%+v", bs)
	}
	if _, err := os.Stat(filepath.Join(ws, ".git")); err == nil {
		t.Fatal("子目录里不该被建仓")
	}
}

func TestProjectCreateCarriesGitPlanAndBoot(t *testing.T) {
	s, _ := startBootStudio(t)
	ws := filepath.Join(t.TempDir(), "carried")
	body := `{"key":"carried","name":"丁","workspace":` + mustJSON(ws) +
		`,"git_plan":{"init_if_missing":true,"base_branch":"main","auto_seat_branch":true}}`
	req := httptest.NewRequest(http.MethodPost, "/projects", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.roomopsFace().HandleProjectCreate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Key     string            `json:"key"`
		GitBoot gitops.BootResult `json:"git_boot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Key != "carried" || !out.GitBoot.Initiated {
		t.Fatalf("git_boot 回执应带 initiated：%+v", out)
	}
}

func TestSeatAutoBranchCutsFromBase(t *testing.T) {
	s, store := startBootStudio(t)
	repoDir := newGitRepo(t)
	if _, err := store.Create(projects.Project{Key: "seatauto", Name: "戊", Workspace: repoDir,
		GitPlan: &projects.GitPlan{BaseBranch: "main", AutoSeatBranch: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Stores.StaffStore.Join("seatauto", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	// 缺失分支：按计划自基线自动切出，不再 400。
	req := httptest.NewRequest(http.MethodPost, "/p/seatauto/git/seat",
		strings.NewReader(`{"person":"小猿","branch":"wt/小猿"}`))
	req.SetPathValue("key", "seatauto")
	rec := httptest.NewRecorder()
	s.gitFace().HandleGitSeat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("自动建枝后应 200，实 %d：%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["branch_created"] != true {
		t.Fatalf("回执应带 branch_created=true：%s", rec.Body.String())
	}
	repo, _ := vcs.Detect(context.Background(), repoDir)
	bs, _ := repo.Branches(context.Background())
	found := false
	for _, b := range bs {
		if b.Name == "wt/小猿" {
			found = true
		}
	}
	if !found {
		t.Fatalf("分支 wt/小猿 应已自基线切出：%+v", bs)
	}

	// 开关关着：另一项目回到旧口径（指路版本管理/立项开关）。
	offDir := newGitRepo(t)
	if _, err := store.Create(projects.Project{Key: "seatoff", Name: "己", Workspace: offDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Stores.StaffStore.Join("seatoff", "小马", "后端开发", ""); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/p/seatoff/git/seat",
		strings.NewReader(`{"person":"小马","branch":"wt/小马"}`))
	req2.SetPathValue("key", "seatoff")
	rec2 := httptest.NewRecorder()
	s.gitFace().HandleGitSeat(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("未开自动建枝应 400，实 %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "版本管理") {
		t.Fatalf("拒绝文案应指路：%s", rec2.Body.String())
	}
}

func TestPreflightCarriesGitFacts(t *testing.T) {
	s, _ := startBootStudio(t)
	repoDir := newGitRepo(t)
	// 有仓且根即工作区：自动填值得给。
	_, out := preflight(t, s, `{"key":"facts","name":"名","workspace":`+mustJSON(repoDir)+`}`)
	g := out["git"].(map[string]any)
	if g["is_repo"] != true || g["owns_repo"] != true {
		t.Fatalf("git 事实块不应答：%+v", g)
	}
	if g["base_fill"] != "main" {
		t.Fatalf("base_fill 应为当前分支 main：%+v", g)
	}
	// 外层仓库子目录：红线预警 + 自动引导停用档。
	sub := filepath.Join(repoDir, "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_, out2 := preflight(t, s, `{"key":"facts2","name":"名","workspace":`+mustJSON(sub)+`}`)
	g2 := out2["git"].(map[string]any)
	if g2["outer_repo"] != true {
		t.Fatalf("子目录应标记 outer_repo：%+v", g2)
	}
	codes := codesOfLevel(out2, "warn")
	if !codes["ws_outer_repo"] {
		t.Fatalf("应有 ws_outer_repo 预警：%+v", out2["findings"])
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
