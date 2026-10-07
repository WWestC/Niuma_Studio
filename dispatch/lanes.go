package dispatch

// lanes.go — 投递车道全家：停车/并车/三档批量旋钮、laneKick 发车、
// 投递组装（deliverQuoted）、读/回执货单、持久化 inbox。拆自
// dispatcher.go（v2.15 结构整理）。

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/wire"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// Member run states (the idle/running pair; the six-value session
// status collapses into these for the room's surface).
const (
	StatusIdle    = "idle"    // subscribed, waiting for work
	StatusRunning = "running" // a turn is in flight
)

// Permission policies for reverse requests (member sessions are yolo,
// so ordinary tools never ask; the alwaysAsk class — ExitPlanMode,
// CreateWorkflow, AskUserQuestion — still does).
const (
	PermAsk   = "ask"   // forward to the room @房主, auto-deny after the window (default)
	PermAllow = "allow" // answer allow/accept without asking
	PermDeny  = "deny"  // answer deny/decline without asking
)

// Lane caps: the next-turn queue and the inject lane are bounded so a
// chatty room can never balloon a member's pending state.
const (
	QueueCap  = 32
	InjectCap = 8
)

// laneEntry is one parked line with everything that rides it. The
// pre-refactor era kept SIX index-parallel slices (queue/queueAt/
// queueFrom/readq/ackq/imgq) whose alignment was a hand-kept
// discipline — every push/pop/fold/trim synced them in lockstep and
// loadLanes padded five defensive blocks. Folding them here makes
// alignment a type guarantee: the only alignment code left is the
// inbox-file boundary (saveLanes/loadLanes), which still speaks the
// legacy parallel JSON shape for disk compatibility.
type laneEntry struct {
	text string // the parked line (a folded entry grows in place, L2)
	// at stamps the entry's arrival (unix seconds; 0 = a legacy
	// pre-upgrade entry or a fresh in-flight line). The stamp is the
	// staleness truth: a line that waited past StaleDelay ships with a
	// 排队说明 header, and the turn's reply auto-quotes the line it
	// answered (引用 #N). Merges keep the FIRST line's stamp — ages
	// are never laundered.
	at int64
	// from carries the entry's sender ("" = a dispatcher line —
	// patrol, resume, re-parked blobs; never merged). L2 same-sender
	// folding keys on it; L4 backpressure credits the note (and the
	// overflow receipt) to the right seat.
	from string
	// read: the room-message seqs the line carries (飞书式已读货) — a
	// successful bridge.Send marks exactly these. 0/nil = not a room
	// message (announcements, patrol/daily triggers).
	read []int64
	// ack: the ADDRESSED say seqs behind the line (飞书式收到货) — the
	// narrower cargo a bare 「收到」 reply converts. Background-flush
	// seqs never ride here — 收到 answers the 点名 line, not the 广播
	// it happened to travel with.
	ack []int64
	// imgs: the line's picture cargo (输入图片) — a busy member's
	// parked say keeps its pictures so the eventual injection still
	// carries them (re-parks never launder cargo away).
	imgs []wire.Image
}

// injectEntry is the background lane's entry: one context-only line
// (prefixed onto the next send, never owed a turn) and the room seq
// it carries (0 = not a room message: no receipt to earn).
type injectEntry struct {
	text string
	read int64
}

// FoldCap is 并车投递's per-injection ceiling: how many foldable lane
// entries (stale OR coalesced siblings — see deliverQuoted) one
// delivery may carry besides its own head line. The cap keeps a deep
// backlog from dumping a dozen instructions into one turn — a reply
// can only meaningfully address a handful, the rest would just launder
// noise through the model.
const FoldCap = 5

// --- v2.11 车道优化旋钮（L1/L2/L3，全部可注入可关） ------------------
//
// 第一性口径：投递问题的本质是 λ（消息供给率）对 μ（回合消化率）。
// 批量决策的真信号是「交互性是否还在」，墙钟年龄只是它的粗代理——
// 汇聚窗按「到达结构」并车（近同时到达的点名线是一批），同发连发
// 按「发送方结构」并车（同人连发多半是一条思路的分段），深度退化
// 按「负载结构」调批量（积压越深，交互性越不值钱、μ 越优先）。

// DefaultCoalesceWindow is L1 汇聚窗: a freshly parked head line waits
// out this window before its kick, so near-simultaneous mentions (A
// and B both @C within seconds) collect into one turn instead of
// burning a full model roundtrip each — the lines fold as siblings at
// delivery (each keeps its own 来源 header, FIFO order intact). The
// cost is bounded by the window itself and sits far under both a
// turn's latency and human perception; a var (not a const) purely as
// the test-binary seam (seat_lease_test.go's TestMain zeroes it — the
// legacy suite's 3s waitFor deadlines must not inherit a 2s delay).
// Config.CoalesceWindow: 0 = this default, negative = off (kick at
// once, the pre-v2.11 behavior).
var DefaultCoalesceWindow = 2 * time.Second

// DefaultSenderFoldWindow is L2 同发送方连发合并: consecutive lines
// from the SAME sender parked closer than this fold into one lane
// entry (the supplement is appended verbatim under a marker — the
// system never rewrites a speaker's words). Same-sender only: cross-
// sender merges would blur attribution, while one speaker's rapid-fire
// bursts are almost always one thought split across sends. Ages are
// not laundered — the merged entry keeps its FIRST line's stamp.
const DefaultSenderFoldWindow = 30 * time.Second

// L3 自适应并车: the fold staleness threshold degrades with lane
// depth (idle lanes keep the one-per-turn rhythm; drowning lanes batch
// aggressively — Little's law applied to a turn-budgeted queue), the
// per-batch cap steps up to match, and the fold section carries a
// rune budget so five monster lines cannot balloon one turn the way
// the background lane's injectBudget already forbids.
const (
	// foldDeepAt is the depth at which the threshold bottoms out and
	// the cap steps up: the lane is officially drowning there.
	foldDeepAt = 8
	// foldFloor is the threshold's floor: never batch below this — a
	// live back-and-forth dialogue must keep its per-turn rhythm even
	// under load.
	foldFloor = 10 * time.Second
	// foldCapDeep is FoldCap's drowned-lane ladder step.
	foldCapDeep = 8
	// foldBudget caps the fold section's runes per delivery. Budget
	// stops the fold — an entry that would blow the budget waits its
	// own turn (addressed lines are never dropped, only deferred).
	foldBudget = 6000
)

