package dispatch

// failexplain 的行为面：失败终点（onState 的 prompt_failed 分支）之后，
// 底层真因从 CLI 当天日志的尾巴里捞出来补进房流——
//   - 捞到：房里多一行「（底层错误：…）」，真因文本原样带出；
//   - 捞不到（日志缺失/该会话没有失败行）：静默，失败行照旧；
//   - 同一底层错误两分钟内不重复补（通道整晚挂掉时每回合都败，
//     补遗是信号不是复读机）；换了错误文本立即放行。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
)

// writeFailLog 落一份当天 CLI 日志夹具：别的会话的失败（噪音）、目标
// 会话的 completed（非失败）、目标会话的 network.failed（真因）、尾随
// 半行（日志正被写入的形状）。
func writeFailLog(t *testing.T, sessionID string) string {
	t.Helper()
	dir := t.TempDir()
	lines := []string{
		`{"timestamp":"2026-10-03T11:41:16.917Z","level":"warn","event":"model.network.failed","sessionId":"sess-someone-else","context":{"statusMessage":"别人的失败不该被捞"}}`,
		`{"timestamp":"2026-10-03T11:41:20.000Z","level":"info","event":"model.request.completed","sessionId":"` + sessionID + `","context":{}}`,
		`{"timestamp":"2026-10-03T11:41:26.917Z","level":"warn","event":"model.network.failed","sessionId":"` + sessionID + `","context":{"statusMessage":"Cannot connect to API: Connect Timeout Error (attempted address: zapi.example:443, timeout: 10000ms)"}}`,
		`{"timestamp":"2026-10-03T11:41:27.000Z","level":"info","event":"session.event.persistence.completed","sessionId":"` + sessionID + `","context":{}}`,
		`{"timestamp":"2026-10-03T11:41:27.100Z","level":"warn","event":"model.netw`, // 半行
	}
	if err := os.WriteFile(filepath.Join(dir, "zcode-2026-10-03.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pointFailLogAt 把补遗的日志源指向夹具目录，测试后还原生产缺省。
// 还原前先排空在飞的补遗 goroutine（drainExplains）——200ms 宽限里的
// 补遗还在飞时还原全局钩子，就是 race 探测器抓到的那只。
func pointFailLogAt(t *testing.T, d *Dispatcher, dir string) {
	t.Helper()
	prev := failExplainLogDir
	failExplainLogDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() {
		d.drainExplains()
		failExplainLogDir = prev
	})
}

// fireFailure 以「provider 端 prompt_failed」的形状打一次失败终点：
// 成员先翻成 Running（setStatus 只在翻转时落失败面）。
func fireFailure(t *testing.T, d *Dispatcher, name, sid string) {
	t.Helper()
	d.mu.Lock()
	if m := d.members[name]; m != nil {
		m.status = StatusRunning
	}
	d.mu.Unlock()
	d.onState(sid, "", "prompt_failed")
}

// TestFailureExplainSurfacesUnderlyingError —— 主断言：失败终点后，
// 房流里出现带真因的「底层错误」行（异步，等它落进 History）。
func TestFailureExplainSurfacesUnderlyingError(t *testing.T) {
	hub, d, _ := startKickRoom(t)
	pointFailLogAt(t, d, writeFailLog(t, "s-kick-a"))

	fireFailure(t, d, "甲", "s-kick-a")

	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if strings.Contains(m.Text, "底层错误") &&
				strings.Contains(m.Text, "zapi.example:443") {
				return true
			}
		}
		return false
	}, "失败终点应从 CLI 日志捞出底层真因补进房流")
}

// TestFailureExplainThrottled —— 同因两分钟内只补一次：连打两次失败
// 终点，房里只有一行「底层错误」。
func TestFailureExplainThrottled(t *testing.T) {
	hub, d, _ := startKickRoom(t)
	pointFailLogAt(t, d, writeFailLog(t, "s-kick-a"))

	fireFailure(t, d, "甲", "s-kick-a")
	waitFor(t, func() bool { return countExplain(hub) == 1 }, "第一次失败应补一行底层错误")

	// 等第一轮补遗彻底收场（节流窗内），再打第二次。
	time.Sleep(500 * time.Millisecond)
	d.mu.Lock()
	if m := d.members["甲"]; m != nil {
		m.status = StatusRunning
	}
	d.mu.Unlock()
	d.onState("s-kick-a", "", "prompt_failed")
	time.Sleep(600 * time.Millisecond)
	if n := countExplain(hub); n != 1 {
		t.Fatalf("同因节流失效：房里出现 %d 行底层错误（应为 1）", n)
	}
}

// TestFailureExplainSilentWithoutLog —— 日志翻不到时静默：失败行照旧
// 落房，补遗不崩不吵。
func TestFailureExplainSilentWithoutLog(t *testing.T) {
	hub, d, _ := startKickRoom(t)
	pointFailLogAt(t, d, t.TempDir()) // 空目录：没有日志文件

	fireFailure(t, d, "甲", "s-kick-a")

	waitFor(t, func() bool {
		for _, m := range hub.History() {
			if strings.Contains(m.Text, "本轮失败中止") {
				return true
			}
		}
		return false
	}, "失败行应照常落房")
	time.Sleep(500 * time.Millisecond)
	if n := countExplain(hub); n != 0 {
		t.Fatalf("无日志时不应有补遗行，got %d", n)
	}
}

func countExplain(hub *chat.Hub) int {
	n := 0
	for _, m := range hub.History() {
		if strings.Contains(m.Text, "底层错误") {
			n++
		}
	}
	return n
}
