package dispatch

// birth.go — 成员的出生/收养/回座/退役：Birth 双路（档案 v1 与编制 v2）、
// attach 与座位租约、重启接续。拆自 dispatcher.go（v2.15 结构整理）。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"encoding/json"
	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/capability"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// adopt resumes every stored member→session binding at boot: the
// seat re-joins (reclaiming any grace ghost after a room restart),
// the session resumes and subscribes in our app-server process, the
// durable lanes come back from disk. v2 P3-b: a project dispatcher
// adopts its staffing rows (occupying, session bound) — staffing is
// the binding's single truth; the v1 shape adopts the agents store's
// bindings.
func (d *Dispatcher) adopt() {
	if d.staff != nil {
		for _, row := range d.staff.ListByProject(d.projectKey) {
			if !row.Occupying() || row.SessionID == "" {
				continue
			}
			if err := d.attach(row.Person, row.ProjectRole, row.SessionID); err != nil {
				log.Printf("[调度] %s 在 %s 的绑定会话 %s 恢复失败：%v", row.Person, d.projectKey, row.SessionID, err)
				d.hub.System(i18n.Sf("[调度] %s 的会话暂不可用（%v）——已保留绑定，可重试召回", row.Person, err))
				continue
			}
			// only an interrupted birth (onboard) needs promoting here —
			// an active row is already in force.
			if row.State == staffing.StateOnboard {
				if _, err := d.staff.MarkActive(d.projectKey, row.Person); err != nil {
					log.Printf("[调度] %s 编制转 active 失败：%v", row.Person, err)
				}
			}
		}
		// 岗位锚迁移（一次性）：锚前时代的存量行按身份对表补锚——
		// 小检/小笔/小墨 那一代（角色串漂移、空 project_role）不必重
		// 生即入锚，看护从下一拍起按锚对账。
		if d.cfg.Docs != nil {
			d.anchorPosts()
		}
		return
	}
	if d.store == nil {
		return
	}
	for _, cfg := range d.store.ListFiltered(false) {
		if cfg.SessionID == "" {
			continue
		}
		if err := d.attach(cfg.Name, cfg.Role, cfg.SessionID); err != nil {
			log.Printf("[调度] %s 绑定会话 %s 恢复失败：%v", cfg.Name, cfg.SessionID, err)
			d.hub.System(i18n.Sf("[调度] %s 的会话暂不可用（%v）——已保留绑定，可重试召回", cfg.Name, err))
		}
	}
}

// anchorPosts is adopt's one-time 岗位锚迁移（存量收编）：every
// OCCUPYING row of this project that predates the anchor gets stamped
// when its identity — project_role, else the profile role — matches a
// table row: exact first, then a UNIQUE stem match (the 「·主笔」尾缀
// generation; a stem two rows share is ambiguous and stays legacy).
// The keeper's post-anchored count then sees them from the next beat,
// no rebirth needed. Unknown identities stay unanchored (the legacy
// exact-role reconcile keeps watching them); every stamp logs — a
// migration is an event, not a background fact.
func (d *Dispatcher) anchorPosts() {
	doc, err := d.cfg.Docs.Get(kb.EstablishmentDocKey(d.projectKey), 0)
	if err != nil {
		return // no table yet: nothing to anchor against（boot 收敛会补表）
	}
	rows, perr := kb.EstablishmentRows(doc.Body)
	if perr != "" {
		return // a broken table is the keeper's alarm, never our guess
	}
	byRole, byStem := map[string]string{}, map[string]string{}
	ambiguousStem := map[string]bool{}
	for _, r := range rows {
		if r.Role == "" {
			continue
		}
		if _, dup := byRole[r.Role]; !dup {
			byRole[r.Role] = r.Key
		}
		if stem := chat.RoleStem(r.Role); stem != "" {
			if _, dup := byStem[stem]; dup {
				ambiguousStem[stem] = true
			} else {
				byStem[stem] = r.Key
			}
		}
	}
	for _, row := range d.staff.ListByProject(d.projectKey) {
		if !row.Occupying() || row.PostKey != "" {
			continue
		}
		role := row.ProjectRole
		if role == "" && d.store != nil {
			if cfg, ok := d.store.Get(row.Person); ok {
				role = cfg.Role
			}
		}
		if role == "" {
			continue
		}
		key := byRole[role]
		if key == "" {
			if stem := chat.RoleStem(role); stem != "" && !ambiguousStem[stem] {
				key = byStem[stem]
			}
		}
		if key == "" {
			continue
		}
		if _, err := d.staff.SetPost(d.projectKey, row.Person, key); err != nil {
			log.Printf("[调度] %s 的岗位锚迁移失败：%v", row.Person, err)
			continue
		}
		log.Printf("[调度] %s 岗位锚迁移（%s）：身份「%s」→ 岗位 %s", row.Person, d.projectKey, role, key)
	}
}

