//go:build darwin

package util

// resign_darwin.go — swapBinary 的 macos 收尾：.app 里的可执行件被
// 原地换位后，bundle 的 ad-hoc 签名封印随之破碎——而 macOS 只把签名
// 完好的 bundle 当有效身份，UNUserNotificationCenter 对无效身份的授权
// 请求毫秒级拒绝（连「想给你发通知」的问询都不弹；签名之死是
// 2026-10-02 弹窗失灵之夜查明的元凶，build.sh 打包时特意签名同源）。
// 换完立刻按 build.sh 同规重签（不传 identifier——ad-hoc 签 bundle 时
// codesign 自会取 Info.plist 的 CFBundleIdentifier，身份随包走）。
// best-effort：codesign 缺席或失败不挡重启——通知可以哑，一次成功的
// 编译换位不该被签名工具的缺席拖回滚。
//
// 注意 ad-hoc 签名的天性：每次重签 CDHash 都变，macOS 可能对同 bundle
// 重新问一次通知授权——答「允许」即可，这是系统对「身份又变了」的
// 正常谨慎，不是故障（应用的「系统授权」状态行会如实亮出来）。
import (
	"os/exec"
	"path/filepath"
)

// resignAppBundle re-seals the .app bundle containing exePath (no-op for
// a bare binary — terminal runs and tests have no bundle to sign).
func resignAppBundle(exePath string) {
	app := filepath.Dir(filepath.Dir(filepath.Dir(exePath))) // .../X.app/Contents/MacOS/exe → X.app
	if filepath.Ext(app) != ".app" {
		return
	}
	_ = exec.Command("codesign", "--force", "--sign", "-", app).Run()
}