// coalesceWindow resolves cfg.CoalesceWindow (0 = the package default,
// negative = off).
func (d *Dispatcher) coalesceWindow() time.Duration {
	if d.cfg.CoalesceWindow < 0 {
		return 0
	}
	if d.cfg.CoalesceWindow == 0 {
		return DefaultCoalesceWindow
	}
	return d.cfg.CoalesceWindow
}

// senderFoldWindow resolves cfg.SenderFoldWindow (same shape as
// coalesceWindow; negative = off).
func (d *Dispatcher) senderFoldWindow() time.Duration {
	if d.cfg.SenderFoldWindow < 0 {
		return 0
	}
	if d.cfg.SenderFoldWindow == 0 {
		return DefaultSenderFoldWindow
	}
	return d.cfg.SenderFoldWindow
}

// foldAfter resolves the fold staleness threshold (seconds) at a given
// lane depth: base at depth ≤ 2, foldFloor at depth ≥ foldDeepAt,
// linear in between. The never-late sentinel survives — StaleDelay<0
// means never fold by staleness, whatever the depth (the coalescing
// rules stay alive on their own signals).
func (d *Dispatcher) foldAfter(depth int) int64 {
	base := d.staleAfter()
	if base >= neverLate { // never-late sentinel: pass through untouched
		return base
	}
	if depth <= 2 {
		return base
	}
	if depth >= foldDeepAt {
		return int64(foldFloor / time.Second)
	}
	floor := int64(foldFloor / time.Second)
	return base - int64(depth-2)*(base-floor)/(foldDeepAt-2)
}

// foldCapFor is the per-batch fold ceiling at a given depth: FoldCap
// normally, foldCapDeep once the lane is drowning.
func foldCapFor(depth int) int {
	if depth >= foldDeepAt {
		return foldCapDeep
	}
	return FoldCap
}

// ackTeachFirsts / ackTeachEvery are L5 会话闩: the 收到-discipline
// footer rides the first ackTeachFirsts room-cargo deliveries (立规
// 矩), then one refresher every ackTeachEvery (long contexts dilute
// protocols taught at birth) — not every delivery, which would just be
// per-turn instruction noise. Folded multi-section deliveries are
// exempt from the latch: their footer is this turn's reply-shape
// operating guidance, not a refresher.
const (
	ackTeachFirsts = 3
	ackTeachEvery  = 25
)

// imgCapPerDelivery is P3 图片单轮预算: vision tokens dwarf text, and
// a fold stacks several lines' pictures into one turn — the delivery
// carries at most this many, the rest degrade to an honest note (the
// line itself is never dropped; the sender can resend in batches).
const imgCapPerDelivery = 3

// imgCapNoteMark is the image-cap note's idempotence marker (a failed
// delivery re-parks its assembled text — the note must not stack).
const imgCapNoteMark = "未随本轮送达"

// injectBudget is the background lane's rune budget for one injection:
// 背景是「无需回应」的顺带上下文，不该挤占点名正文的体量——InjectCap
// 只限条数（8 条 × 每条 2000 字的上限，最坏一万六），一条注入仍能无限
// 长。超预算从最旧一条让位（新的比旧的重要，与聊天环「保最近」同口
// 径），让位不静默：计数与房史回看指针留在注入面上（injDropped 连
// InjectCap 的截断一起记），原文在 history 一条不少。
const injectBudget = 4000

// clipBackground trims the background block to injectBudget runes,
// oldest first (newest context wins — the chat ring's 保最近
// discipline), always keeping at least the newest line so the block
// never renders empty.
func clipBackground(bg []injectEntry) ([]injectEntry, int) {
	total := 0
	for _, e := range bg {
		total += len([]rune(e.text))
	}
	if len(bg) == 0 || total <= injectBudget {
		return bg, 0
	}
	drop := 0
	for total > injectBudget && len(bg) > 1 {
		total -= len([]rune(bg[0].text))
		bg = bg[1:]
		drop++
	}
	return bg, drop
}

// deliver hands one addressed line to the member: send now, queue on
// busy, reactivate on cold sessions. A pending 【能力更新】 header
// (the prompt slot's next-injection effect) is prepended first, and
// the fabric's tool denylist (the tool slot) rides the send. seqs names
// the room messages the text carries (nil for the non-room injections
// — patrol, daily, birth): the turn a successful Send starts now OWNS
// those seqs as read cargo — the hub's read ledger hears them at the
// turn's terminal (onEvent/onState), not here: accepted ≠ read. acks
// is the narrower 收到 cargo — the ADDRESSED seqs only, never the
// background lines the flush folds into the same delivery — arming the
// turn for a bare 「收到」 reply's receipt conversion.
func (d *Dispatcher) deliver(name, text string, seqs, acks []int64) {
	d.deliverQuoted(name, text, seqs, acks, 0, nil)
}

// deliverImg is deliver with the line's picture cargo (输入图片): the
// images ride the injection as session/send attachments and the queue
// lane on a busy member (the deliverQuoted discipline).
func (d *Dispatcher) deliverImg(name, text string, seqs, acks []int64, imgs []wire.Image) {
	d.deliverQuoted(name, text, seqs, acks, 0, imgs)
}

