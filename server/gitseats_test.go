package server

// gitseats_test.go — 分支隔离座位面的钉子（v2.7.1）：seats 读脸（矩阵
// ＋树状态＋残留树）、seat 写脸（钩子路由/直写退化/未知分支 400/房
// 间留痕）、seat-cleanup（在驻分支 409、脏树如实、幂等）、trace/file
// 对成员工作树的放行。

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/vcs"
)

type seatEvent struct{ project, person, branch string }

// startGitSeatStudio boots a studio whose lobby sits on a git fixture,
// with 小猿 on staff and an injectable worktree root + seat hook.
func startGitSeatStudio(t *testing.T, hook func(project, person, branch string) error) (*Server, *chat.Client, *staffing.Store, string, string, chan seatEvent) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机无 git，跳过分支隔离座位面用例")
	}
	repo := newGitRepo(t)
	wtRoot := t.TempDir()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projStore.EnsureLobby(repo); err != nil {
		t.Fatal(err)
	}
	staffStore, err := staffing.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staffStore.Join("default", "小猿", "前端开发", ""); err != nil {
		t.Fatal(err)
	}
	events := make(chan seatEvent, 4)
	opts := Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			StaffStore:   staffStore,
		},
		LocalName:       "房主",
		GitWorktreeRoot: wtRoot}
	if hook != nil {
		opts.Fleet = FleetFuncs{GitSeatFn: hook}
	} else {
		opts.Fleet = FleetFuncs{GitSeatFn: func(project, person, branch string) error {
			events <- seatEvent{project, person, branch}
			return nil
		}}
	}
	s, err := Start(lobby, opts)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, lobby.AttachObserver(chat.LobbyKey, false, 0), staffStore, repo, wtRoot, events
}

func TestGitSeatsReadFace(t *testing.T) {
	s, _, staff, repo, wtRoot, _ := startGitSeatStudio(t, nil)
	if _, err := staff.SetBranch("default", "小猿", "wt/小猿"); err != nil {
		t.Fatal(err)
	}
	// 手工落一棵真树（真实读取路径）。
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(wtRoot, "default", "小猿")
	if err := r.AddWorktree(context.Background(), wt, "wt/小猿", ""); err != nil {
		t.Fatal(err)
	}
	// 再造一棵无主残留树。
	if err := r.AddWorktree(context.Background(), filepath.Join(wtRoot, "default", "离职牛"), "wt/离职牛", ""); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Seats []struct {
			Person     string `json:"person"`
			Branch     string `json:"branch"`
			Worktree   string `json:"worktree"`
			TreeStatus *struct {
				Branch string `json:"branch"`
			} `json:"tree_status"`
		} `json:"seats"`
		OrphanTrees []struct {
			Person string `json:"person"`
		} `json:"orphan_trees"`
	}
	if code := gitGetJSON(t, s, "/p/default/git/seats", &out); code != http.StatusOK {
		t.Fatalf("seats 应 200: %d", code)
	}
	if len(out.Seats) != 1 || out.Seats[0].Branch != "wt/小猿" ||
		out.Seats[0].Worktree != wt || out.Seats[0].TreeStatus == nil || out.Seats[0].TreeStatus.Branch != "wt/小猿" {
		t.Fatalf("小猿的座位行不合预期: %+v", out.Seats)
	}
	if len(out.OrphanTrees) != 1 || out.OrphanTrees[0].Person != "离职牛" {
		t.Fatalf("残留树应恰有一棵: %+v", out.OrphanTrees)
	}
}

