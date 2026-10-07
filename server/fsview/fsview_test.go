package fsview

// fsview_test.go — 文件浏览器三端点的行为面：目录清单（排序/.git 隐去/
// 越界拒绝）、文件信封（文本/图片/二进制嗅探/截断）、检索（命中/忽略
// 目录/帽）。路径围界是本面的安全主张：任何 rel 都钉死在工作区根下。
// （随域面外迁：直测 Face，不再借壳。）

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

// startFsStudio boots a ProjectStore-backed server with one active
// project "alpha" whose workspace is a temp tree the test shapes.
func startFsStudio(t *testing.T) (*Face, string) {
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
	return &Face{Projects: projStore}, ws
}

func fsGet(t *testing.T, s *Face, key, face, query string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/p/"+key+face+query, nil)
	req.SetPathValue("key", key)
	rec := httptest.NewRecorder()
	switch face {
	case "/fs/tree":
		s.Tree(rec, req)
	case "/fs/file":
		s.File(rec, req)
	case "/fs/search":
		s.Search(rec, req)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestFsTreeListsSortedAndHidesGit(t *testing.T) {
	s, ws := startFsStudio(t)
	for _, d := range []string{"web/chat", ".git", "Zebra"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"app.py", "Readme.md", "alpha.c"} {
		if err := os.WriteFile(filepath.Join(ws, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := fsGet(t, s, "alpha", "/fs/tree", "")
	if code != 200 {
		t.Fatalf("root list: %d %v", code, out)
	}
	var names []string
	for _, e := range out["entries"].([]any) {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	want := []string{"web", "Zebra", "alpha.c", "app.py", "Readme.md"} // 目录在前、组内不区分大小写
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	if out["workspace"] != ws {
		t.Fatalf("workspace = %v", out["workspace"])
	}
	// 子目录：parent 链回根
	code, out = fsGet(t, s, "alpha", "/fs/tree", "?path=web/chat")
	if code != 200 || out["parent"] != "web" {
		t.Fatalf("subdir: %d parent=%v", code, out["parent"])
	}
}

func TestFsTreeRefusesEscapeAndUnknown(t *testing.T) {
	s, _ := startFsStudio(t)
	for _, q := range []string{"?path=../../../etc", "?path=/etc", "?path=..\\..\\etc"} {
		code, out := fsGet(t, s, "alpha", "/fs/tree", q)
		if code != 400 {
			t.Fatalf("%s 应 400（钉死在根下）, got %d %v", q, code, out)
		}
	}
	if code, _ := fsGet(t, s, "ghost", "/fs/tree", ""); code != 400 {
		t.Fatalf("未知项目应 400, got %d", code)
	}
	// 文件路径不是目录
	if code, _ := fsGet(t, s, "alpha", "/fs/tree", "?path=nope/deep"); code != 400 {
		t.Fatalf("不存在目录应 400, got %d", code)
	}
}

func TestFsFileEnvelopeKinds(t *testing.T) {
	s, ws := startFsStudio(t)
	write := func(rel string, b []byte) {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.py", []byte("print('你好')\n"))
	write("shot.png", []byte("\x89PNG fake"))
	write("blob.bin", []byte("a\x00b"))
	write("latin1.txt", []byte{0xff, 0xfe, 'q'})

	code, out := fsGet(t, s, "alpha", "/fs/file", "?path=main.py")
	if code != 200 || out["kind"] != "text" || out["content"] != "print('你好')\n" {
		t.Fatalf("text: %d %v", code, out)
	}
	code, out = fsGet(t, s, "alpha", "/fs/file", "?path=shot.png")
	if code != 200 || out["kind"] != "image" || out["content"] != nil {
		t.Fatalf("image: %d %v", code, out)
	}
	code, out = fsGet(t, s, "alpha", "/fs/file", "?path=blob.bin")
	if code != 200 || out["kind"] != "binary" {
		t.Fatalf("NUL 嗅探: %d %v", code, out)
	}
	code, out = fsGet(t, s, "alpha", "/fs/file", "?path=latin1.txt")
	if code != 200 || out["kind"] != "binary" {
		t.Fatalf("非 UTF-8: %d %v", code, out)
	}
	// 目录/越界/缺参
	if code, _ := fsGet(t, s, "alpha", "/fs/file", "?path=."); code != 400 {
		t.Fatalf("目录应 400, got %d", code)
	}
	if code, _ := fsGet(t, s, "alpha", "/fs/file", "?path=../x"); code != 400 {
		t.Fatalf("越界应 400, got %d", code)
	}
	if code, _ := fsGet(t, s, "alpha", "/fs/file", ""); code != 400 {
		t.Fatalf("缺 path 应 400, got %d", code)
	}
}

func TestFsFileTruncatesOversized(t *testing.T) {
	s, ws := startFsStudio(t)
	big := strings.Repeat("a", fsTextMaxBytes+10)
	if err := os.WriteFile(filepath.Join(ws, "big.log"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := fsGet(t, s, "alpha", "/fs/file", "?path=big.log")
	if code != 200 || out["kind"] != "text" || out["truncated"] != true {
		t.Fatalf("truncated: %d %v", code, out["truncated"])
	}
	if len(out["content"].(string)) != fsTextMaxBytes {
		t.Fatalf("content len = %d, want %d", len(out["content"].(string)), fsTextMaxBytes)
	}
}

func TestFsSearchHitsAndSkips(t *testing.T) {
	s, ws := startFsStudio(t)
	for _, rel := range []string{"web/chat/hub.go", "server/hub_test.go", "docs/hub.md",
		".git/hub_internal", "node_modules/pkg/hub.js"} {
		full := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := fsGet(t, s, "alpha", "/fs/search", "?q=HUB")
	if code != 200 {
		t.Fatalf("search: %d", code)
	}
	hits, _ := out["hits"].([]any)
	if len(hits) != 3 {
		t.Fatalf("hits = %v（.git/node_modules 不进检索）", hits)
	}
	joined := ""
	for _, h := range hits {
		joined += h.(string) + ","
	}
	for _, want := range []string{"web/chat/hub.go", "server/hub_test.go", "docs/hub.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺命中 %s: %v", want, joined)
		}
	}
	// 路径子串也算命中：q=chat/hub
	code, out = fsGet(t, s, "alpha", "/fs/search", "?q=chat/hub")
	if code != 200 || len(out["hits"].([]any)) != 1 {
		t.Fatalf("path-needle: %d %v", code, out["hits"])
	}
	if code, _ := fsGet(t, s, "alpha", "/fs/search", ""); code != 400 {
		t.Fatalf("缺 q 应 400, got %d", code)
	}
}

func TestFsSearchCapFences(t *testing.T) {
	s, ws := startFsStudio(t)
	dir := filepath.Join(ws, "many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < fsSearchCap+5; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("needle_%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := fsGet(t, s, "alpha", "/fs/search", "?q=needle")
	if code != 200 || len(out["hits"].([]any)) != fsSearchCap || out["truncated"] != true {
		t.Fatalf("cap: %d n=%v truncated=%v", code, len(out["hits"].([]any)), out["truncated"])
	}
}
