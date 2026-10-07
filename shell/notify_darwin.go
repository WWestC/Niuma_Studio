//go:build darwin

// notify_darwin.go — 原生系统通知的 Go 半边（ObjC 半边在
// notify_darwin.m）。app_darwin.go 装窗口时 setupUserNotifications（主
// 线程，一次），页面经 osNotify 绑定投递（title/body/房键 thread），
// 用户点弹窗由 delegate 回调 niumaNotifyClicked → notifyOnClick 处理器
// → webview Eval 唤页面的 __osNotifyClick(房键)——跳房动作留在页面，
// 壳只做投递员与传令兵。

package shell

/*
#cgo darwin LDFLAGS: -framework UserNotifications -framework AppKit
#include <stdlib.h>
void niumaNotifySetup(void);
void niumaNotifyPost(const char *title, const char *body, const char *thread);
int niumaNotifyAskAuth(void);
int niumaNotifyProbe(void);
*/
import "C"

import (
	"os/exec"
	"sync"
	"unsafe"
)

var (
	notifyMu      sync.Mutex
	notifyOnClick func(thread string) // app_darwin.go 装的点击路由（Eval 回页面）
)

// setupUserNotifications installs the notification delegate and asks the
// system for alert authorization once per process. Main thread only —
// RunMainWindow's caller is the app's main thread.
func setupUserNotifications() {
	C.niumaNotifySetup()
}

// postUserNotification delivers one OS notification (thread = room key;
// Notification Center coalesces per thread, the banner shows the latest).
func postUserNotification(title, body, thread string) {
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	cb := C.CString(body)
	defer C.free(unsafe.Pointer(cb))
	ch := C.CString(thread)
	defer C.free(unsafe.Pointer(ch))
	C.niumaNotifyPost(ct, cb, ch)
}

// notifyAskAuth re-asks UN for alert authorization and reports the fresh
// answer as a page-facing word: "granted" / "denied" / "unknown" (undecided,
// prompt still pending, or the whole bridge degraded — bundleless process).
// Decided states never re-prompt, so the settings card reads current truth
// (System Settings flips show up) instead of the launch-time ledger.
func notifyAskAuth() string {
	switch C.niumaNotifyAskAuth() {
	case 1:
		return "granted"
	case 2:
		return "denied"
	default:
		return "unknown"
	}
}

// notifyProbe reads the CURRENT authorization without ever prompting —
// the same three words notifyAskAuth speaks, but getNotificationSettings
// behind it instead of requestAuthorization. The message path probes this
// (msgpop's fallback gate): a denied channel swallows every post silently,
// and only a read-only probe can catch that without nagging the user with
// permission prompts on the arrival of a message.
func notifyProbe() string {
	switch C.niumaNotifyProbe() {
	case 1:
		return "granted"
	case 2:
		return "denied"
	default:
		return "unknown"
	}
}

// notifySettingsURLs is the deep-link chain to 系统设置→通知. Ventura
// renamed the panes; the chain degrades gracefully on a future rename
// (an anchor the system no longer knows fails the `open` and we step to
// the next candidate) instead of leaving a dead button.
var notifySettingsURLs = []string{
	"x-apple.systempreferences:com.apple.Notifications-Settings.extension",
	"x-apple.systempreferences:com.apple.Notifications-Settings",
	"x-apple.systempreferences:com.apple.preferences.notifications",
}

// openNotifySettings walks the deep-link chain — first `open` that
// succeeds wins. Best effort by the house rule: a navigation failure must
// never take anything down with it (the denied-path UI always carries the
// manual path in its text as well).
func openNotifySettings() {
	for _, url := range notifySettingsURLs {
		if err := exec.Command("open", url).Run(); err == nil {
			return
		}
	}
}

// setNotifyClickHandler routes notification clicks (delegate → here →
// the webview). Set before the window runs; the handler hops to the main
// thread itself, so it may be called from any thread.
func setNotifyClickHandler(f func(thread string)) {
	notifyMu.Lock()
	notifyOnClick = f
	notifyMu.Unlock()
}

// niumaNotifyClicked is the ObjC delegate's entry (UNUserNotificationCenter
// delivers on the main thread).
//
//export niumaNotifyClicked
func niumaNotifyClicked(thread *C.char) {
	key := C.GoString(thread)
	notifyMu.Lock()
	f := notifyOnClick
	notifyMu.Unlock()
	if f != nil {
		f(key)
	}
}
