package util

// rebuild_test.go — 重新编译半场的可离线验证件：源码根候选判定（module
// 行验明正身＋main.go 在场）、module 行解析、换位的原子性（unix 跨运
// 行进程同理，这里验文件语义）、编译器输出的截尾整形。真正的 go
// build 不在单测里烧——RebuildSelf 的成败契约由 server/rebuild_test.go
// 从门面按「无源码即 400」钉住。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepo(t *testing.T, dir, module string, withMain bool) {
	t.Helper()
	mod := "module " + module + "\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	if withMain {
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModuleLine(t *testing.T) {
	for _, tc := range []struct{ mod, want string }{
		{"module github.com/WWestC/Niuma_Studio\n", "github.com/WWestC/Niuma_Studio"},
		{"// 注释\ngo 1.22\n\nmodule  spaced/example  \n", "spaced/example"}, // 空白与前置行都要吃得下
		{"go 1.22\n", ""}, // 无 module 行
		{"modulex wrong/prefix\n", ""},
	} {
		if got := moduleLine(tc.mod); got != tc.want {
			t.Errorf("moduleLine(%q) = %q, want %q", tc.mod, got, tc.want)
		}
	}
}

func TestSourceRootFromCandidates(t *testing.T) {
	repo := t.TempDir()
	writeRepo(t, repo, niumaModule, true)
	foreign := t.TempDir() // 别人的 module：光有 go.mod 不算数
	writeRepo(t, foreign, "example.com/someone/else", true)
	noMain := t.TempDir() // 我们的 module 但没有 main.go（例如被误认的子目录）
	writeRepo(t, noMain, niumaModule, false)

	// 候选按序取胜：空串与非源码目录跳过，第一个验明正身的赢
	if got, err := sourceRootFrom([]string{"", foreign, repo}); err != nil || got != repo {
		t.Errorf("候选序列：got (%q, %v), want (%q, nil)", got, err, repo)
	}
	if _, err := sourceRootFrom([]string{foreign, noMain}); err == nil {
		t.Error("无有效候选必须报错，不能把别人的仓库当自家源码")
	}
}

func TestSwapBinaryReplacesInPlace(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "niuma")
	tmp := exe + ".rebuild"
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(tmp, exe); err != nil {
		t.Fatalf("swapBinary: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Errorf("换位后旧位必须是新件，got %q", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("临时件必须随换位消失，不留残渣")
	}
}

// 换位后必须重签 bundle（弹窗失灵之夜的后续）：.app 的 ad-hoc 封印随
// 换位破碎，破签的 bundle 在 macOS 眼里是无效身份——系统通知授权被毫
// 秒级拒绝。行为面：非 bundle 路径零副作用不许炸；源钉：unix 换位路
// 上必须调重签（codesign 的真执行不在单测里烧，server 面按门脸钉）。
func TestResignAppBundleNoopOutsideBundle(t *testing.T) {
	resignAppBundle(filepath.Join(t.TempDir(), "niuma")) // 裸二进制：无事可做，也不许炸
}

func TestSwapResignsAfterReplace(t *testing.T) {
	src, err := os.ReadFile("rebuild.go")
	if err != nil {
		t.Skipf("rebuild.go 不可读: %v", err)
	}
	if !strings.Contains(string(src), "resignAppBundle(exe)") {
		t.Fatal("unix 换位后丢了 resignAppBundle(exe)——rebuild 会把通知签名弄死（破签 bundle 的授权被系统毫秒级拒绝）")
	}
}

func TestTailOutput(t *testing.T) {
	if got := tailOutput([]byte("  one error\n\n")); got != "one error" {
		t.Errorf("短输出应原样修剪：got %q", got)
	}
	if got := tailOutput(nil); !strings.Contains(got, "无输出") {
		t.Errorf("空输出给占位说明：got %q", got)
	}
	long := strings.Repeat("x", 9<<10)
	got := tailOutput([]byte(long))
	if !strings.HasPrefix(got, "……（前文截断）") || len(got) > (8<<10)+64 {
		t.Errorf("长输出截尾到 8KB 带标记：got %d 字节", len(got))
	}
}

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "go")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 缺失与目录都跳过，第一个真文件赢
	if got := firstExisting([]string{filepath.Join(dir, "missing"), subdir, file}); got != file {
		t.Errorf("firstExisting = %q, want %q", got, file)
	}
	if got := firstExisting([]string{filepath.Join(dir, "missing"), subdir}); got != "" {
		t.Errorf("全未命中应回空串，got %q", got)
	}
}

func TestBuildEnv(t *testing.T) {
	sep := string(os.PathListSeparator) // 拼接分隔符按平台（Windows 是 ";"）；PATH 尾段原样保留即可
	env := buildEnv([]string{"PATH=/usr/bin:/bin", "CGO_ENABLED=0", "HOME=/tmp"}, "/opt/homebrew/bin")
	var gotPath, gotCgo, gotHome string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "PATH="):
			if gotPath != "" {
				t.Error("PATH 必须唯一")
			}
			gotPath = kv
		case strings.HasPrefix(kv, "CGO_ENABLED="):
			if gotCgo != "" {
				t.Error("CGO_ENABLED 必须唯一（先摘残留再钉）")
			}
			gotCgo = kv
		case strings.HasPrefix(kv, "HOME="):
			gotHome = kv
		}
	}
	if gotPath != "PATH=/opt/homebrew/bin"+sep+"/usr/bin:/bin" {
		t.Errorf("PATH 应前置 go 所在目录：got %q", gotPath)
	}
	if gotCgo != "CGO_ENABLED=1" {
		t.Errorf("CGO_ENABLED 必须强制为 1（webview 走 cgo）：got %q", gotCgo)
	}
	if gotHome != "HOME=/tmp" {
		t.Errorf("其余环境原样保留：got HOME %q", gotHome)
	}
	// base 里没有 PATH 时（极简环境）也要补一条，go 工具链才找得回自己
	env = buildEnv([]string{"HOME=/tmp"}, "/opt/homebrew/bin")
	found := false
	for _, kv := range env {
		if kv == "PATH=/opt/homebrew/bin" {
			found = true
		}
	}
	if !found {
		t.Errorf("无 PATH 的 base 必须补 PATH=goDir：got %v", env)
	}
}
