//go:build !darwin

package shell

import (
	"errors"
	"fmt"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/shell/extlink"
	"github.com/WWestC/Niuma_Studio/webview"
)

var errNoWindow = errors.New("webview 创建失败（Windows 需 WebView2 运行时，Linux 需 WebKitGTK）")

// AppURL is the app face's address on the given port.
func AppURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/app", port)
}

// HasWindow reports the native-window face's availability — since the
// three-platform app rule it is available everywhere: Edge WebView2 on
// Windows, WebKitGTK on Linux (the vendored webview core's backends).
func HasWindow() bool { return true }

// RunMainWindow is the app's face on Windows/Linux: the one native
// WebView window pointed at this process's own /app — same contract as
// the darwin face (close window = quit app) minus the macOS-only fused
// titlebar. The page guards its setChromeColor call (typeof check), so
// the missing binding is a no-op here rather than an error.
func RunMainWindow(port int) error {
	w := webview.New(false)
	if w == nil {
		return errNoWindow
	}
	w.SetTitle(i18n.S("牛马工作室"))
	w.SetSize(1280, 840, webview.HintNone)
	// 外链跳默认浏览器：与 darwin 面同约（shell/extlink 白名单）。
	_ = w.Bind("openExternal", extlink.OpenExternal)
	w.Navigate(AppURL(port))
	w.Run()
	return nil
}
