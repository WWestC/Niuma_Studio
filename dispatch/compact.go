package dispatch

// compact.go — P0 周期性会话折叠：上下文仪表（ctx_estimate，上下文
// 用量仪表的活体口径）越过阈值线的空闲成员，由看护钟代发一次
// session/compact——摘要保留在办任务与关键决定，丢弃过程性细节。
//
//   - 阈值：staffing 旋钮 compact_ctx（0=关，缺省——折叠花一个模型
//     回合，得房主显式开）> 环境变量 NIUMA_COMPACT_CTX；合法域
//     5 万–1000 万（钳制），低于下限按未设置读——手滑值不许点亮
//     折叠循环。
//   - 节律：看护钟每拍一巡（4min 缺省）；同一成员冷却 1 小时（折叠
//     回合要跑一阵，且 ctx_estimate 要等折叠后的下一个采样轮才会
//     落下来，一小时内重发只会重复折叠）；Running 成员不折（服务端
//     本来就拒）；房间暂停时整个看护拍本来就停。
//   - 折叠回合的终点不进房：摘要照常生成（那是折叠的意义），但成员
//     的 say 镜像被 compacting 标记吞掉——房里只留发起/完成的档案行
//     （终端转录），聊天流不灌一大坨整理文本。

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

const (
	// compactCooldown caps the fold cadence per member.
	compactCooldown = time.Hour
	// compactFloor/compactCeil clamp the threshold's legal domain.
	compactFloor int64 = 50_000
	compactCeil  int64 = 10_000_000
)

// compactInstructions is the fold's own brief — what the summary must
// keep. The member's working state rides the summary; process chatter
// does not.
const compactInstructions = "这是系统发起的会话整理。请保留：当前在办任务与排期、未完成的承诺、重要决定及其理由、关键文件路径/分支/任务编号。可以丢弃：过程性讨论细节、已被取代的旧方案。整理完成后按原状态继续待命。"

// compactorBridge is the fold's optional bridge face (the attBridge
// pattern): the production zcode client speaks it; a test fake opts in.
type compactorBridge interface {
	Compact(ctx context.Context, sessionID, instructions string) error
}

// compactThreshold resolves the fold line: staffing 旋钮 compact_ctx >
// env NIUMA_COMPACT_CTX > 0 (off).
func (d *Dispatcher) compactThreshold() int64 {
	if d.staff != nil {
		if v := d.staff.SettingsOf(d.projectKey).CompactCtxTokens; v > 0 {
			return clampCompact(v)
		}
	}
	if raw := strings.TrimSpace(util.Env("COMPACT_CTX")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return clampCompact(n)
		}
	}
	return 0
}

func clampCompact(v int64) int64 {
	if v < compactFloor {
		return 0 // 太小的值按未设置读——折叠不许低空乱开
	}
	if v > compactCeil {
		return compactCeil
	}
	return v
}

// ctxGauges resolves the gauge rows for the fold pass (cfg stub > the
// zcode ledger's one-pass read; a failure logs and reads empty — the
// fold waits for a healthier tick, it never alarms the room).
func (d *Dispatcher) ctxGauges(ids []string) map[string]zcode.SessionGauge {
	if d.cfg.CtxGauge != nil {
		g, err := d.cfg.CtxGauge(ids)
		if err != nil {
			log.Printf("[看护] 折叠前读上下文仪表失败（本拍跳过折叠）：%v", err)
			return map[string]zcode.SessionGauge{}
		}
		return g
	}
	g, err := zcode.SessionGauges(ids)
	if err != nil {
		log.Printf("[看护] 折叠前读上下文仪表失败（本拍跳过折叠）：%v", err)
		return map[string]zcode.SessionGauge{}
	}
	return g
}

// CompactNow runs one fold pass (the watchdog's tail): every Idle
// member whose ctx_estimate crossed the line and whose cooldown
// elapsed gets one session/compact. The fold IS a turn — the member
// flips Running (the lane holds; a concurrent mention parks behind it)
// until the fold's own terminal flips it back.
func (d *Dispatcher) CompactNow() {
	thr := d.compactThreshold()
	if thr <= 0 {
		return
	}
	cb, ok := d.bridge.(compactorBridge)
	if !ok {
		return
	}
	if d.stopped() || d.pausedNow() {
		return
	}
	now := time.Now()
	d.mu.Lock()
	ids := make([]string, 0, len(d.members))
	bySession := map[string]*member{}
	for _, m := range d.members {
		if m.status != StatusIdle || m.sessionID == "" {
			continue
		}
		if now.Sub(m.watch.compactAt) < compactCooldown {
			continue
		}
		ids = append(ids, m.sessionID)
		bySession[m.sessionID] = m
	}
	d.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	gauges := d.ctxGauges(ids)
	for sid, m := range bySession {
		g, known := gauges[sid]
		if !known || g.CtxEstimate < thr {
			continue
		}
		if d.stopped() || d.pausedNow() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := cb.Compact(ctx, sid, compactInstructions)
		cancel()
		d.mu.Lock()
		m.watch.compactAt = now // 成功与被拒都盖冷却：成功节律下一次折叠，被拒节律重试
		foldErr := err != nil
		if !foldErr {
			m.watch.compacting = true
		}
		d.mu.Unlock()
		if foldErr {
			log.Printf("[看护] %s 会话折叠发起失败（冷却后再试）：%v", m.name, err)
			continue
		}
		d.setStatus(m, StatusRunning)
		d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
			Text: clipMiddle(i18n.Sf("会话折叠已发起（活体上下文估计 %s，已跑 %d 回合）——保留在办与关键决定，丢弃过程性细节",
				humanTokens(g.CtxEstimate), g.Turns), 4096)}})
	}
}

// humanTokens renders a token count the manual's way (万 with one
// decimal below 亿).
func humanTokens(n int64) string {
	if n < 10_000 {
		return strconv.FormatInt(n, 10)
	}
	if n < 100_000_000 {
		return fmt.Sprintf("%.1f 万", float64(n)/10_000)
	}
	return fmt.Sprintf("%.2f 亿", float64(n)/100_000_000)
}

// foldTerminal swallows the fold turn's terminal: the summary stays in
// the session (that's the point of folding) instead of being mirrored
// into the room as the member's say. Reports whether it consumed the
// event.
func (d *Dispatcher) foldTerminal(m *member) bool {
	d.mu.Lock()
	folding := m.watch.compacting
	m.watch.compacting = false
	d.mu.Unlock()
	if !folding {
		return false
	}
	d.emitTrace([]chat.TraceEntry{{From: m.name, Kind: chat.TraceSys,
		Text: i18n.S("会话折叠完成——上下文已收敛为摘要（在办与关键决定保留）")}})
	return true
}