// ReadoptMissing re-seats staffing rows the dispatcher is NOT carrying —
// the un-pause leg's find-back half. A member whose boot-time attach
// failed (session briefly unavailable on a cold app-server) holds no
// dispatcher seat; their room seat rode the snapshot ghost, and once
// that expired the roster lost them outright. An UNPAUSED room heals
// through the keeper (缺岗报警 → 自动补员召回); a paused room skips
// the keeper entirely, so its heal moment is the resume itself (v2.13
// 实录：暂停→重启→小人不见了). Idempotent: rows already carried are
// untouched (attach itself refuses twins).
func (d *Dispatcher) ReadoptMissing() {
	if d.staff == nil {
		return
	}
	d.mu.Lock()
	known := make(map[string]bool, len(d.members))
	for name := range d.members {
		known[name] = true
	}
	d.mu.Unlock()
	for _, row := range d.staff.ListByProject(d.projectKey) {
		if !row.Occupying() || row.SessionID == "" || known[row.Person] {
			continue
		}
		if err := d.attach(row.Person, row.ProjectRole, row.SessionID); err != nil {
			log.Printf("[调度] 恢复补座 %s 失败（%v）——保留绑定，等看护按编制核对", row.Person, err)
			continue
		}
		if row.State == staffing.StateOnboard {
			if _, err := d.staff.MarkActive(d.projectKey, row.Person); err != nil {
				log.Printf("[调度] %s 编制转 active 失败：%v", row.Person, err)
			}
		}
		d.hub.System(i18n.Sf("[调度] %s 已随办公室恢复归位", row.Person))
	}
}

// ContinueText is the dispatcher's own resume line for a member the exit
// snapshot caught mid-turn (重启自动接续): the card's 「让 TA 继续」
// button used to be the only path — now the dispatcher sends this itself
// at boot, the button stays as the owner's manual fallback.
const ContinueText = "【自动接续】工作室服务重启，你上一轮的工作随之中断（对话记忆完好）。请对照手头任务与台账，从断点继续；若上轮事项已经完成或不再需要，简要说明即可。"

