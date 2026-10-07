//go:build darwin

package shell

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/shell/extlink"
	"github.com/WWestC/Niuma_Studio/webview"
)

var errNoWindow = errors.New("webview 创建失败（WKWebView 不可用）")

// defaultTitlebar is the first-frame window background — the feishu
// light theme's top strip (web's --titlebar default). Full-bleed chrome
// keeps it mostly covered by the page, but it still shows while the
// first frame loads and at resize edges; the page corrects it via the
// setChromeColor binding at load.
const defaultTitlebar = "#f5f6f7"

// AppURL is the app window's address on the given port — the merged
// workbench (management boards + the pixel room canvas board).
func AppURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/app", port)
}

// HasWindow reports the native-window face's availability (darwin
// only — the vendored webview core is WKWebView).
func HasWindow() bool { return true }

// hexRGB parses #rgb/#rrggbb (or a bare form) into 0..1 floats.
func hexRGB(hex string) (r, g, b float64, ok bool) {
	s := hex
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	switch len(s) {
	case 3:
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
	default:
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, true
}

// RunMainWindow is the app's face: the one native WebView window
// pointed at this process's own /app. The server is already serving
// (main started it before calling in); closing the window returns,
// and main's deferred server close flushes the room snapshot on the
// way out — close window = quit app, ZCode-style.
func RunMainWindow(port int) error {
	w := webview.New(false)
	if w == nil {
		return errNoWindow
	}
	w.SetTitle(i18n.S("牛马工作室"))
	w.SetSize(1280, 840, webview.HintNone)
	// 融合标题栏（ZCode 式全出血）：透明标题栏＋FullSizeContentView，
	// 页面顶到窗口边，红黄绿浮在侧栏上，顶条仍是系统拖拽区。必须在
	// SetSize 之后调——SetSize 会整体重写 styleMask，先融合会被抹掉。
	// 底色初值取主题默认，页面加载后通过 setChromeColor 上报
	// --titlebar 实时校正（主题再改也跟得上）
	blendTitlebar(w.Window())
	// 补上 WKWebView 缺的 main menu：macOS 的 ⌘C/⌘V/⌘X/⌘Z/⌘A 靠菜单项的
	// keyEquivalent 派发 selector，壳不建菜单这些快捷键就到不了网页输入框
	// （只能右键）。见 menu_darwin.m——绝不放退出项，退出必须走关窗落盘。
	installEditMenu()
	if r, g, b, ok := hexRGB(defaultTitlebar); ok {
		w.Dispatch(func() { setTitlebarColor(w.Window(), r, g, b) })
	}
	_ = w.Bind("setChromeColor", func(hex string) error {
		r, g, b, ok := hexRGB(hex)
		if ok {
			w.Dispatch(func() { setTitlebarColor(w.Window(), r, g, b) })
		}
		return nil
	})
	// 标题条工具簇的点击穿透带（ZCode 式：☰/←/→ 与红黄绿同排）：页面量
	// 好簇的视口 x 区间上报，罩子在该带内放行点击到 webview、带外仍
	// 是拖拽/双击缩放（titlebar_darwin.m 的 hitTest 洞）。
	_ = w.Bind("setTitlebarPassZone", func(x, zoneW float64) error {
		w.Dispatch(func() { setHoodPassZone(x, zoneW) })
		return nil
	})
	// 外链跳默认浏览器（对话/公告/文档正文链接）：页面在捕获阶段拦
	// 点击转交此桥——壳侧白名单见 shell/extlink。
	_ = w.Bind("openExternal", extlink.OpenExternal)
	w.Navigate(AppURL(port))

	// 原生系统通知（微信/飞书式 OS 弹窗）：WKWebView 没有 Web
	// Notification API，壳经 UNUserNotificationCenter 代发。装窗口即请求
	// 授权（一次性系统弹窗）；页面经 osNotify(title, body, thread=房键)
	// 投递，点弹窗回 __osNotifyClick(房键) 跳房——动作留在页面。
	setupUserNotifications()
	setNotifyClickHandler(func(thread string) {
		w.Dispatch(func() {
			w.Eval("window.__osNotifyClick && window.__osNotifyClick(" + strconv.Quote(thread) + ")")
		})
	})
	_ = w.Bind("osNotify", func(title, body, thread string) error {
		postUserNotification(title, body, thread)
		return nil
	})
	// 授权状态查询（设置卡的指路灯）：拒绝过后 macOS 不再弹问询，页
	// 面得知道这条路已断，指给用户 系统设置→通知 的开关。重问在已定
	// 状态下不弹窗、答的是现行新账——系统设置里改主意立刻可见。
	_ = w.Bind("osNotifyAuth", func() string {
		return notifyAskAuth()
	})
	// 只读授权探针＋一键直达系统设置的通知面板：被拒的通道投了也是被
	// 系统静默丢——「只听见叮咚、等不来横幅」正是这条路（弹窗失灵之
	// 夜的用户面）。页面的兜底横幅门与设置卡的状态行靠这两个桥说实
	// 话、指对路；探针绝不弹问询，消息路径上可以放心常探。
	_ = w.Bind("osNotifyState", func() string {
		return notifyProbe()
	})
	_ = w.Bind("openNotifySettings", func() error {
		openNotifySettings()
		return nil
	})
	w.Run()
	return nil
}
