package util

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/i18n"
)

func lookMiss(string) (string, error) { return "", errors.New("not found") }

func statIs(set map[string]bool) func(string) error {
	return func(p string) error {
		if set[p] {
			return nil
		}
		return errors.New("no such file")
	}
}

func TestNiumaOnPathWith(t *testing.T) {
	shellHit := func() string { return "/Users/x/.nvm_shim/niuma" }
	cases := []struct {
		name  string
		look  func(string) (string, error)
		stat  func(string) error
		shell func() string
		want  string
	}{
		{"PATH 命中优先于一切", func(string) (string, error) { return "/custom/dir/niuma", nil }, statIs(nil), shellHit, "/custom/dir/niuma"},
		{"PATH 缺、常规 bin 命中", lookMiss, statIs(map[string]bool{"/usr/local/bin/niuma": true}), shellHit, "/usr/local/bin/niuma"},
		{"PATH 缺、仅 homebrew 命中", lookMiss, statIs(map[string]bool{"/opt/homebrew/bin/niuma": true}), shellHit, "/opt/homebrew/bin/niuma"},
		{"PATH 与 bin 全缺、登录 shell 兜住", lookMiss, statIs(nil), shellHit, "/Users/x/.nvm_shim/niuma"},
		{"全链落空", lookMiss, statIs(nil), func() string { return "" }, ""},
		{"无 shell 档（非 darwin 形状）不炸", lookMiss, statIs(nil), nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := niumaOnPathWith(c.look, c.stat, c.shell); got != c.want {
				t.Fatalf("niumaOnPathWith = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGoRunTempExe(t *testing.T) {
	cases := []struct {
		exe  string
		want bool
	}{
		{"/var/folders/zz/T/go-build114513/b001/exe/niuma", true},
		{"/tmp/go-build000999/b001/exe/niuma", true},
		{"/Applications/Niuma_Studio.app/Contents/MacOS/niuma", false},
		{"/Users/x/Niuma_Studio/niuma", false},
		{"niuma", false},
	}
	for _, c := range cases {
		if got := GoRunTempExe(c.exe); got != c.want {
			t.Errorf("GoRunTempExe(%q) = %v, want %v", c.exe, got, c.want)
		}
	}
}

func TestCLIMissingNotice(t *testing.T) {
	stable := "/Applications/Niuma_Studio.app/Contents/MacOS/niuma"
	tpl, args := CLIMissingNotice(stable)
	if !strings.Contains(tpl, `ln -s "%s"`) {
		t.Fatalf("稳定二进制的通告应含 ln -s 命令模板: %q", tpl)
	}
	if strings.Contains(tpl, "go build") {
		t.Fatalf("稳定二进制的通告不该建议先 go build: %q", tpl)
	}
	if len(args) != 1 || args[0] != stable {
		t.Fatalf("args 应回填自身路径: %v", args)
	}
	if out := fmt.Sprintf(tpl, args...); !strings.Contains(out, stable) {
		t.Fatalf("格式化后的通告应嵌入可执行文件路径: %q", out)
	}

	scratch := "/var/folders/zz/T/go-build114513/b001/exe/niuma"
	tpl, args = CLIMissingNotice(scratch)
	if !strings.Contains(tpl, "go build -o") {
		t.Fatalf("go run 临时二进制的通告应建议先落正式二进制: %q", tpl)
	}
	if strings.Contains(tpl, "%s") {
		t.Fatalf("临时二进制的通告不应再回填路径（link 它会悬空）: %q", tpl)
	}
	if len(args) != 0 {
		t.Fatalf("临时二进制分支 args 应为空: %v", args)
	}
}

// TestCLIMissingNoticeI18n guards the template↔dictionary seam: both
// notice shapes are Sf keys, so a wording edit that forgets en.go
// would silently fall back to Chinese in English UI. zh formatted and
// en formatted differ, so equality means "fell back".
func TestCLIMissingNoticeI18n(t *testing.T) {
	i18n.SetLang("en")
	t.Cleanup(func() { i18n.SetLang("zh") })
	for _, exe := range []string{
		"/Applications/Niuma_Studio.app/Contents/MacOS/niuma",
		"/var/folders/zz/T/go-build114513/b001/exe/niuma",
	} {
		tpl, args := CLIMissingNotice(exe)
		if got, zh := i18n.Sf(tpl, args...), fmt.Sprintf(tpl, args...); got == zh {
			t.Errorf("通告模板缺 en 词典条目（英文界面将回落中文）: %q", tpl)
		}
	}
}
