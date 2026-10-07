//go:build darwin

// menu_darwin.go — 编辑快捷键菜单的 Go 半边（ObjC 半边在 menu_darwin.m）。
// macOS 的 ⌘C/⌘V/⌘X/⌘Z/⌘A 不走网页键盘事件，而是由 NSApp.mainMenu 的
// keyEquivalent 匹配把 copy:/paste: 等 selector 沿 responder chain 派发到
// 作为 first responder 的 WKWebView——vendored webview 壳从不建 mainMenu，
// 快捷键没有菜单项可路由，牛马对话输入框只能右键、纯键盘死路。补一个
// 最小菜单（App 菜单隐藏族＋标准 Edit 六项）即通。主线程调用
// （RunMainWindow 装窗序列）。

package shell

/*
#cgo darwin LDFLAGS: -framework AppKit
void shellInstallEditMenu(void);
*/
import "C"

// installEditMenu installs NSApp.mainMenu (replaces any previous bar).
// Called once at window setup.
func installEditMenu() {
	C.shellInstallEditMenu()
}