func TestGitSeatWriteFace(t *testing.T) {
	s, obs, _, repo, _, events := startGitSeatStudio(t, nil)
	// 目标分支先造出来（写面校验分支真实存在）。
	cmd := exec.Command("git", "branch", "wt/小猿")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	var out struct {
		Person string `json:"person"`
		Branch string `json:"branch"`
		Moved  bool   `json:"moved"`
	}
	code, errMsg := gitPostJSON(t, s, "/p/default/git/seat", map[string]any{"person": "小猿", "branch": "wt/小猿"}, &out)
	if code != http.StatusOK || errMsg != "" || !out.Moved {
		t.Fatalf("设分支应经钩子成功: %d %q %+v", code, errMsg, out)
	}
	select {
	case ev := <-events:
		if ev.project != "default" || ev.person != "小猿" || ev.branch != "wt/小猿" {
			t.Fatalf("钩子参数不合预期: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("DispatcherSeat 未被路由")
	}
	found := false
	for _, m := range drainGitFrames(obs) {
		if bytes.Contains([]byte(m.Text), []byte("小猿 已驻分支 wt/小猿")) {
			found = true
		}
	}
	if !found {
		t.Fatal("房间应落 [版本] 留痕行")
	}

	// 未知分支 400；幽灵成员 400。
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/seat", map[string]any{"person": "小猿", "branch": "ghost"}, nil); code != http.StatusBadRequest {
		t.Fatalf("幽灵分支应 400: %d %q", code, errMsg)
	}
	if code, _ := gitPostJSON(t, s, "/p/default/git/seat", map[string]any{"person": "幽灵", "branch": "wt/小猿"}, nil); code != http.StatusBadRequest {
		t.Fatal("幽灵成员应 400")
	}
}

func TestGitSeatFallbackWithoutHook(t *testing.T) {
	_, _, _, repo, _, _ := startGitSeatStudio(t, func(project, person, branch string) error {
		return nil // 占位：本用例走无钩子面（见下）
	})
	// 直接换成无钩子形态：另起一个 studio。
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, _ := projects.Open("")
	projStore.EnsureLobby(repo)
	staffStore, _ := staffing.Open("")
	staffStore.Join("default", "小鹿", "前端开发", "")
	cmd := exec.Command("git", "branch", "wt/小鹿")
	cmd.Dir = repo
	cmd.CombinedOutput()
	s2, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			StaffStore:   staffStore,
		},
		LocalName:       "房主",
		GitWorktreeRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	var out struct {
		Moved bool `json:"moved"`
	}
	if code, errMsg := gitPostJSON(t, s2, "/p/default/git/seat", map[string]any{"person": "小鹿", "branch": "wt/小鹿"}, &out); code != http.StatusOK || out.Moved {
		t.Fatalf("无钩子应直写 staffing 且 moved=false: %d %q %+v", code, errMsg, out)
	}
	if row, _ := staffStore.Get("default", "小鹿"); row.Branch != "wt/小鹿" {
		t.Fatalf("直写未落账: %+v", row)
	}
}

func TestGitSeatCleanupFace(t *testing.T) {
	s, _, staff, repo, wtRoot, _ := startGitSeatStudio(t, nil)
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(wtRoot, "default", "小猿")
	if err := r.AddWorktree(context.Background(), wt, "wt/小猿", ""); err != nil {
		t.Fatal(err)
	}
	// 在驻分支的树不收。
	if _, err := staff.SetBranch("default", "小猿", "wt/小猿"); err != nil {
		t.Fatal(err)
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/seat-cleanup", map[string]any{"person": "小猿"}, nil); code != http.StatusConflict {
		t.Fatalf("在驻分支应 409: %d %q", code, errMsg)
	}
	// 清掉分支后回收成功；幂等。
	if _, err := staff.SetBranch("default", "小猿", ""); err != nil {
		t.Fatal(err)
	}
	if code, errMsg := gitPostJSON(t, s, "/p/default/git/seat-cleanup", map[string]any{"person": "小猿"}, nil); code != http.StatusOK {
		t.Fatalf("回收应成功: %d %q", code, errMsg)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("树目录应已移除")
	}
	if code, _ := gitPostJSON(t, s, "/p/default/git/seat-cleanup", map[string]any{"person": "小猿"}, nil); code != http.StatusOK {
		t.Fatal("重复回收应幂等成功")
	}
}

func TestTraceFileAdmitsWorktree(t *testing.T) {
	s, _, _, repo, wtRoot, _ := startGitSeatStudio(t, nil)
	r, err := vcs.Detect(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(wtRoot, "default", "小猿")
	if err := r.AddWorktree(context.Background(), wt, "wt/小猿", ""); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(wt, "shot.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(addrHTTP(s) + "/p/default/trace/file?path=" + png)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("成员工作树里的图片应放行（联合根）: %d", resp.StatusCode)
	}
	// 联合根之外依旧拒绝。
	outside := filepath.Join(t.TempDir(), "x.png")
	os.WriteFile(outside, []byte("x"), 0o644)
	resp2, err := http.Get(addrHTTP(s) + "/p/default/trace/file?path=" + outside)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("根外路径应 400: %d", resp2.StatusCode)
	}
}

var _ = json.Marshal // 保持 json 导入（drainGitFrames 家族共用）