// deliverQuoted is deliver with the line's lane stamp (unix seconds; 0
// = fresh, never queued — every direct deliver) and picture cargo.
// queuedAt past StaleDelay ships the line with a 排队说明 header so the
// member can judge a stale instruction by the clock, and arms the
// terminal's auto-quote (a reply to one late addressed line prefixes 引用 #N).
func (d *Dispatcher) deliverQuoted(name, text string, seqs, acks []int64, queuedAt int64, imgs []wire.Image) {
	m := d.lookup(name)
	if m == nil {
		return
	}
	// 房间暂停：直达投递（巡逻/日报/会议/私信等）一律停进车道，带原
	// 队龄戳——resume 后按序投出，排队说明的年龄口径不洗新。
	if d.pausedNow() {
		d.enqueueAt(m, text, seqs, acks, queuedAt, imgs)
		return
	}
	if queuedAt <= 0 {
		queuedAt = time.Now().Unix() // a fresh line stamps now: a bounce into the queue must not launder age to zero
	}
	if wait := time.Now().Unix() - queuedAt; wait > d.staleAfter() {
		text = fmt.Sprintf(
			"【排队说明】这条消息 %s 发出，因你上一轮未结束而排队等待，等了约 %s 现在才送达——请结合当前时间与语境判断是否仍需要执行。\n\n%s",
			util.ClockTime(queuedAt), util.HumanWait(wait), text)
	}
	cargo := positiveSeqs(acks)
	d.mu.Lock()
	out := append([]int64(nil), seqs...)
	consumed := false
	if len(m.lanes.inject) > 0 {
		// 回看预算：背景块裁到 injectBudget rune（最旧先让位），被让位
		// 的行没送达——read cargo 只带留下的，不把没见过的行记成已读。
		inj, yielded := clipBackground(m.lanes.inject)
		m.lanes.injDropped += yielded
		header := ""
		if m.lanes.injDropped > 0 {
			header = fmt.Sprintf("（最早 %d 条背景超本轮回看预算未附——原文一条不少在房史，GET /p/%s/history 回看）\n",
				m.lanes.injDropped, d.historyKey())
			m.lanes.injDropped = 0
		}
		injText := make([]string, len(inj))
		for i, e := range inj {
			injText[i] = e.text
			out = append(out, e.read) // the flush reads only the lines it carries（0 照旧被 positiveSeqs 滤掉）
		}
		text = "【期间未点名的办公室广播（背景，无需回应）】\n" + header + strings.Join(injText, "\n") +
			"\n【点名给你的消息】\n" + text
		m.lanes.inject = nil
		consumed = true
	}
	if m.fab.pendingCap != "" {
		text = m.fab.pendingCap + "\n\n" + text
		m.fab.pendingCap = ""
		consumed = true
	}
	// 并车投递（v2.11 三处扩容）：车道里的线满足其一即折进本次注入——
	// ①陈线：排队超过阈值；②同窗兄弟：与队头到达相差在汇聚窗内
	// （近同时的点名是一批，交互性还没建立，一轮带走）。「一回合一消
	// 息」的口径不变（一次注入仍只开一轮），变的只是一轮可以捎走多条
	// ——房主实录（重启后五连点名）：全员互相 @ 的供给速度远超每回合
	// 一条的消化速度，待投只进不出，并成一车省掉的是每条一整轮的模
	// 型往返。阈值随车道深度退化（L3）：闲时保逐条节奏（谁的回合谁
	// 见），淹没时大口吞（交互性早没了，消化率优先）——深度 ≤2 用
	// StaleDelay 全额，≥foldDeepAt 退到 foldFloor，中间线性。护栏有
	// 三：只折「陈线或同窗」（新鲜异窗线保住逐条节奏）；一次至多折
	// cap 条（深度 ≥foldDeepAt 阶梯至 foldCapDeep）；折叠段字数过
	// foldBudget 停折（装不下的线留自己的回合——欠回复的线只准延
	// 后，不准丢，不变量①）。FIFO 不变——队头不满足即停，永远从队
	// 头顺序取，插队不存在。
	folded := 0
	var foldWaits []int64 // each folded entry's wait seconds (metrics)
	if len(m.lanes.queue) > 0 {
		depth := len(m.lanes.queue)
		thr := d.foldAfter(depth)
		capN := foldCapFor(depth)
		coSec := int64(d.coalesceWindow() / time.Second)
		now := time.Now().Unix()
		imgs = append([]wire.Image(nil), imgs...) // 折叠要长大：与入参底层数组分家，货不写进调用者口袋
		var section string
		runes := 0
		for ; len(m.lanes.queue) > 0 && folded < capN; folded++ {
			e := m.lanes.queue[0]
			if e.at <= 0 {
				break // 零戳＝预戳文件的老线，按新鲜对待（与迟到头同一口径）
			}
			// 同窗兄弟的锚点是本次投递头线的到达（queuedAt，入口已归
			// 一）——与头线相差在汇聚窗内的线是一批，哪怕逐条各自到达。
			gap := e.at - queuedAt
			stale := now-e.at > thr
			sibling := coSec > 0 && gap >= -coSec && gap <= coSec
			if !stale && !sibling {
				break // 队头还新鲜且异窗：后面纵有陈年货也按序等自己的回合
			}
			if runes+len([]rune(e.text)) > foldBudget {
				break // 字数预算已满（首条同检——怪物头线不得独占一轮，
				// 留自己的回合做主线，主线无预算）：不丢线
			}
			section += fmt.Sprintf("\n\n〔%d〕%s 发出，等了约 %s：\n%s",
				folded+1, util.ClockTime(e.at), util.HumanWait(now-e.at), e.text)
			runes += len([]rune(e.text))
			foldWaits = append(foldWaits, now-e.at)
			out = append(out, e.read...)
			cargo = append(cargo, positiveSeqs(e.ack)...) // 滤零与入参 acks 同纪律
			imgs = append(imgs, e.imgs...)
			m.lanes.queue = m.lanes.queue[1:]
		}
		if folded > 0 {
			text += fmt.Sprintf(
				"\n\n【排队积压一并送达】你上一轮期间还有 %d 条消息（陈线或近时同到）随本条一并给你——请逐条结合当前时间与语境判断是否仍需执行：%s",
				folded, section)
			consumed = true
		}
	}
	// L5 会话闩（锁内定夺——ackTaught 是受锁字段）：本轮携带房线货才
	// 计数；带不带 footer 按「头 ackTeachFirsts 轮＋每 ackTeachEvery 轮
	// 复习＋并车多节永带（回复结构的操作指引）」定。
	teachAck := false
	if len(positiveSeqs(cargo)) > 0 {
		teachAck = folded > 0 || m.lanes.ackTaught < ackTeachFirsts || (m.lanes.ackTaught+1)%ackTeachEvery == 0
		m.lanes.ackTaught++
	}
	var inboxBar <-chan struct{}
	pendingSave, haveSave := inboxJob{}, false
	if consumed {
		// 消费即落盘（重启回生防线）：车道条目一旦折进本次注入文本，
		// 磁盘必须先清掉——成功路径若等下一次 saveLanes 才写，进程死
		// 在窗口内会让已捎带的广播/头在重启读档时整条复活、二次捎带。
		// 快照在锁内取、入队在锁外做（prepareInboxSaveOrdered 的序号
		// 制保住次序）；Send 出发前在锁外等落地。
		pendingSave, haveSave = d.prepareInboxSaveOrdered(m), true
	}
	d.mu.Unlock()
	if haveSave {
		inboxBar = d.enqueueInboxSave(pendingSave)
	}

	// L5 收到纪律的投递面 footer（v2.11，带会话闩）：单车单节出单线口
	// 径，并车多节教「一轮一收到，回执挂每条」；带不带由锁内算好的
	// teachAck 定（头几轮＋周期复习＋多节永带）。幂等标记防重注：失败
	// 回停车道再投时文本里已带着 footer，不再叠第二遍。
	if teachAck && !strings.Contains(text, ackNoteMark) {
		if folded > 0 {
			text += "\n\n（若以上各条——含〔N〕各节——均只需知晓、无需回应或行动：整轮只回一次「收到」二字即可——系统会把收到挂成本轮每条原消息下的回执，不广播进办公室、不会再@任何人；也可以在你的回合里跑一次 `niuma ack` 命令，效果相同（二选一）；需要回应或行动的节请按〔N〕编号分节作答。）"
		} else {
			text += "\n\n（若这条消息只需知晓、无需回应或行动：整轮只回「收到」二字即可——系统会把收到挂到原消息下作为回执，不广播进办公室、不会再@任何人；也可以在你的回合里跑一次 `niuma ack` 命令，效果相同（二选一）；需要回应或行动则正常作答。）"
		}
	}

	// P3 图片单轮预算：vision token 远贵于文本，并车会把多条线的图叠进
	// 一轮；超出上限的降级为诚实注记（线不丢，图由发送方分条重发），
	// 逐线文案只说「附带」，送达与否以这里为准。
	if n := len(imgs); n > imgCapPerDelivery && !strings.Contains(text, imgCapNoteMark) {
		text += fmt.Sprintf("\n（注：本条共随附 %d 张图片，超出单轮 %d 张上限，其余 %d 张未随本轮送达——需要看图请让发送方分条重发）",
			n, imgCapPerDelivery, n-imgCapPerDelivery)
		imgs = imgs[:imgCapPerDelivery]
	}

	// 注入等待（设置卡「消息注入等待」，缺省 5 分钟）：ack 迟归多半
	// 是 app-server 在忙（回合在跑/会话冷启动），太急的假死判定会把
	// 已生效的输入误报成「结果未知」弃单。代价只是真死会话的播报
	// 晚几分钟——弃单不重试的纪律不变。
	ctx, cancel := context.WithTimeout(context.Background(), d.SendWait())
	defer cancel()
	// 停机解卡：注入等待进行中工作室停机（Stop 关 d.stop）时立刻撤
	// ctx——Stop 的 wg 收拢不能为一个死会话的 ack 等上一整个等待窗。
	// 撤销落在普通 err 分支：disarm＋回停车道，线随车道持久到下一轮
	// 生命（nil 的 d.stop——手搓 Dispatcher 的测试桩——此 select 恒
	// 阻塞，watch 关闭即回收，无副作用）。
	if d.stop != nil {
		watch := make(chan struct{})
		go func() {
			select {
			case <-d.stop:
				cancel()
			case <-watch:
			}
		}()
		defer close(watch)
	}
	// inputId must stay ASCII: the app-server rejects non-ASCII ids at
	// the provider boundary as an instant prompt_failed — and member
	// names here are Chinese. A stable name hash keeps the id
	// traceable without the bytes.
	nh := fnv.New32a()
	_, _ = nh.Write([]byte(name))
	inputID := fmt.Sprintf("dh-%08x-%d", nh.Sum32(), time.Now().UnixNano())
	// 输入图片：引用还原成附件（media.Store.Path→localPath）。解析不
	// 出的（仓库缺文件）只落文字注记——图片永远不拖垮正文投递。
	atts, missing := d.resolveAttachments(imgs)
	if missing != "" {
		text += "\n（注：" + missing + "）"
	}
	send := func() (*zcode.SendAck, error) {
		sid := d.sessionOf(m) // Rebirth 换会话在 d.mu 内写；闭包在 kick goroutine 上迟调
		if len(atts) > 0 {
			if ab, ok := d.bridge.(attBridge); ok {
				return ab.SendAttached(ctx, sid, text, inputID, nil, atts)
			}
		}
		return d.bridge.Send(ctx, sid, text, inputID, nil)
	}
	// 先武装，后发送（arm-before-send）：本轮的读货/回执货/车道戳与
	// Running 态在 Send 之前一次置好。车道 kick 把投递搬上了自己的
	// goroutine，回合终点事件（onEvent）随时可能在另一条 goroutine
	// 上到达——若装货留在 Send 之后，「已发出」与「已记账」之间插进
	// 一个终点，终点会扑空本轮货单：上一轮的 turnAck 残留漏给下一
	// 个终点，回答就错挂到旧线的 @上（reads 实测竞态）。先武装则
	// 「Send 已发出」即「本轮已记账」，终点永远看得到本轮的货；各
	// 失败分支 disarm 回滚到先前的货单与状态（停车分支的货本来就走
	// readq/ackq 随线持久，活槽位必须让干净）。
	//
	// 撞车让位（v2.14）：Running 成员身上活回合还没终局，货单槽是它
	// 的——本次直投（巡报/日报/加编通知/会议注入不带车道守卫）的货进
	// pending 队列，活回合终局清槽时晋升（takeReadCargo）。直接武装
	// 会把活回合的货洗掉：回复照进房、已读永远丢（房主实录：「请继
	// 续」回话了还挂未读，全案根因）。空货直投也一视同仁——旧代码把
	// 槽洗成 nil，同样是丢账的元凶。disarm/超时按 token 摘除，线没
	// 上路的货不能记。
	d.mu.Lock()
	prevStatus := m.status
	prevRead, prevTurnAck, prevTurnAt := m.cargo.readSeqs, m.cargo.turnAck, m.cargo.turnAt
	prevAckSeqs := m.cargo.ackSeqs
	reads := positiveSeqs(out)
	liveTurn := prevStatus == StatusRunning
	var pendToken int64
	if liveTurn {
		if len(reads) > 0 || len(cargo) > 0 {
			d.cargoTok++
			pendToken = d.cargoTok
			m.cargo.pend = append(m.cargo.pend, pendCargo{token: pendToken, readSeqs: reads, turnAck: cargo, at: queuedAt})
		}
	} else {
		m.cargo.readSeqs = reads
		m.cargo.turnAck = cargo
		m.cargo.turnAt = queuedAt // normalized at entry: the line's own arrival time
		m.cargo.ackSeqs = cargo
	}
	d.mu.Unlock()
	// v2.10 同步收口：亮灯前先落行。武装即开回合，注入原文行照旧等
	// ack（r_17 的送达纪律不破），但「投递中」的开场行必须与 chip 同
	// 一刻可见——注入等待窗（缺省 5 分钟）再拥塞，抽屉里也不能只剩上
	// 一轮的旧账或空态。
	if prevStatus != StatusRunning {
		d.turnOpen(m, i18n.S("回合开始——注入投递中…"))
	}
	d.setStatus(m, StatusRunning)
	disarm := func() {
		d.mu.Lock()
		if liveTurn {
			d.dropPendLocked(m, pendToken) // 撞车条目回停车道：pending 的货随线摘除
		} else {
			m.cargo.readSeqs, m.cargo.turnAck, m.cargo.turnAt, m.cargo.ackSeqs = prevRead, prevTurnAck, prevTurnAt, prevAckSeqs
		}
		d.mu.Unlock()
		d.setStatus(m, prevStatus)
	}
	if inboxBar != nil {
		<-inboxBar // 盘上先反映消费，线才上路（二次捎带防线；锁外等，房间不被磁盘拖住）
	}
	_, err := send()
	if zcode.IsCode(err, zcode.ErrSessionNotActive) {
		if rerr := d.reactivate(m); rerr != nil {
			log.Printf("[调度] %s 会话重激活失败：%v", name, rerr)
			disarm()
			d.enqueueAt(m, text, out, cargo, queuedAt, imgs)
			return
		}
		_, err = send()
	}
	if zcode.IsCode(err, zcode.ErrPromptRunning) {
		disarm()
		d.enqueueAt(m, text, out, cargo, queuedAt, imgs)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// 无 ack 超时 ≠ 未送达：请求早已写进 child 的 stdin，回合很可能
		// 正在跑——此时入队重试会让同一条指令在下一轮重放（「第N次招
		// PM」永动机的根因：每轮超时弹一进一，队列永不排空）。结
		// 果未知时宁可弃单播报，把重发的决定权交还房主；协议拒绝码
		// （-32004/-32010）与传输失败才是确定未送达，照旧走持久重试。
		log.Printf("[调度] %s 注入超时（ack 未归，输入可能已生效），不重试以免重复执行：%v", name, err)
		d.hub.System(i18n.Sf("[调度] 给 %s 的一条消息注入超时（结果未知）——为避免重复执行不再自动重试；如该成员迟迟无回应，请重发一次", name))
		// r_17 终端转录：超时的输入可能已生效——原文照留（input 行），
		// 加一枚 sys 行标注结果未知（弃单不重试的留痕）。双门（v2.9）：
		// 此刻 working 故意保持亮——抽屉里必须看得见「注入了什么、为何
		// 悬置」，否则就是一具亮着干活中的空抽屉。
		row := termInputRow(name, text)
		if n := len(imgs); n > 0 {
			row.Text += i18n.Sf("\n（附图 %d 张随注入送达）", n)
		}
		d.emitTrace([]chat.TraceEntry{
			row,
			{From: name, Kind: chat.TraceSys, Text: i18n.S("注入超时（ack 未归，结果未知）——已弃单不重试，等房主决定是否重发")},
		})
		// 弃单不清货（v2.14 改），不清 Running：请求早已写进 child 的
		// stdin（r_17 的判定前提），输入要么已被活回合读走、要么在
		// app-server 的队列里等下一回合——消费它的回合终局照常冲账。
		// 旧做法把货洗成 nil 是「回话了还挂未读」的第二案犯（房主实录：
		// 巡报撞车＋超时清货双杀）；「结果未知」已向房间披露，罕见
		// 的「输入真丢了」会记一枚迟到已读，两害取其轻。Running 保持
		// 亮着让看护继续探活（冷了走复活梯子，真长了走「长回合」提示）。
		d.pushQueued(m) // the lane no longer holds it — the chip must not say 排队中 forever
		return
	}
	if err != nil {
		log.Printf("[调度] %s 注入失败：%v（消息已排队）", name, err)
		// r_17：确定未送达的失败不落 input 行（排队重试，送达时再落），
		// 只留一枚 sys 行——终端里看得见这条线曾弹回停车道。双门（v2.9）。
		d.emitTrace([]chat.TraceEntry{
			{From: name, Kind: chat.TraceSys, Text: clipMiddle(i18n.Sf("注入失败，已排队重试：%v", err), 4096)},
		})
		disarm()
		d.enqueueAt(m, text, out, cargo, queuedAt, imgs)
		return
	}
	// the Send acked: the session now holds every line this delivery
	// carried. That is 送达, not 已读 — the turn this input starts owns
	// the seqs as read cargo (chat/reads.go hears them at the turn's
	// terminal) and the ack cargo a bare 「收到」 reply will convert
	// (chat/acks.go). turnAck/turnAt arm the terminal's auto-quote. The
	// arming itself already happened ahead of the Send (see above); a
	// success only settles the ledger.
	d.pushQueued(m) // the popped entry left the lane — in-flight is not queued
	// L6 度量：送达才计（失败回停车道的线等它真正上路再记，避免重试
	// 把同一等待记两遍）。头线一条＋折进各线各一条，等待分布由此拼
	// 出；并车条数一轮一记。
	d.mu.Lock()
	d.laneWaits = append(d.laneWaits, time.Now().Unix()-queuedAt)
	d.laneWaits = append(d.laneWaits, foldWaits...)
	if len(d.laneWaits) > 128 {
		d.laneWaits = d.laneWaits[len(d.laneWaits)-128:]
	}
	d.laneFolds = append(d.laneFolds, folded)
	if len(d.laneFolds) > 128 {
		d.laneFolds = d.laneFolds[len(d.laneFolds)-128:]
	}
	d.mu.Unlock()
	// r_17 终端转录的 IN 半边：注入成功即落 input 行——text 是本次投递
	// 的最终合成文（排队说明/背景捎带/装配头都在，会话看到什么就记
	// 什么），src 从行内标记提源；随行的图片附件以注记入册（附件本体
	// 走 session/send 附件通道，不在文本里）。v2.9 双门：这条行现在也
	// 进工作过程环——「干活中」亮起的同一刻，抽屉里就有这轮注入了什么。
	inRow := termInputRow(name, text)
	if n := len(imgs); n > 0 {
		inRow.Text += i18n.Sf("\n（附图 %d 张随注入送达）", n)
	}
	d.emitTrace([]chat.TraceEntry{inRow})
}

