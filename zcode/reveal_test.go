package zcode

// reveal_test.go — the reveal MODE's own truth (no python, no desktop
// needed): the delivers truth table (one gate shared by the nudge and
// the settings face must never drift), the resolution chain (settings
// mirror > env > platform default), and the on-disk roundtrip. The
// mirror is a package global — every test resets it in cleanup so the
// suite stays order-free.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRevealDeliversTruthTable(t *testing.T) {
	// off 全平台不投；link 全平台投（macOS 的 opt-in，别处只是「投」
	// 的同义词）；其余（"" 与未知值）＝平台默认：darwin 不投、别处投
	// ——env 时代对垃圾值的宽容原样保留。
	cases := []struct {
		mode, goos string
		want       bool
	}{
		{"off", "darwin", false}, {"off", "windows", false}, {"off", "linux", false},
		{"link", "darwin", true}, {"link", "windows", true}, {"link", "linux", true},
		{"", "darwin", false}, {"", "windows", true}, {"", "linux", true},
		{"garbage", "darwin", false}, {"garbage", "windows", true}, {"auto", "linux", true},
	}
	for _, c := range cases {
		if got := RevealDelivers(c.mode, c.goos); got != c.want {
			t.Errorf("RevealDelivers(%q, %q) = %v, want %v", c.mode, c.goos, got, c.want)
		}
	}
}

func TestRevealModeResolution(t *testing.T) {
	t.Cleanup(func() { SetRevealOverride("") })
	t.Setenv("NIUMA_REVEAL", "") // env 不表态

	if got := RevealMode(); got != "" {
		t.Fatalf("无覆写无 env：RevealMode() = %q, want \"\"", got)
	}
	SetRevealOverride("link")
	if got := RevealMode(); got != "link" {
		t.Fatalf("覆写优先：RevealMode() = %q, want link", got)
	}
	// 覆写清空回落 env；env 的两代拼法（NIUMA_ 胜 DH_）由 util.Env 保证，
	// 这里只钉优先级链本身
	SetRevealOverride("")
	t.Setenv("NIUMA_REVEAL", "off")
	if got := RevealMode(); got != "off" {
		t.Fatalf("覆写为空回落 env：RevealMode() = %q, want off", got)
	}
	SetRevealOverride("auto") // 文件镜像哪怕与 env 冲突也赢——落盘的就是用户刚点的
	if got := RevealMode(); got != "auto" {
		t.Fatalf("覆写压过 env：RevealMode() = %q, want auto", got)
	}
}

func TestRevealModeRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reveal.json")
	if got := LoadRevealMode(path); got != "" {
		t.Fatalf("文件缺席：LoadRevealMode() = %q, want \"\"", got)
	}
	for _, mode := range []string{"off", "link", "auto"} {
		if err := SaveRevealMode(path, mode); err != nil {
			t.Fatalf("SaveRevealMode(%q): %v", mode, err)
		}
		if got := LoadRevealMode(path); got != mode {
			t.Fatalf("roundtrip %q：读回 %q", mode, got)
		}
	}
	// 坏 JSON 是 ""（persistence must never refuse the office）——写一坨
	// 非 JSON 进去，读面静默回落未表态
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if got := LoadRevealMode(path); got != "" {
		t.Fatalf("坏文件：LoadRevealMode() = %q, want \"\"", got)
	}
}
