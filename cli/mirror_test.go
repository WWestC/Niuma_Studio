package cli

// owner-M1：mirror 发言凭证的三级解析——--token 显式 > NIUMA_OWNER_TOKEN
// 环境 > 座位凭证文件；三级全空是硬错（拒绝拨号），绝不降级为凭名字
// 发言。p/book 事故（成员借 mirror 通道顶房主名发言）的 CLI 侧回归。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
)

func TestResolveOwnerCredential(t *testing.T) {
	restore := chat.OverrideSeatTokenHome(t.TempDir())
	defer restore()

	if got := resolveOwnerCredential("", "房主"); got != "" {
		t.Fatalf("三级全空应为空（调用方拒绝），got %q", got)
	}
	t.Setenv("NIUMA_OWNER_TOKEN", "env-tok")
	if got := resolveOwnerCredential("", "房主"); got != "env-tok" {
		t.Fatalf("环境变量级应生效，got %q", got)
	}
	if got := resolveOwnerCredential("flag-tok", "房主"); got != "flag-tok" {
		t.Fatalf("显式 --token 应压过环境变量，got %q", got)
	}
	t.Setenv("NIUMA_OWNER_TOKEN", "")
	chat.SaveSeatToken("", "房主", "file-tok")
	if got := resolveOwnerCredential("", "房主"); got != "file-tok" {
		t.Fatalf("凭证文件级应生效，got %q", got)
	}
	// 别人的凭证文件不串门：按房主名取
	chat.SaveSeatToken("", "小鸟", "bird-tok")
	if got := resolveOwnerCredential("", "房主"); got != "file-tok" {
		t.Fatalf("按房主名取文件，got %q", got)
	}
}