// resolveAttachments turns a delivery's picture cargo into
// session/send attachments: each media reference resolves through the
// warehouse to an absolute localPath (kind "image" — the child reads
// the file itself, no base64 on the wire). Unresolvable references
// (nil store, dropped file) come back as one honest note line for the
// injected text — a missing picture must never fail the words riding
// with it.
func (d *Dispatcher) resolveAttachments(imgs []wire.Image) ([]zcode.Attachment, string) {
	if len(imgs) == 0 {
		return nil, ""
	}
	var (
		atts    []zcode.Attachment
		missing []string
	)
	for _, im := range imgs {
		path := d.cfg.MediaStore.Path(im.ID)
		if path == "" {
			if im.Name != "" {
				missing = append(missing, im.Name)
			} else {
				missing = append(missing, im.ID)
			}
			continue
		}
		atts = append(atts, zcode.Attachment{
			Kind: "image", LocalPath: path,
			Filename: im.Name, MimeType: im.Mime, SizeBytes: im.Bytes,
		})
	}
	if len(missing) > 0 {
		return atts, "图片 " + strings.Join(missing, "、") + " 已不可用（原文件不在媒体仓库），仅文字送达"
	}
	return atts, ""
}

// enqueue parks a line on the member's next-turn lane stamped NOW, and
// pushes the member's queued set to the hub (the 排队中 chip lights).
func (d *Dispatcher) enqueue(m *member, text string, seqs []int64, acks []int64) {
	d.enqueueFrom(m, "", text, seqs, acks, time.Now().Unix(), nil)
}

