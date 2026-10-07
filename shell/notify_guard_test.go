//go:build darwin

package shell

import (
	"os"
	"strings"
	"testing"
)

// 通知桥的回归钉：UNUserNotificationCenter 在 bundleIdentifier 解析不出
// 时抛 NSInternalInconsistencyException（2026-10-02 启动失败之夜的
// SIGABRT 元凶——直跑内层二进制、build.sh 重打 bundle 的窗口期、
// Info.plist 损坏都命中）。go test 的测试二进制是裸进程（无 .app、无
// Info.plist，bundleIdentifier 恒 nil）——正是当年的引爆条件。setup 与
// post 在这里必须活着走完：通知可以哑，进程不许炸。
func TestUserNotificationsSurviveBundlelessProcess(t *testing.T) {
	setupUserNotifications()
	postUserNotification("回归测试", "无 bundle 进程不许因此炸掉", "test-thread")
	// 降级路径上的按需重问也得活着，且不许装知道：裸进程没有 delegate，
	// 授权答复无从谈起，恒报 unknown（声称 granted/denied 都是编造）
	if got := notifyAskAuth(); got != "unknown" {
		t.Fatalf("裸进程的授权状态应为 unknown，得到 %q——无 bundle 时不许装知道", got)
	}
	// 只读探针同一条纪律：活着走完、不装知道（消息路径的兜底门靠它
	// 判通道生死，一个装知道的探针会把活通道判死、死通道装活）
	if got := notifyProbe(); got != "unknown" {
		t.Fatalf("裸进程的只读探针应为 unknown，得到 %q——无 bundle 时不许装知道", got)
	}
}

// 「同房顶掉」的源钉：UN 的替换语义认 identifier（threadIdentifier 只管
// 分组不替换），identifier 必须取房键这类稳定值——每条一个 NSUUID 的话
// 同房通知永不互顶、通知中心按房无限堆积（2026-10-02 通知数量爆炸的
// 元凶）。裸进程测不到投递行为（无 bundle 时 post 全哑），钉住源形状。
func TestNotificationIdentifierIsThreadStable(t *testing.T) {
	src, err := os.ReadFile("notify_darwin.m")
	if err != nil {
		t.Skipf("notify_darwin.m 不可读: %v", err)
	}
	if strings.Contains(string(src), "requestWithIdentifier:[[NSUUID") {
		t.Fatal("notify_darwin.m 又用上 NSUUID 当 identifier 了——同房通知将永不互顶、按房无限堆积；identifier 必须取房键（与 threadIdentifier 同源）")
	}
	if !strings.Contains(string(src), `requestWithIdentifier:ident`) {
		t.Fatal("notify_darwin.m 的 requestWithIdentifier 不再走稳定 ident——「同房新弹窗顶掉旧的」契约断了")
	}
}

// 授权答复记账的源钉：requestAuthorization 的两处回调（setup 的首问与
// ask 的重问）都必须把 granted 落进 gNotifyAuth，ask 必须以 delegate 在
// 位为前提、且等待有上限——丢了记账，设置卡的「被拒指路」就退回静默
// （2026-10-02 弹窗失灵之夜：被拒无声无息，用户只觉得弹窗坏了）。
func TestNotifyAuthStatusKeepsLedger(t *testing.T) {
	src, err := os.ReadFile("notify_darwin.m")
	if err != nil {
		t.Skipf("notify_darwin.m 不可读: %v", err)
	}
	for _, pin := range []string{
		"gNotifyAuth = granted ? 1 : 2;",                                                    // 记账不丢
		"if (gNotifyDelegate == nil) return 0;",                                             // 无 bundle 不装知道
		"dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC))", // 主线程等待有上限
	} {
		if !strings.Contains(string(src), pin) {
			t.Fatalf("notify_darwin.m 丢了形状钉 %q——授权状态的记账/降级/有界等待契约断了", pin)
		}
	}
}

// 只读探针的源钉（弹窗失灵之夜的后续）：探针必须走
// getNotificationSettings 读现行账——绝不在消息路径上 requestAuthorization
// （那会弹问询，逐条消息纠缠用户）；问询只许出现在 setup 首问与 ask
// 的手势重问两处，多一处就是探针混进了弹窗。
func TestNotifyProbeNeverPrompts(t *testing.T) {
	src, err := os.ReadFile("notify_darwin.m")
	if err != nil {
		t.Skipf("notify_darwin.m 不可读: %v", err)
	}
	if !strings.Contains(string(src), "getNotificationSettingsWithCompletionHandler") {
		t.Fatal("notify_darwin.m 的只读探针丢了 getNotificationSettings——探针必须读现行账，不走会弹问询的 requestAuthorization")
	}
	if n := strings.Count(string(src), "requestAuthorizationWithOptions"); n != 2 {
		t.Fatalf("requestAuthorization 只许出现在 setup 首问与 ask 重问两处（现 %d 处）——探针混进去就是消息路径弹问询", n)
	}
}

// 通知面板深链链的形状钉：必须属于 x-apple.systempreferences: 家族
// （open 才认得），且链长 ≥2——未来系统改名时降级到次选，不许死按钮。
func TestNotifySettingsDeepLinks(t *testing.T) {
	if len(notifySettingsURLs) < 2 {
		t.Fatal("通知面板深链链长必须 ≥2——单链在未来系统改名后就是死按钮")
	}
	for _, u := range notifySettingsURLs {
		if !strings.HasPrefix(u, "x-apple.systempreferences:") {
			t.Fatalf("深链必须属于 x-apple.systempreferences: 家族（open 才认得）：%q", u)
		}
	}
}
