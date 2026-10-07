package server

// preflight_test.go — POST /projects/preflight（v2.13 立项预检）的行为
// 面：store 必拒项以 error 预告（根目录、与在册项目共用目录、key 冲
// 突），目录上的事实以 warn 提前亮（目录不存在、主目录、~/.niuma、与
// 别的项目嵌套、非 git 仓库）；干净输入零发现不拦人。预检只读——不因
// 调用改变 store。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

func startPreflightStudio(t *testing.T) (*Server, string) {
	t.Helper()
	ws := t.TempDir()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.Create(projects.Project{Key: "alpha", Name: "项目甲", Workspace: ws}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("alpha"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return &Server{opts: Options{Stores: StoreSet{ProjectStore: projStore}}}, ws
}

func preflight(t *testing.T, s *Server, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/preflight", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.roomopsFace().HandleProjectPreflight(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func codesOfLevel(out map[string]any, level string) map[string]bool {
	codes := map[string]bool{}
	if list, ok := out["findings"].([]any); ok {
		for _, f := range list {
			if m, ok := f.(map[string]any); ok && m["level"] == level {
				codes[m["code"].(string)] = true
			}
		}
	}
	return codes
}

func TestPreflightPreviewsStoreRefusals(t *testing.T) {
	s, ws := startPreflightStudio(t)
	// 根目录：error 拦下
	// 根目录＝卷根：unix 是 "/"，Windows 得带卷名（C:\ 才算绝对路径）。
	volRoot := filepath.VolumeName(ws) + string(filepath.Separator)
	code, out := preflight(t, s, `{"key":"ok-key","name":"名","workspace":`+jsonStr(volRoot)+`}`)
	if code != 200 {
		t.Fatalf("preflight 应 200，得 %d", code)
	}
	if out["blocking"] != true {
		t.Fatal("根目录 workspace 应 blocking")
	}
	if !codesOfLevel(out, "error")["ws_root"] {
		t.Fatalf("应有 ws_root 发现: %v", out["findings"])
	}
	// 与在册 active 项目共用目录：error 且点名
	_, out = preflight(t, s, `{"key":"ok-key","name":"名","workspace":`+jsonStr(ws)+`}`)
	if !codesOfLevel(out, "error")["ws_dup"] {
		t.Fatalf("应有 ws_dup 发现: %v", out["findings"])
	}
	// key 冲突：error
	_, out = preflight(t, s, `{"key":"alpha","name":"名","workspace":`+jsonStr(filepath.Join(filepath.Dir(ws), "other-z"))+`}`)
	if !codesOfLevel(out, "error")["dup_key"] {
		t.Fatalf("应有 dup_key 发现: %v", out["findings"])
	}
	// 相对路径：error
	_, out = preflight(t, s, `{"key":"ok-key","name":"名","workspace":"relative/ws"}`)
	if !codesOfLevel(out, "error")["bad_ws"] {
		t.Fatalf("应有 bad_ws 发现: %v", out["findings"])
	}
	// 预检只读：调了这些次数，store 仍只有 alpha 一项
	if len(s.opts.Stores.ProjectStore.List()) != 1 {
		t.Fatal("预检不得写 store")
	}
}

func TestPreflightSurfacesDirectoryFacts(t *testing.T) {
	s, ws := startPreflightStudio(t)
	// 目录不存在：warn 提前亮
	_, out := preflight(t, s, `{"key":"ok-key","name":"名","workspace":`+jsonStr(filepath.Join(ws, "not-yet"))+`}`)
	if !codesOfLevel(out, "warn")["ws_missing"] {
		t.Fatalf("应有 ws_missing 发现: %v", out["findings"])
	}
	if out["blocking"] != false {
		t.Fatal("warn 不拦人（blocking 只看 error）")
	}
	// 主目录：warn（成员会在整个主目录读写执行）
	home, err := os.UserHomeDir()
	if err == nil {
		_, out = preflight(t, s, `{"key":"ok-key","name":"名","workspace":`+jsonStr(home)+`}`)
		if !codesOfLevel(out, "warn")["ws_home"] {
			t.Fatalf("应有 ws_home 发现: %v", out["findings"])
		}
	}
	// 与别的项目嵌套（父目录）：warn 遮蔽预告
	_, out = preflight(t, s, `{"key":"ok-key","name":"名","workspace":`+jsonStr(filepath.Dir(ws))+`}`)
	if !codesOfLevel(out, "warn")["ws_nested"] {
		t.Fatalf("应有 ws_nested 发现: %v", out["findings"])
	}
	// 非 git 仓库的已存在目录：warn（分支隔离无从落树）
	if !codesOfLevel(out, "warn")["ws_nogit"] {
		t.Fatalf("临时目录非 git 仓库应有 ws_nogit 发现: %v", out["findings"])
	}
}

func TestPreflightCleanWorkspacePasses(t *testing.T) {
	s, _ := startPreflightStudio(t)
	// git 仓库里的干净输入：零 error 发现（nogit 也不再报）
	repo := t.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q").Run(); err != nil {
		t.Skipf("git 不可用: %v", err)
	}
	_, out := preflight(t, s, `{"key":"ok-key","name":"名","workspace":`+jsonStr(repo)+`}`)
	if errs := codesOfLevel(out, "error"); len(errs) != 0 {
		t.Fatalf("干净输入不应有 error 发现: %v", errs)
	}
	if warns := codesOfLevel(out, "warn"); len(warns) != 0 {
		t.Fatalf("git 仓库干净输入不应有 warn 发现: %v", warns)
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