// enqueueAt is enqueue with the line's ORIGINAL lane stamp — re-parks
// (busy, cold, transport failure) must preserve the age a late line
// has already waited, or a retry loop would launder staleness away.
// from stays "" (a re-parked blob is already assembled — merging
// anything further into it would blur whose words are whose).
func (d *Dispatcher) enqueueAt(m *member, text string, seqs []int64, acks []int64, at int64, imgs []wire.Image) {
	d.enqueueFrom(m, "", text, seqs, acks, at, imgs)
}

// enqueueFrom is the lane's single write face (callers hold no lock):
// parks one line with its sender, folding same-sender rapid-fire
// supplements into the tail entry (L2), clipping the overflow with a
// room-grade notice instead of silent drops (L4), and lighting the
// senders' congestion note when the lane deepens past the watermark
// (L4). The three emissions (overflow system line, sender notes) run
// AFTER the lock — hub/background calls take locks of their own.
func (d *Dispatcher) enqueueFrom(m *member, from, text string, seqs []int64, acks []int64, at int64, imgs []wire.Image) {
	d.mu.Lock()
	// L2 同发送方连发合并：同人短窗内的下一条并进尾节——归因零风险
	// （都是同一个人说的），读/回执货与图片随节合并，队龄保持首条
	// 的戳（年龄不许被合并洗新）。跨发送方、系统线（from="")、超窗的
	// 一概照旧各占一位——谁的回合谁见的逐条节奏只让给「同一个人的一
	// 条思路」。
	if w := int64(d.senderFoldWindow() / time.Second); w > 0 && from != "" && len(m.lanes.queue) > 0 {
		tail := &m.lanes.queue[len(m.lanes.queue)-1]
		if tail.from == from && tail.at > 0 && at >= tail.at && at-tail.at < w {
			tail.text += "\n" + senderFoldMarker + "\n" + text
			tail.read = append(tail.read, seqs...)
			tail.ack = append(tail.ack, acks...)
			tail.imgs = append(tail.imgs, imgs...)
			d.saveLanes(m)
			d.mu.Unlock()
			d.pushQueued(m)
			return
		}
	}
	m.lanes.queue = append(m.lanes.queue, laneEntry{text: text, at: at, from: from, read: seqs, ack: acks, imgs: imgs})
	d.laneDepths = append(d.laneDepths, len(m.lanes.queue))
	if len(d.laneDepths) > 128 {
		d.laneDepths = d.laneDepths[len(d.laneDepths)-128:]
	}
	// L4 溢出：超容裁旧不再静默——被裁的线从未送达，欠的回复不能凭
	// 空蒸发（不变量①）。房里落一条系统行（房主看得见沉默的原因），
	// 发送方若是成员，其背景车道再记一笔（下一轮自然知道重发或改道）。
	var overflow []string // rendered clips of the dropped lines, room-line fodder
	var overflowSenders []string
	if len(m.lanes.queue) > QueueCap {
		n := len(m.lanes.queue) - QueueCap
		for i := 0; i < n; i++ {
			if f := m.lanes.queue[i].from; f != "" {
				overflow = append(overflow, f+"「"+clipRunes(m.lanes.queue[i].text, 40)+"」")
				overflowSenders = append(overflowSenders, f)
			} else {
				overflow = append(overflow, "「"+clipRunes(m.lanes.queue[i].text, 40)+"」")
			}
		}
		d.laneDropped += int64(n)
		m.lanes.queue = m.lanes.queue[n:]
	}
	// L4 拥塞水位：深度越过 watermark（或再涨 4）才开口——稳定系统的
	// 负反馈要说话，但不能每个到达都说一遍。
	noteSender := ""
	depth := len(m.lanes.queue)
	if depth < backpressureAt {
		m.lanes.backNoted = 0
	} else if depth >= backpressureAt && depth >= m.lanes.backNoted+4 {
		m.lanes.backNoted = depth
		noteSender = from
	}
	d.saveLanes(m)
	d.mu.Unlock()
	d.pushQueued(m)
	if len(overflow) > 0 {
		// SystemRecorded（广播＋入史，不进 say feed）：溢出是房级事件，
		// 房主晚些回看也要看得见沉默的原因。
		d.hub.SystemRecorded(i18n.Sf("【积压溢出】%s 的待投队列已满（%d 条），最早的 %d 条被裁未送达：%s——请改 @ 其他空闲成员或稍后重发",
			m.name, QueueCap, len(overflow), strings.Join(overflow, "、")))
		seen := map[string]bool{}
		for _, f := range overflowSenders {
			if seen[f] {
				continue
			}
			seen[f] = true
			d.background(f, fmt.Sprintf(
				"【投递失败】你在 %s 积压中的较早消息已被裁剪未送达（%s 的待投队列满 %d 条）——若仍需要对方处理，请重发或改 @ 其他空闲同事。",
				m.name, m.name, QueueCap))
		}
	}
	if noteSender != "" {
		d.noteCongestion(noteSender, m.name, depth)
	}
}

