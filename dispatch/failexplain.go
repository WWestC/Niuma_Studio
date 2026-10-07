package dispatch

// failexplain.go — 失败终点的人话补遗。provider 侧把回合失败上报成裸
// stopReason（prompt_failed），底层真因（Connect Timeout、并发已满、
// 401/429 …）一个字都不上协议线，只落在 CLI 自己的 ~/.zcode/cli/log
// 日志里——聊天流里的「（本轮失败中止：prompt_failed）」五个字符无从
// 下手（本修的动机：内网网关整晚连不上，满屏 prompt_failed 却没人知道
// 该去看哪里）。失败终点在这里异步翻当天日志的尾巴，按会话捞出最近一
// 条 model 层失败的 statusMessage，往房里补一行「底层错误」。
//
// 纪律：读日志绝不落在读循环栈上（Hooks 契约，同 laneKick/drainTerminal
// 一族）——自有 goroutine；同一成员同一底层错误两分钟内只补一次，通道
// 整段挂掉时每回合都败，补遗得是信号不是复读机；日志缺失/翻不到就静默
// 收场，失败行上的方向性提示（zcode.TurnFailHint）已经把路指到日志。

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

const (
	// failExplainEvery 节流窗：同一成员的重复补遗至少隔这么久（换了
	// 错误文本则立即放行——病因变了是新信号）。
	failExplainEvery = 2 * time.Minute
	// failExplainTail 只翻当天日志的尾巴：要找的是最新一条失败，前面
	// 几十 MB 的历史与之无关。
	failExplainTail = 4 << 20
	// failExplainGrace 等 CLI 把失败行刷进日志再翻：state 翻转常常先
	// 于日志落盘几十毫秒。
	failExplainGrace = 200 * time.Millisecond
)

// failExplainLogDir 解析 CLI 日志目录；nil = 生产缺省 ~/.zcode/cli/log。
// 测试钩子——指向夹具目录即离线可测。
var failExplainLogDir func() (string, error)

// explainFail 在失败终点后异步补一行底层真因。fire-and-forget：调用
// 方（onState 的失败分支）在读循环栈上，这里的一切等待都不许回去。
// explainWG 与 d.wg 分家：Stop 收拢的是「不许越过停机线」，explainWG
// 只跟补遗 goroutine 本身——测试缝（pointFailLogAt 的还原）等的是它，
// 不该陪 patrol/watchdog 这些常驻 goroutine 一起等到停机。
func (d *Dispatcher) explainFail(m *member) {
	d.wg.Add(1) // Stop 收拢在飞的补遗——不越过停机线
	d.explainWG.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.explainWG.Done()
		util.Guard("dispatch: fail explain", func() {
			time.Sleep(failExplainGrace)
			if d.stopped() {
				return
			}
			msg := latestModelFail(d.sessionOf(m))
			if msg == "" {
				return
			}
			// 临界区闭包化（defer 解锁）：panic 也放掉 d.mu，不楔死调度器。
			fresh := func() bool {
				d.mu.Lock()
				defer d.mu.Unlock()
				fresh := m.watch.failExpText != msg || time.Since(m.watch.failExpAt) >= failExplainEvery
				m.watch.failExpAt, m.watch.failExpText = time.Now(), msg
				return fresh
			}()
			if !fresh {
				return
			}
			d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
				Text: clipMiddle(i18n.Sf("底层错误：%s", msg), 4096)}})
			d.sayThrough(m, i18n.Sf("（底层错误：%s）", clipMiddle(msg, 512)), nil)
			d.stampRowUnread(m.name, d.sessionOf(m))
		})
	}()
}

// drainExplains blocks until every in-flight explain goroutine has
// finished — the test seam's restore point (pointFailLogAt swaps the
// global log-dir hook back only after this, so a slow 200ms-grace
// goroutine can never read a restored prod default mid-flight).
func (d *Dispatcher) drainExplains() { d.explainWG.Wait() }

// latestModelFail 翻当天 CLI 日志的尾巴，找该会话最近一条 model 层失败
// 的 statusMessage——provider 的真因（连接超时带主机端口、并发限流带
// 规则细节）只有这里写得全。翻不到（没有日志/没有该会话的失败）就空串
// 静默。文件名 zcode-YYYY-MM-DD.jsonl 按字典序即日期序，取最大者。
func latestModelFail(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	dir := failExplainDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "zcode-") && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	f, err := os.Open(filepath.Join(dir, names[len(names)-1]))
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := int64(0)
	if st.Size() > failExplainTail {
		off = st.Size() - failExplainTail
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return ""
	}
	type failEntry struct {
		SessionID string `json:"sessionId"`
		Event     string `json:"event"`
		Context   struct {
			StatusMessage string `json:"statusMessage"`
		} `json:"context"`
	}
	last := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, sessionID) { // 廉价预滤：会话 id 不在行内直接跳过
			continue
		}
		var fe failEntry
		if json.Unmarshal([]byte(line), &fe) != nil {
			continue // 半行（日志正被写入）或别的记录形状：跳过
		}
		if fe.Event != "model.network.failed" && fe.Event != "model.request.failed" {
			continue
		}
		if fe.Context.StatusMessage != "" {
			last = fe.Context.StatusMessage // 扫到尾，留最后一条
		}
	}
	return last
}

// failExplainDir 解析 CLI 日志目录（测试钩子优先）。
func failExplainDir() string {
	if failExplainLogDir != nil {
		dir, err := failExplainLogDir()
		if err == nil {
			return dir
		}
	}
	dir, err := zcode.HomePath("cli/log")
	if err != nil {
		return ""
	}
	return dir
}