// RenderResumeInjection is the frozen three-段 template (r_20 t_195,
// design/r20-resume §二). Empty sources degrade to ContinueText (§四
// 极端兜底——零回退).
func RenderResumeInjection(project string, files, commits, notes []string, filesAt, commitsAt, notesAt, interruptedAt, now int64) string {
	if len(files) == 0 && len(commits) == 0 && len(notes) == 0 {
		return ContinueText
	}
	stamp := func(t int64) string {
		if t <= 0 {
			return "未知时刻"
		}
		return time.Unix(t, 0).Format("15:04:05")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "【接续要点】now=%d 项目=%s。你上一回合被重启中断（%s），从这三段找回现场，无需重读全部：\n",
		now, project, stamp(interruptedAt))
	fmt.Fprintf(&b, "\n▸ 读什么（你手上的文件，采于 %s）：\n", stamp(filesAt))
	for _, f := range files {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprintf(&b, "\n▸ 记得什么（最近落库的决策，最新于 %s）：\n", stamp(commitsAt))
	for _, c := range commits {
		fmt.Fprintf(&b, "  %s\n", c)
	}
	fmt.Fprintf(&b, "\n▸ 干什么（你留下的下一步线索，最新于 %s）：\n", stamp(notesAt))
	for _, n := range notes {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	b.WriteString("\n规则：按线索接着干；要点的任何一条与你记忆冲突时，以台账为准（要点只是路标，台账是事实）；确认已读后从「干什么」段的第一条继续。")
	return b.String()
}

// ContinueInterrupted re-points the restart's interrupted seats at their
// work (重启自动接续): a member whose lane carries parked lines continues
// on those — the lines ARE the resume, a laneKick puts the head back on
// the road; an empty-lane member gets one ContinueText injection, parked
// then kicked like any lane entry so the Send stays off the caller's
// stack (a cold session must not hold the boot walk) and survives a
// failed boot attempt durably on disk. nil seqs, the patrol/daily
// discipline: no room line to read or ack. Unknown names are skipped —
// attach already told the room whose session refused to come back.
func (d *Dispatcher) ContinueInterrupted(names []string) {
	d.ContinueInterruptedNotes(names, nil)
}

// ContinueInterruptedNotes (r_20 t_195): the resume injection carries
// each member's distilled contextNotes when the farewell snapshot
// caught one (三段引用式——引用不概括, design/r20-resume §〇); an
// empty-note member falls back to the bare ContinueText exactly as
// before (零回退).
func (d *Dispatcher) ContinueInterruptedNotes(names []string, notes map[string]string) {
	for _, name := range names {
		m := d.lookup(name)
		if m == nil {
			continue
		}
		d.mu.Lock()
		queued := len(m.lanes.queue) > 0 // m.lanes.queue 的写点全在 d.mu 内——裸读与入队泵竞态
		d.mu.Unlock()
		if queued {
			d.laneKick(m)
			continue
		}
		text := ContinueText
		if raw := notes[name]; raw != "" {
			// raw 是 ResumeNotes JSON（server 侧冻结契约）——解析三段
			var rn struct {
				Files     []string `json:"Files"`
				Commits   []string `json:"Commits"`
				Notes     []string `json:"Notes"`
				FilesAt   int64    `json:"FilesAt"`
				CommitsAt int64    `json:"CommitsAt"`
				NotesAt   int64    `json:"NotesAt"`
			}
			if json.Unmarshal([]byte(raw), &rn) == nil {
				text = RenderResumeInjection(d.projectKey, rn.Files, rn.Commits, rn.Notes,
					rn.FilesAt, rn.CommitsAt, rn.NotesAt, time.Now().Unix()-3600, time.Now().Unix())
			}
		}
		d.enqueue(m, text, nil, nil)
		d.laneKick(m)
	}
}

// attach seats the member and activates the session binding.
func (d *Dispatcher) attach(name, role, sessionID string) error {
	d.mu.Lock()
	if _, exists := d.members[name]; exists {
		d.mu.Unlock()
		return errors.New(i18n.Sf("%s 已在调度中（受管成员无需重复出生；要重置请先移出或归档，再同名出生即复用档案）", name))
	}
	d.mu.Unlock()

	// Refuse to twin a LIVE seat of the same name (the -2 disease):
	// kick or wait for the old connection first. Grace ghosts are fine
	// — Join silently reclaims them, which is exactly the room-restart
	// adoption path (the snapshot restores ghosts, we re-seat them).
	grace := map[string]bool{}
	for _, n := range d.hub.GraceNames() {
		grace[n] = true
	}
	for _, m := range d.hub.Members() {
		if m.Name == name && !grace[m.Name] {
			return errors.New(i18n.Sf("%s 已有在座成员（%s），先踢出或等其离线再接入调度", name, m.Role))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := d.bridge.Subscribe(ctx, sessionID); err != nil {
		if !zcode.IsCode(err, zcode.ErrSessionNotActive) {
			return err
		}
		if _, err := d.bridge.ResumeSession(ctx, sessionID); err != nil {
			return errors.New(i18n.Sf("恢复会话 %s 失败：%v", sessionID, err))
		}
		if _, err := d.bridge.Subscribe(ctx, sessionID); err != nil {
			return errors.New(i18n.Sf("订阅会话失败（恢复后）：%v", err))
		}
	}

	seat, _ := d.hub.Join(name, role, false)
	// Persist the seat credential where the member's own one-shot CLI
	// commands (plan submit, task confirm …) read it: presenting it
	// takes this seat back for a moment (supersede) instead of
	// colliding into a "-2" twin that the anti-top-seat probe then
	// refuses. Reclaimed ghosts continue their old credential here,
	// so the file is re-stamped on every attach — the single truth.
	// 分项目键（chat.SaveSeatToken）：流转房不再互踩凭证（小苗-2 事故
	// 的形状——旧房幽灵持旧凭证、新房 CLI 持新房凭证，一把全局键必然
	// 顶错）。v1 单房形态（staff==nil）保持旧键 byte-for-byte。
	if d.staff != nil {
		chat.SaveSeatToken(d.projectKey, name, seat.SeatToken())
	} else {
		chat.SaveSeatToken("", name, seat.SeatToken())
	}
	m := &member{name: name, role: role, sessionID: sessionID, seat: seat, status: StatusIdle}
	// The seat's assembly rides along on every attach path (birth,
	// adopt, bind): a compose overflow degrades to the v1 base here
	// and only logs — Birth is where the room hears about it.
	if d.store != nil {
		if cfg, ok := d.store.Get(name); ok {
			if fabric, skillKeys, mcpKeys, err := d.composeFor(name, cfg); err != nil {
				log.Printf("[调度] %s 装配面料超限，按无装配基座接入：%v", name, err)
			} else if fabric != nil {
				m.fab.fabric, m.fab.skillKeys, m.fab.mcpKeys = fabric, skillKeys, mcpKeys
			}
		}
	}
	d.loadLanes(m)
	d.mu.Lock()
	d.members[name] = m
	d.mu.Unlock()
	d.pushQueued(m) // restored lanes re-light the 排队中 chips (the ledger is transient by design)
	// 归位即发车：恢复出来的停车道立刻把队头踢上路——不等房主点名，
	// 也不等第一拍看护。会话还忙的线照旧弹回停车（原时间戳保留），
	// FIFO 与一回合一消息的纪律不变，省掉的只是「压着线干等谁来踢」
	// 的那段空转（房主实录：重启后旧账全躺在车道上等人）。
	d.laneKick(m)

	d.wg.Add(1)
	go util.Guard("dispatch: seat watch", func() { d.watchSeat(name, seat) })
	return nil
}

// watchSeat detaches the member when their seat goes away (kick,
// offboard) — the binding survives for a later recall. v2 P3-b: on a
// project dispatcher the staffing row offboards with the seat (kick
// and the offboard pipeline both end here; the dispatcher seat never
// leaves on its own, so Done means removed). The one exception is a
// same-token supersede: the member's own one-shot CLI command took
// the seat for a moment (the seat-credential path, chat/seattoken.go)
// — that is a lease, not a departure, so the seat is re-taken and
// everything (binding, staffing row, task index) stays put.
func (d *Dispatcher) watchSeat(name string, seat *chat.Client) {
	defer d.wg.Done()
	select {
	case <-seat.Done():
	case <-d.stop:
		return
	}
	if seat.CloseReason() == chat.CloseReasonSuperseded {
		d.reseatLoop(name, seat)
		return
	}
	d.seatGone(name, seat)
}

func (d *Dispatcher) reseatPatience() time.Duration {
	if p := d.cfg.ReseatPatience; p > 0 {
		return p
	}
	return DefaultReseatPatience
}

// reseatLoop waits out the member's one-shot and re-takes the seat.
// JoinFree refuses rather than twins: a live same-name holder under a
// different credential can never spawn a "-2" through this path.
func (d *Dispatcher) reseatLoop(name string, dead *chat.Client) {
	deadline := time.Now().Add(d.reseatPatience())
	for {
		d.mu.Lock()
		m, ok := d.members[name]
		cur := dead
		if ok {
			cur = m.seat
		}
		d.mu.Unlock()
		// !ok：成员在顶座与首轮读册之间被离编（kick/offboard/耐心耗
		// 尽的 seatGone）——旧代码此处裸解引用 m.seat 直接 SIGSEGV。
		if !ok || cur != dead {
			return // re-seated by another path (sayThrough), or gone outright: converge
		}
		if _, held := d.hub.SeatHolderToken(name); !held {
			if d.reseatNow(name, dead, "") {
				return
			}
			// refused by a racing taker: fall through and keep waiting
		} else if time.Now().After(deadline) {
			scope := ""
			if d.staff != nil {
				scope = d.projectKey
			}
			tok := chat.LoadSeatToken(scope, name)
			holder, _ := d.hub.SeatHolderToken(name)
			if tok != "" && holder == tok {
				if d.reseatNow(name, dead, tok) {
					return
				}
			}
			log.Printf("[调度] %s 的座位被其他连接长期占用，按缺岗处理", name)
			d.seatGone(name, dead)
			return
		}
		select {
		case <-d.stop:
			return
		case <-time.After(reseatPoll):
		}
	}
}

// reseatNow takes the member's seat back once the name is free (or
// provably ours by credential). It swaps the member's seat under the
// lock and re-arms watchSeat; a member that left through another path
// while we waited hands the fresh seat straight back to grace.
func (d *Dispatcher) reseatNow(name string, dead *chat.Client, token string) bool {
	role := ""
	d.mu.Lock()
	m, ok := d.members[name]
	if ok {
		if m.seat != dead {
			// another path (sayThrough's mirror keep-alive, a racing
			// re-seat) already took the seat back: converge, never
			// bounce the fresh seat at patience expiry.
			d.mu.Unlock()
			return true
		}
		role = m.role
	}
	d.mu.Unlock()
	if role == "" {
		return true // member gone while we waited: nothing to re-seat
	}
	seat, _, ok := d.hub.JoinFree(name, role, false, token)
	if !ok {
		return false
	}
	if seat.Member().Name != name {
		// unreachable under JoinFree's refusal, kept as a hard stop so
		// a future refactor can never reintroduce the -2 disease here
		d.hub.Leave(seat)
		return false
	}
	if d.staff != nil {
		chat.SaveSeatToken(d.projectKey, name, seat.SeatToken())
	} else {
		chat.SaveSeatToken("", name, seat.SeatToken())
	}
	d.mu.Lock()
	m, exists := d.members[name]
	ours := exists && m.seat == dead
	if ours {
		m.seat = seat
	}
	d.mu.Unlock()
	if !ours {
		// offboarded/kicked while we waited: the seat is not ours
		d.hub.Leave(seat)
		return true
	}
	// 新座位记录出生 Working=false：回合若还在跑，chip 必须按调度器
	// 真相重烫（v2.10 同步收口——租约顶座期间丢的位在此补回）。
	d.reassertWork(m)
	d.wg.Add(1)
	go util.Guard("dispatch: seat watch", func() { d.watchSeat(name, seat) })
	return true
}

// seatGone is the seat's real death (kick, offboard, a stranger that
// outstayed the re-seat patience): the member leaves the dispatcher,
// the staffing row offboards, the sidebar row folds away.
func (d *Dispatcher) seatGone(name string, seat *chat.Client) {
	d.mu.Lock()
	sessionID := ""
	if m, ok := d.members[name]; ok && m.seat == seat {
		delete(d.members, name)
		sessionID = m.sessionID
	}
	d.mu.Unlock()
	d.hub.SetQueued(name) // a departed member's lane chips fold with the row
	if d.staff != nil {
		if _, err := d.staff.Offboard(d.projectKey, name); err != nil && !strings.Contains(err.Error(), "已离编") {
			log.Printf("[调度] %s 座位消失后编制离编失败：%v", name, err)
		}
	}
	// The seat's death IS the offboard event (kick and the offboard
	// pipeline both land here), so the sidebar row folds away with it
	// — a departed member must not linger as "running" in the list.
	d.archiveTaskIndex(name, sessionID)
}

// ensureOrchestratorPost self-registers the 排期编排 post into this
// scope's establishment table — the orchestrator birth's
// self-registration: the post joins the keeper's watch the moment a
// person takes the seat. The lobby lands in ops/establishment
// (kb.EstablishmentDocKey — the table its keeper actually watches); a
// project's is p/<key>/establishment. Every outcome the host can act
// on leaves a room trace: created says where the row landed, a
// refusal says why nothing was written; the already-present no-op
// stays silent (recall/rebirth must not spam the room).
func (d *Dispatcher) ensureOrchestratorPost(name string) {
	docKey := kb.EstablishmentDocKey(d.projectKey)
	created, err := kb.EnsureEstablishmentPost(d.cfg.Docs, docKey,
		"orchestrator", "编排者", agents.OrchestratorRole, 1, true, "", true, "调度")
	switch {
	case err != nil:
		d.hub.System(i18n.Sf("[调度] 编排者 %s 入职：编制表登记未成（%v）——座位照常上岗，该岗位暂不受看护", name, err))
	case created:
		d.hub.System(i18n.Sf("[调度] 编排者 %s 入职：已在 %s 登记岗位行（排期编排 ×1，自动补员=是），缺岗由看护照此核对", name, docKey))
	}
}

// Birth creates a member from scratch (the recruit path, v1.0): a
// fresh ZCode session in the workspace carries the onboarding prompt
// as its first user input, the config is stored and bound, the seat
// joins. firstMessage is the full onboarding prompt (empty = the
// stored config's HandoffPrompt). model is the birth-time model pick
// ("providerId/modelId" or a bare id; empty = the fabric ⊕ process
// default chain decides) — on a staffing dispatcher it is stamped into
// the row as the seat's pick (the single truth later re-picks edit);
// the v1 shape applies it to the session without persisting. v2 P3-b:
// on a project dispatcher (StaffStore wired) this is staffing-driven —
// see birthStaffed.
func (d *Dispatcher) Birth(name, role, firstMessage, model, reasoning string) (string, error) {
	return d.BirthPost(name, "", role, firstMessage, model, reasoning)
}

// BirthPost is Birth with the establishment post anchor（--post，分项目
// 根治的出生面）: the anchor lands on the staffing row (SetPost) — the
// keeper's reconciliation key from the moment the seat exists — and
// when the caller passes NO role, the 身份 resolves from THIS project's
// table row (kb.EstablishmentRole)：HR 停止手抄身份串，锚到的行即真
// 相。An unknown post key degrades honestly: the role stays whatever
// the caller gave (possibly empty), the anchor is still stamped — a
// wrong table is the keeper's alarm, never birth's guess.
func (d *Dispatcher) BirthPost(name, postKey, role, firstMessage, model, reasoning string) (string, error) {
	postKey = strings.TrimSpace(postKey)
	if postKey != "" && d.staff != nil && d.cfg.Docs != nil {
		if tableRole, ok := kb.EstablishmentRole(d.cfg.Docs, kb.EstablishmentDocKey(d.projectKey), postKey); ok && strings.TrimSpace(role) == "" {
			role = tableRole // 身份按表反查——表行即单一事实源
		}
	}
	if d.bridge == nil {
		return "", errors.New(i18n.S("调度器未启用（zcode 不可用）"))
	}
	if d.store == nil {
		return "", errors.New(i18n.S("agents store 不可用"))
	}
	cfg, existed := d.store.Get(name)
	rehire := existed && cfg.Archived // r_28 复职豁免：归档复职者走欢迎回来短模板
	if !existed {
		d.store.Upsert(name, role, "", "")
		cfg, _ = d.store.Get(name)
	} else if cfg.Archived {
		// 复职: a same-name birth reinstates the archived profile so the
		// roster views stop hiding the returnee (Upsert rebuilds the
		// entry, keeping role/manual/prompt/binding/packs) — the same
		// contract the agent_save recruit path always had.
		d.store.Upsert(cfg.Name, cfg.Role, cfg.Manual, cfg.Prompt)
		cfg.Archived = false
	}
	// v2 P4-b → v2.2: a birth under a role-marked post stamps the
	// person's governance rank (编排者/HR both Lv.8 — agents.
	// RankForRole is the single lookup) — the rank is an attribute of
	// the post, so the stamp is idempotent and fires on recall/rebirth
	// too.
	if rank, ok := agents.RankForRole(cfg.Role); ok && d.cfg.SetRank != nil {
		d.cfg.SetRank(name, rank)
	}
	if cfg.Role == agents.OrchestratorRole {
		if firstMessage == "" {
			firstMessage = agents.OrchestratorBirthPrompt(name, d.projectKey)
		}
		// v2: the orchestrator's post joins the scope's establishment
		// table at birth — the keeper then watches the seat (缺岗报警,
		// 自动补员 recall). Staffed dispatchers only: the v1 single-room
		// shape has no table to govern.
		if d.staff != nil && d.cfg.Docs != nil {
			d.ensureOrchestratorPost(name)
		}
	}
	// r_28 t_221：导师指派（代码化判定——autopilot 补员场景 HR 也是
	// AI，手册指引不可执行，判定必须代码化才能被自动复用）。一次性
	// 无状态：结果只进注入文本，不建数据结构不持久化（「答一次疑」的
	// 轻定义不值得存储）。复职豁免在 welcomeBack 分支——老熟人跳过。
	if !rehire {
		if mentor, hrFallback := d.pickMentor(name, cfg.Role); mentor != "" || hrFallback {
			firstMessage += "\n\n" + d.mentorLine(name, mentor, cfg.Role)
		}
	}
	// The birth fabric: compose the profile + assembly into the first
	// injection. Overflow degrades to the v1 base — a birth must not
	// brick on an assembly — and the room hears why.
	fabric, _, _, ferr := d.composeFor(name, cfg)
	if ferr != nil {
		d.hub.System(i18n.Sf("[调度] %s 的技能装配超限（%v）——按无装配基座出生", name, ferr))
		fabric = nil
	}
	if d.staff != nil {
		return d.birthStaffed(name, postKey, role, firstMessage, model, reasoning, cfg, fabric, rehire)
	}
	if firstMessage == "" {
		// r_28 t_221：复职豁免——归档复职者走欢迎回来短模板（四步引导
		// 跳过）；fabric 优先级照旧（装配是能力面，与引导模板正交）
		if rehire {
			firstMessage = agents.WelcomeBackPrompt(cfg)
		} else {
			firstMessage = agents.HandoffPrompt(cfg)
		}
		if fabric != nil {
			firstMessage = fabric.Text
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sessionID, err := d.createBirthSession(ctx, d.cfg.Workspace, fabric)
	if err != nil {
		return "", errors.New(i18n.Sf("为 %s 创建会话失败：%v", name, err))
	}
	appliedModel, appliedReasoning := d.selectBirthModel(ctx, sessionID, name, fabric, model, reasoning)
	if err := d.finishBirth(name, cfg.Role, sessionID, firstMessage, cfg, fabric,
		appliedModel, appliedReasoning, false); err != nil {
		return sessionID, err
	}
	return sessionID, nil
}

// finishBirth runs the create-branch tail both Birth flavors share —
// the sequence the v1 path and birthStaffed used to carry as two
// byte-for-byte copies (「shares it byte-for-byte」, the twins debt):
// the displaced-binding fold (a stale binding's pinned sidebar row must
// not linger as a second live pin — the 「两个小马」disease), the
// binding write (staffing row when the studio runs staffed, else the
// agents profile), sidebar registration, attach, the applied-tier note
// (the member exists only past attach), the staffing active promotion,
// and the first injection. markActive is the staffing path's extra
// leg; the v1 path passes false.
func (d *Dispatcher) finishBirth(name, seatRole, sessionID, firstMessage string,
	cfg agents.Config, fabric *capability.EffectiveFabric,
	appliedModel, appliedReasoning string, markActive bool) error {
	if cfg.SessionID != "" && cfg.SessionID != sessionID {
		d.archiveTaskIndex(name, cfg.SessionID)
	}
	if d.staff != nil {
		if _, err := d.staff.BindSession(d.projectKey, name, sessionID); err != nil {
			log.Printf("[调度] %s 会话绑定回写编制失败：%v", name, err)
		}
	} else {
		d.store.BindSession(name, sessionID)
	}
	d.registerTaskIndexAsync(name, seatRole, sessionID, fabric)
	if err := d.attach(name, seatRole, sessionID); err != nil {
		return err
	}
	d.noteApplied(name, appliedModel, appliedReasoning)
	if markActive {
		if _, err := d.staff.MarkActive(d.projectKey, name); err != nil {
			log.Printf("[调度] %s 编制转 active 失败：%v", name, err)
		}
	}
	d.deliver(name, d.noticePrefix()+firstMessage, nil, nil)
	return nil
}

// createBirthSession opens a member's session in ws, carrying the
// assembly's MCP servers when there are any: a non-empty spec list
// rides session/create's mcpServers override (which REPLACES the
// member's default MCP fleet — the manual says so in prose); an empty
// list creates plain, keeping the default fleet. Bridges without the
// MCP extension (test fakes) degrade to a plain create — same
// discipline as the assistant probe.
func (d *Dispatcher) createBirthSession(ctx context.Context, ws string, fabric *capability.EffectiveFabric) (string, error) {
	specs := mcpSpecsOf(fabric)
	if len(specs) == 0 {
		return d.bridge.CreateSession(ctx, ws, "yolo")
	}
	if mb, ok := d.bridge.(mcpOverrideBridge); ok {
		return mb.CreateSessionMCP(ctx, ws, "yolo", specs)
	}
	return d.bridge.CreateSession(ctx, ws, "yolo")
}

// mcpSpecsOf converts the fabric's assembled servers into the zcode
// wire shape: the asset key becomes the server name (unique per
// session by construction — keys are unique in the library),
// isolation "session" scopes the fleet to this session.
func mcpSpecsOf(fabric *capability.EffectiveFabric) []zcode.MCPServerSpec {
	if fabric == nil || len(fabric.MCP) == 0 {
		return nil
	}
	out := make([]zcode.MCPServerSpec, 0, len(fabric.MCP))
	for _, m := range fabric.MCP {
		spec := zcode.MCPServerSpec{
			Name: m.Key, Type: m.Type, URL: m.URL,
			Isolation: "session",
		}
		for _, h := range m.Headers {
			spec.Headers = append(spec.Headers, zcode.MCPHeader{Name: h.Name, Value: h.Value})
		}
		out = append(out, spec)
	}
	return out
}

// birthStaffed is the staffing-driven Birth (v2 P3-b): the project's
// staffing row is the registry and the binding's single truth. A row
// with a session takes the adopt path (resume + subscribe, a light
// recall notice wakes the member — never the full onboarding prompt
// into a session that already carries it); a row without one creates
// the session in the project's workspace (Config.Workspace, set by
// the Fleet from the project store — the sole source) and writes the
// binding back to the row, never to agents.Config.
func (d *Dispatcher) birthStaffed(name, postKey, role, firstMessage, model, reasoning string, cfg agents.Config, fabric *capability.EffectiveFabric, rehire bool) (string, error) {
	if _, err := d.staff.Join(d.projectKey, name, role, ""); err != nil {
		// A row already occupying THIS project is fine — recall/retry
		// after a failed adoption replays Birth; only a block elsewhere
		// (or a genuinely missing row) refuses.
		row, ok := d.staff.Get(d.projectKey, name)
		if !ok || !row.Occupying() {
			return "", err
		}
	}
	// 岗位锚随出生落行（--post）：复职路径（row 已带锚）不受影响——
	// Join 只在锚空时保留旧值，显式 SetPost 覆盖为本次的锚。
	if postKey != "" {
		if _, err := d.staff.SetPost(d.projectKey, name, postKey); err != nil {
			log.Printf("[调度] %s 岗位锚落行失败：%v", name, err)
		}
	}
	// The birth-time model tier is the seat's pick from the moment the
	// row exists (Join carries a prior pick through reinstatement; an
	// explicit birth pick — model, reasoning-only, or both — replaces
	// it, the tier is one atomic triple — a fully empty one keeps it:
	// the recall/rebirth path must not wipe a configured seat).
	if strings.TrimSpace(model) != "" || strings.TrimSpace(reasoning) != "" {
		if _, err := d.staff.SetModel(d.projectKey, name, model, reasoning); err != nil {
			log.Printf("[调度] %s 出生模型档落行失败：%v", name, err)
		}
	}
	row, _ := d.staff.Get(d.projectKey, name)
	seatRole := row.ProjectRole
	if seatRole == "" {
		seatRole = cfg.Role
	}

	if row.SessionID != "" {
		if err := d.attach(name, seatRole, row.SessionID); err != nil {
			return row.SessionID, err
		}
		if _, err := d.staff.MarkActive(d.projectKey, name); err != nil {
			log.Printf("[调度] %s 编制转 active 失败：%v", name, err)
		}
		// Recall resurrection: the offboard fold lifts with the seat,
		// so the returning member reclaims the pinned shelf.
		d.registerTaskIndexAsync(name, seatRole, row.SessionID, nil)
		if firstMessage == "" {
			firstMessage = fmt.Sprintf("【拉回】%s，你已被拉回项目「%s」的办公室继续工作；点名叫你的消息会照常注入。", name, d.projectKey)
		}
		d.deliver(name, d.noticePrefix()+firstMessage, nil, nil)
		return row.SessionID, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// v2.7.1 分支隔离：座位带 Branch 的成员生在自己的专属工作树（建
	// 树失败 birth 拒绝——绝不静默落回共享主树）。
	ws, werr := d.birthWorkspace(name)
	if werr != nil {
		return "", werr
	}
	sessionID, err := d.createBirthSession(ctx, ws, fabric)
	if err != nil {
		return "", errors.New(i18n.Sf("为 %s 创建会话失败：%v", name, err))
	}
	appliedModel, appliedReasoning := d.selectBirthModel(ctx, sessionID, name, fabric, model, reasoning)
	// Same displaced-binding fold as the v1 path: the create branch
	// parks a NEW session under the member — a stale binding anywhere
	if firstMessage == "" {
		// r_28 t_221：复职豁免——归档复职者走欢迎回来短模板（四步引导
		// 跳过）；fabric 优先级照旧（装配是能力面，与引导模板正交）
		if rehire {
			firstMessage = agents.WelcomeBackPrompt(cfg)
		} else {
			firstMessage = agents.HandoffPrompt(cfg)
		}
		if fabric != nil {
			firstMessage = fabric.Text
		}
	}
	if row.Branch != "" && ws != d.cfg.Workspace {
		firstMessage = fmt.Sprintf(
			"【工作树】你的专属工作树：%s（分支 %s）——文件改动只影响你自己，与其他成员互不踩脚；提交请带 t_NN/r_NN 号。\n\n", ws, row.Branch) + firstMessage
	}
	// 共用尾巴（finishBirth）： displaced-binding 折叠、绑定回写、侧栏、
	// attach、选档注记、转 active、首发注入——v1 路同款。此前返回的是
	// 建会话前的旧 row.SessionID（此分支必为空串）——真 bug，改回真身。
	if err := d.finishBirth(name, seatRole, sessionID, firstMessage, cfg, fabric,
		appliedModel, appliedReasoning, true); err != nil {
		return sessionID, err
	}
	return sessionID, nil
}

// selectBirthModel applies the spike-G birth formula: the explicit
// birth pick (the 招牛马表单's choice, model + thinking intensity),
// else the assembly's model tier, else the process default, else one
// notice that the personal provider is missing. A reasoning-only pick
// (empty model — the ZCode picker's default-model shape) re-voices
// the intensity over the chain's model: the assembly's tier when one
// stands, else the process default. Returns the tier it applied for
// the caller to note AFTER attach — the member entry doesn't exist
// yet at this point, so an in-here noteApplied would no-op ("" model
// = nothing applied; the fabric/default cases stay unnoted, the
// staffing pick is the display truth for them). Extracted so the
// staffing-driven create path shares it byte-for-byte.
func (d *Dispatcher) selectBirthModel(ctx context.Context, sessionID, name string, fabric *capability.EffectiveFabric, pick, pickReasoning string) (string, string) {
	switch {
	case pick != "":
		if d.applyModel(ctx, sessionID, name, pick, pickReasoning) {
			return pick, pickReasoning
		}
	case pickReasoning != "" && fabric != nil && fabric.Model != nil:
		if d.applyModel(ctx, sessionID, name, fabric.Model.ID, pickReasoning) {
			return fabric.Model.ID, pickReasoning
		}
	case pickReasoning != "" && d.providerID != "" && d.cfg.Model != "":
		sel := zcode.ModelSelection{ProviderID: d.providerID, ModelID: d.cfg.Model,
			Options: &zcode.ModelOptions{ReasoningLevel: pickReasoning}}
		if err := d.bridge.SetModel(ctx, sessionID, sel); err != nil {
			log.Printf("[调度] %s setModel(%s) 失败：%v（沿用默认模型）", name, d.cfg.Model, err)
		} else {
			return d.cfg.Model, pickReasoning
		}
	case fabric != nil && fabric.Model != nil:
		if d.applyModel(ctx, sessionID, name, fabric.Model.ID, fabric.Model.Reasoning) {
			d.noteApplied(name, fabric.Model.ID, fabric.Model.Reasoning)
		}
	case d.providerID != "" && d.cfg.Model != "":
		sel := zcode.ModelSelection{ProviderID: d.providerID, ModelID: d.cfg.Model}
		if d.cfg.Reasoning != "" {
			sel.Options = &zcode.ModelOptions{ReasoningLevel: d.cfg.Reasoning}
		}
		if err := d.bridge.SetModel(ctx, sessionID, sel); err != nil {
			log.Printf("[调度] %s setModel(%s) 失败：%v（沿用默认模型）", name, d.cfg.Model, err)
		}
	case !d.providerNoted && d.cfg.Model != "":
		d.providerNoted = true
		d.hub.System(i18n.Sf("[调度] 未在 ~/.zcode/v2/provider_config.json 找到可用个人 provider，成员会话将沿用 CLI 默认模型（想要 %s 需先配置）", d.cfg.Model))
	}
	return "", ""
}

// Bind attaches an existing ZCode session under a stored member (the
// adoption path for sessions born before v1.0). sessionID empty
// unbinds. v2 P3-b: a project dispatcher writes the binding to its
// staffing row (the single truth), never to agents.Config.
func (d *Dispatcher) Bind(name, sessionID string) error {
	cfg, ok := d.store.Get(name)
	if !ok {
		return errors.New(i18n.Sf("%s 未建档", name))
	}
	bind := func(id string) {
		if d.staff != nil {
			if _, err := d.staff.BindSession(d.projectKey, name, id); err != nil {
				log.Printf("[调度] %s 会话绑定回写编制失败：%v", name, err)
			}
			return
		}
		d.store.BindSession(name, id)
	}
	if sessionID == "" {
		// The unbind retires the member: whatever session the binding
		// held leaves its pinned sidebar row behind unless folded here
		// — watchSeat never fires for a member removed this way.
		if d.staff != nil {
			if row, ok := d.staff.Get(d.projectKey, name); ok && row.SessionID != "" {
				d.archiveTaskIndex(name, row.SessionID)
			}
		} else if cfg.SessionID != "" {
			d.archiveTaskIndex(name, cfg.SessionID)
		}
		d.mu.Lock()
		if m, live := d.members[name]; live {
			delete(d.members, name)
			_ = m
		}
		d.mu.Unlock()
		d.hub.SetQueued(name) // the retired member's lane chips fold with the row
		bind("")
		return nil
	}
	if err := d.attach(name, cfg.Role, sessionID); err != nil {
		return err
	}
	bind(sessionID)
	return nil
}

// Recall births (or re-attaches) a stored member — the watch keeper's
// 自动补员 hook: dispatcher-managed rebirth replaces the GUI drive
// path when the dispatcher is live. Reports whether it handled it.
func (d *Dispatcher) Recall(name string) bool {
	if d.bridge == nil || d.store == nil {
		return false
	}
	cfg, ok := d.store.Get(name)
	if !ok {
		return false
	}
	// First message left empty: Birth composes the seat's own fabric
	// (the v1 base when no assembly applies) — one source for the
	// birth injection, recall included. No model pick: a reborn member
	// keeps the row's stamped pick (an empty birth pick never wipes it).
	if _, err := d.Birth(name, cfg.Role, "", "", ""); err != nil {
		log.Printf("[调度] 召回 %s 失败：%v", name, err)
		return false
	}
	return true
}