// noteCongestion delivers the congestion note to one sender: a member
// sender gets a background-lane line (zero new turns, zero room noise
// — their NEXT turn simply knows better than to pile on); anyone else
// (the owner, a grace ghost) reads the room's system line instead.
func (d *Dispatcher) noteCongestion(sender, target string, depth int) {
	d.mu.Lock()
	_, member := d.members[sender]
	d.mu.Unlock()
	if member {
		d.background(sender, fmt.Sprintf(
			"【拥塞提示】你发给 %s 的消息已入队：%s 当前有 %d 条待投积压（一轮消化一条，陈线会并车）。若非紧急，建议等 %s 消化、或改 @ 其他空闲同事，避免继续追加。",
			target, target, depth, target))
		return
	}
	d.hub.SystemRecorded(i18n.Sf("【拥塞提示】%s 的待投队列已达 %d 条——新的点名将持续排队等待消化", target, depth))
}

// clipRunes keeps the first n runes plus an ellipsis (the ack-digest
// clipAskRunes discipline, generalized for the overflow notice).
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// takeNext pops one queued line with both cargos, its lane stamp and
// its picture cargo (callers hold no lock; one per turn).
func (d *Dispatcher) takeNext(m *member) (string, []int64, []int64, int64, []wire.Image) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(m.lanes.queue) == 0 {
		return "", nil, nil, 0, nil
	}
	e := m.lanes.queue[0]
	m.lanes.queue = m.lanes.queue[1:]
	d.saveLanes(m)
	return e.text, e.read, e.ack, e.at, e.imgs
}

// laneKick hands the head of m's parked lane to a goroutine of its own
// when the member carries no live turn. Say-routing must never run a
// session Send on the caller's stack: the hub feeds its OnSay observers
// one line at a time on a single pump goroutine, so one hung injection
// (a cold session whose Send neither acks nor errors — the 房主实录
// freeze: 14:21 一条 @冷会话成员的注入挂起，其后全房间的 @点名
// 静默，泵被冻到注入等待超时) would stop every later mention's
// routing while the room keeps echoing lines — the freeze reads as
// 「艾特了没人理」. The kick keeps a hang per-member: only this lane
// waits, the pump moves on. The turn terminals (onEvent/onState) ride
// the same kick for a harder reason: they run ON the app-server read
// loop (zcode.Client 的 Hooks 契约——钩子不得同步回调 client)，a
// synchronous Send there waits for an ack only that same blocked loop
// can read — the wire self-deadlocks until the injection window burns
// out (反向请求连读都读不到，子进程退避重宣告；积压事件末了一秒
// 冲刷；ack「结果未知」弃单). draining serializes kicks (the queue stays
// the member's FIFO — a second line parks behind the in-flight Send),
// and a Running member is left to its turn's terminal, which pops the
// lane itself (one message per turn).
func (d *Dispatcher) laneKick(m *member) {
	d.mu.Lock()
	if d.stopped() {
		d.mu.Unlock()
		return
	}
	// 成员资格守卫（coalesceKick 同款）：lookup 与 kick 之间成员可能已
	// 被 seatGone 摘除——放行会把队头弹出落盘后无处投递，排队线被静
	// 默丢；折返则线随车道留档，召回可续。
	if d.members[m.name] != m {
		d.mu.Unlock()
		return
	}
	// 房间暂停：车道照旧收线，但一发不发——kick 是新回合的唯一入口，
	// 门关在这里，停在车道里的线等 Resume 重踢（FIFO 不变）。
	if d.pausedNow() {
		d.mu.Unlock()
		return
	}
	if m.lanes.draining {
		// 这次叫醒会被一记正在收尾的 kick（Send 已归、退出中）吞掉：
		// 回合终局的续投就这样丢过一次（满载实测）——终点弹线的 kick
		// 撞上 draining 直接折返，退出的 kick 不再补扫，线滞留车道等
		// 下一个外来触发。把意图记在账上，收尾的 kick 替它补这一脚。
		m.lanes.kickAfter = true
		d.mu.Unlock()
		return
	}
	if m.status == StatusRunning || len(m.lanes.queue) == 0 {
		d.mu.Unlock()
		return
	}
	// L1 汇聚窗：新鲜队头让出一个小窗再发车——近同时到达的点名
	// （A、B 先后 @C）收进同一轮，省掉每条一整轮的模型往返。窗只
	// 挂在「即将开新回合」的队头上：Running/空车道的守卫已在上面
	// 挡掉；零戳老线（预戳文件）年龄不可判，照旧立即发车。timer
	// 在位即已有一次待发（去重），发火即laneKick 自证所有守卫。
	if w := d.coalesceWindow(); w > 0 {
		if at := laneHeadAt(m); at > 0 {
			if wait := time.Until(time.Unix(at, 0).Add(w)); wait > 0 {
				if m.lanes.kickTimer == nil {
					m.lanes.kickTimer = time.AfterFunc(wait, func() { d.coalesceKick(m) })
				}
				d.mu.Unlock()
				return
			}
		}
	}
	m.lanes.draining = true
	d.mu.Unlock()
	d.wg.Add(1) // Stop 收拢在飞的 kick——车道写盘与广播不得越过停机线
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			m.lanes.draining = false
			again := m.lanes.kickAfter
			m.lanes.kickAfter = false
			d.mu.Unlock()
			if again {
				// 补上被吞的叫醒：Running/空车道由守卫自辨；补脚只弹
				// 一条（一回合一消息），失败重试纪律仍归 deliverQuoted。
				d.laneKick(m)
			}
		}()
		// 栅栏在两枚 defer 之内：panic 也先把 draining 复位再弃——
		// 线留在停车道等下一次外部叫醒，不越过停机线也不死锁。
		util.Guard("dispatch: lane kick", func() {
			if d.stopped() {
				return // 线留在停车道（车道持久），下一轮生命接手
			}
			next, seqs, acks, at, imgs := d.takeNext(m)
			if next == "" {
				return
			}
			// 出错/忙碌由 deliverQuoted 自己回停车道（enqueueAt）——kick
			// 只负责「不在调用者栈上送」这一件事，重试纪律不变。
			d.deliverQuoted(m.name, next, seqs, acks, at, imgs)
		})
	}()
}

// coalesceKick is the window timer's fire: clear the slot and hand the
// kick back to laneKick, whose guards re-decide everything (stopped,
// membership, draining, Running, and whether a NEWER head still owes
// window time). A departed member's timer dies here — a kicked-out
// seat must not fire a delivery on its way out the door.
func (d *Dispatcher) coalesceKick(m *member) {
	d.mu.Lock()
	m.lanes.kickTimer = nil
	still := d.members[m.name] == m
	d.mu.Unlock()
	if !still {
		return
	}
	d.laneKick(m)
}

// drainTerminal runs the turn-terminal tail — the deferred model
// switch, then the queued line's kick — on a goroutine of its own.
// flushModel→applyTier→SetModel is under the same read-loop contract
// as Send: onEvent/onState are driven synchronously by the app-server
// read loop (the Hooks contract in zcode/client.go), and SetModel's
// ack can only be read back by that very loop — run in place, a tier
// switch deferred to the terminal freezes the whole shared wire for
// its 30s window (the injection-wait freeze's smaller sibling, same
// family — see laneKick's comment for the pump-side ancestor).
// Chaining the switch BEFORE the kick keeps Assemble's deferral
// promise: the next injection leaves only after the new tier settles,
// so the next turn runs on it. A kick that started a turn before
// this runs re-parks the switch the Assemble way — wantModel stays
// for that turn's own terminal to flush.
func (d *Dispatcher) drainTerminal(m *member) {
	d.wg.Add(1) // Stop 收拢在飞的切换/注入——不越过停机线
	go func() {
		defer d.wg.Done()
		util.Guard("dispatch: terminal drain", func() {
			if d.stopped() {
				return
			}
			// 临界区闭包化（defer 解锁）：panic 也放掉 d.mu，不楔死调度器。
			idle := func() bool {
				d.mu.Lock()
				defer d.mu.Unlock()
				return m.status != StatusRunning
			}()
			if idle {
				d.flushModel(m)
			}
			d.laneKick(m)
		})
	}()
}

// pushQueued projects the member's current queue onto the hub's
// delivery-queue ledger (chat/queues.go): the say seqs parked on the
// lane, full-state replace — the 排队中 chip and the sidebar 待投
// badge read it. Called at every lane mutation and at adoption.
func (d *Dispatcher) pushQueued(m *member) {
	d.mu.Lock()
	var seqs []int64
	for _, e := range m.lanes.queue {
		seqs = append(seqs, e.read...)
	}
	d.mu.Unlock()
	d.hub.SetQueued(m.name, positiveSeqs(seqs)...)
}

// takeReadCargo drains the turn's read cargo at its terminal — the
// 已读 moment (chat/reads.go): the turn consuming those lines ended,
// so the member has actually read them. Also hands back the delivery's
// addressed seqs and lane stamp for the terminal's auto-quote arm.
// Every terminal drains, so a stale cargo can never bleed into the
// next turn (callers hold no lock; the takeAckCargo discipline).
// Draining also promotes the pending queue's head onto the vacated
// slot: a barge-in delivery's input is queued at the app-server behind
// this very turn, so it becomes the next turn by FIFO — its cargo
// waits one terminal further, then marks at its own (v2.14 撞车让位).
func (d *Dispatcher) takeReadCargo(m *member) (seqs, turnAck []int64, turnAt int64) {
	d.mu.Lock()
	seqs, turnAck, turnAt = m.cargo.readSeqs, m.cargo.turnAck, m.cargo.turnAt
	m.cargo.readSeqs, m.cargo.turnAck, m.cargo.turnAt = nil, nil, 0
	if len(m.cargo.pend) > 0 {
		pc := m.cargo.pend[0]
		m.cargo.pend = m.cargo.pend[1:]
		m.cargo.readSeqs, m.cargo.turnAck, m.cargo.turnAt, m.cargo.ackSeqs = pc.readSeqs, pc.turnAck, pc.at, pc.turnAck
	}
	d.mu.Unlock()
	return seqs, turnAck, turnAt
}

// dropPendLocked removes one pending cargo entry by token — the
// delivery is re-parking (protocol reject, transport failure), so its
// line never shipped and its read must never fire. Callers hold d.mu.
func (d *Dispatcher) dropPendLocked(m *member, token int64) {
	if token == 0 {
		return
	}
	for i, pc := range m.cargo.pend {
		if pc.token == token {
			m.cargo.pend = append(m.cargo.pend[:i], m.cargo.pend[i+1:]...)
			return
		}
	}
}

// takeAckCargo drains the turn's 收到 cargo at its terminal — every
// terminal, answered or not, so a stale cargo can never bleed into the
// next turn (callers hold no lock).
func (d *Dispatcher) takeAckCargo(m *member) []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := m.cargo.ackSeqs
	m.cargo.ackSeqs = nil
	return c
}

// SeatAck receipts EVERYTHING the member's current turn owes — the
// structured sibling of the bare 「收到」 reply: the one-shot CLI verb
// (niuma ack) routes here through the server's SeatAck tap, the
// dispatcher being the only one holding the turn→cargo mapping. The
// turn's reply/quote arm clears with it (those lines are answered by
// the receipts), so a later terminal finds nothing owed. Reports false
// when the member owes no room cargo (unknown name, or a turn whose
// lines carry none).
func (d *Dispatcher) SeatAck(name string) bool {
	d.mu.Lock()
	m := d.members[name]
	if m == nil {
		d.mu.Unlock()
		return false
	}
	cargo := positiveSeqs(m.cargo.ackSeqs)
	m.cargo.ackSeqs, m.cargo.turnAck = nil, nil
	d.mu.Unlock()
	if len(cargo) == 0 {
		return false
	}
	d.hub.MarkAck(name, cargo...)
	return true
}

// isAckReply reports whether a turn's reply is a bare acknowledgement —
// 「收到」, terminal punctuation aside. Strict about content, forgiving
// about form: the mention-wake injection teaches the exact two chars,
// and anything with actual words in it stays a real reply.
func isAckReply(content string) bool {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(content),
		"。．·，,！!？?～~；;")) == "收到"
}

// positiveSeqs filters out the non-message zeros (birth, patrol, daily
// injections carry nil or 0 seqs).
func positiveSeqs(seqs []int64) []int64 {
	var out []int64
	for _, s := range seqs {
		if s > 0 {
			out = append(out, s)
		}
	}
	return out
}

// laneHeadAt is the first parked entry's stamp (0 = empty lane or a
// zero-stamp pre-upgrade entry — both read as "unknown age, treat as
// fresh").
func laneHeadAt(m *member) int64 {
	if len(m.lanes.queue) == 0 {
		return 0
	}
	return m.lanes.queue[0].at
}

// LaneStats snapshots the lane metrics (bounded rings, newest last).
func (d *Dispatcher) LaneStats() LaneStatsSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return LaneStatsSnapshot{
		WaitSeconds:       append([]int64(nil), d.laneWaits...),
		QueueDepth:        append([]int(nil), d.laneDepths...),
		FoldedPerDelivery: append([]int(nil), d.laneFolds...),
		OverflowDropped:   d.laneDropped,
	}
}

// staleAfter resolves cfg.StaleDelay into the seconds a queue entry
// may wait before its deliver counts as LATE (0 = the default,
// negative = never late — the neverLate sentinel keeps every comparison
// a plain greater-than at the call sites).
func (d *Dispatcher) staleAfter() int64 {
	if d.cfg.StaleDelay < 0 {
		return neverLate
	}
	if d.cfg.StaleDelay == 0 {
		return int64(DefaultStaleDelay / time.Second)
	}
	return int64(d.cfg.StaleDelay / time.Second)
}
