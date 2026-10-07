package dispatch

// The boot flagship recruiter (v2.2): the app's front door waits on
// the establishment — the lobby's flagship posts (编排者 小牛, HR 小马,
// both Lv.8) must be SEATED before the office opens. The loop starts
// the moment a bridge attaches (boot or recovery) and keeps one
// reconcile-birth pass ticking until the table is filled: it first
// ensures the post rows exist (creating/repairing the lobby table
// under the frozen contract), then births the first missing flagship
// person through the lobby dispatcher's real Birth (a true persistent
// ZCode session — never a headless pull), one hire per beat so the
// splash's progress stays legible. A refusal never opens the door:
// Blocked carries the reason (a broken table, a refused birth) and the
// loop keeps retrying — the host sees why on the splash and can fix it
// over the CLI. Ranks refresh on the way: every filled flagship post
// re-stamps its holders' Lv.8 each boot, so an incumbent promoted by
// hand yesterday cannot sit at a stale level today.

import (
	"fmt"
	"log"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/util"
)

// RecruitProgress is the splash's hiring view over the lobby's
// flagship establishment. Done=true means the door's establishment
// half is satisfied (or was never gated — no docs face, no dispatcher).
type RecruitProgress struct {
	Filled  int    `json:"filled"`
	Total   int    `json:"total"`
	Current string `json:"current,omitempty"` // the post being hired, "小马（HR）"
	Blocked string `json:"blocked,omitempty"` // the last refusal; empty when healthy
	Done    bool   `json:"done"`
}

// recruitTick is the loop beat: one hire per tick staggers session
// creates and keeps the splash's count moving; retries ride the same
// beat (a dead bridge is re-spawned by the watchdog on its own 10s
// ladder — the next tick picks the replacement up). A var (not a
// const) purely as the tests' throttle seam.
var recruitTick = 5 * time.Second

// KickFlagshipRecruit starts the boot recruitment loop (idempotent;
// main kicks it the moment a bridge attaches — inside Attach it would
// race the first Birth past any test seam). No-op without room
// memory: a fleet without the docs face cannot serve the
// establishment gate and stays at its satisfied default — the ZCode
// gate alone governs that shape.
func (f *Fleet) KickFlagshipRecruit() { f.kickRecruit(true) }

// KickProjectRecruit re-arms the recruitment loop when a project opens
// after the boot loop settled (EnsureRoom's tail, v2.5 分项目编制): a
// still-running loop sees the new room on its next beat; a settled one
// restarts WITHOUT touching the door's progress (the lobby half
// reports instantly done, the splash never re-waits). Same docs guard
// as the boot kick. Nil-safe like the fleet's other directly-wired
// taps (main's PurgeRecruitKick binds it under --no-dispatch too).
func (f *Fleet) KickProjectRecruit() {
	if f == nil {
		return
	}
	f.kickRecruit(false)
}

// kickRecruit is the two kicks' shared core: recruitRunning is the
// liveness flag the loop clears on exit (the boot kick's old
// one-shot recruitStarted semantics — a settled loop may be re-armed).
func (f *Fleet) kickRecruit(boot bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recruitRunning || f.base.Docs == nil {
		return
	}
	f.recruitRunning = true
	if boot {
		f.recruitProg = RecruitProgress{Done: false}
	}
	f.recruitWG.Add(1) // Stop 关门后还要收拢最后一轮（见 Fleet.Stop）
	go func() {
		defer f.recruitWG.Done()
		f.recruitLoop()
	}()
}

// FlagshipRecruit snapshots the recruitment progress for the boot
// gate's /zcode answer.
func (f *Fleet) FlagshipRecruit() RecruitProgress {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recruitProg
}

// recruitLoop runs one round per beat until the WHOLE establishment is
// filled (or the fleet stops underneath it): the lobby first — the
// door's half, prog.Done is its verdict alone — then the per-project
// system posts (one birth per beat across everything, sessions
// stagger). The loop may long outlive prog.Done=true: the door opens
// on the lobby, the projects keep hiring quietly behind it.
func (f *Fleet) recruitLoop() {
	defer func() {
		f.mu.Lock()
		f.recruitRunning = false
		f.mu.Unlock()
	}()
	for {
		// 每轮带栅栏：招聘循环死了没人重启它——启动门（boot gate）就
		// 再也凑不齐。一轮 panic 降级为「这轮无功而返」，下一拍重试。
		prog := RecruitProgress{}
		util.Guard("dispatch: recruit round", func() { prog = f.recruitRound() })
		f.mu.Lock()
		f.recruitProg = prog
		f.mu.Unlock()
		if prog.Done && f.projectRecruitPass() {
			return
		}
		select {
		case <-f.recruitStop:
			return
		case <-time.After(recruitTick):
		}
	}
}

// recruitRound is one reconcile-birth pass over the lobby's flagship
// establishment. The pass never guesses: a table it cannot parse or a
// birth that is refused parks the reason in Blocked and leaves Done
// false — the door waits, the splash says why.
func (f *Fleet) recruitRound() RecruitProgress {
	d := f.Lobby()
	if d == nil {
		// no lobby dispatcher (a fleet shape without rooms): the gate
		// cannot be served — satisfied, the ZCode state governs alone
		return RecruitProgress{Done: true}
	}

	// The rows first: a fresh room's table comes from the seed, an
	// older room gains the orchestrator row here, a deleted table is
	// rebuilt under the frozen contract. A broken table refuses. The
	// advisor row rides along (必须 system post — its life is the
	// assistant session's own, so this pass only ever ensures the ROW,
	// never a birth; the boot gate doesn't count it).
	docKey := kb.EstablishmentDocKey(chat.LobbyKey)
	blocked := ""
	ensurePosts := append(append([]kb.FlagshipPost(nil), kb.FlagshipPosts...), kb.AdvisorPost)
	for _, p := range ensurePosts {
		if _, err := kb.EnsureEstablishmentPost(f.base.Docs, docKey,
			p.Key, p.Name, p.Role, p.Headcount, p.AutoFill, p.Manual, true, "调度"); err != nil {
			blocked = err.Error()
			break
		}
	}

	// Presence here is LIVE SEATS ONLY (kb.LiveEstablishmentRows): a
	// config without a seat is a hire that hasn't happened yet — the
	// recruiter's own pre-birth upsert must never satisfy its own gate
	// (the report endpoint's config-inclusive view is the keeper's
	// semantics, not the boot contract's).
	rows, perr := kb.LiveEstablishmentRows(d.hub, f.base.Docs, docKey)
	if perr != "" {
		blocked = perr
	}
	filled, total := kb.FlagshipFilled(rows)
	prog := RecruitProgress{Filled: filled, Total: total}
	if blocked != "" {
		prog.Blocked = blocked
		return prog
	}

	missing := kb.FlagshipShortage(rows)
	if len(missing) == 0 {
		f.stampFlagshipRanks(d, rows)
		prog.Done = true
		return prog
	}

	// One hire per pass, in table order — the exact shape of a manual
	// POST /dispatch/birth: the config upsert carries the post's manual
	// (HR's handoff points at roles/hr) and Birth does the rest — real
	// persistent session, seat, ZCode sidebar pin, orchestrator prompt,
	// rank stamp, post registration.
	post := missing[0]
	prog.Current = i18n.Sf("%s（%s）", post.Person, post.Name)
	if f.agents != nil {
		f.agents.Upsert(post.Person, post.Role, post.Manual, "")
	}
	// 旗舰岗带锚出生（--post）：表行 key 即锚，看护按锚对账——
	// 身份串从出生起就只是显示文本。
	if _, err := d.BirthPost(post.Person, post.Key, post.Role, "", "", ""); err != nil {
		prog.Blocked = i18n.Sf("招聘 %s（%s）未成：%v", post.Person, post.Name, err)
		return prog
	}
	log.Printf("[招聘] 旗舰岗位到岗：%s（%s，Lv.%d）", post.Person, post.Name, post.Rank)
	d.hub.System(i18n.Sf("[招聘] %s 已到岗（%s，Lv.%d）——启动编制 %d/%d",
		post.Person, post.Name, post.Rank, filled+1, total))
	// 与 niuma recruit 同款的 org-memory 台账行（步骤⑥）：入职落
	// ops/room-log（AppendRollover——v3 分卷口径），失败降级为一行
	// 日志，绝不影响出生结果。
	if _, err := f.base.Docs.AppendRollover("ops/room-log",
		fmt.Sprintf("%s 已上岗（岗位 %s，启动招聘，%s）", post.Person, post.Role, time.Now().Format("2006-01-02")),
		"调度", 0); err != nil {
		log.Printf("[招聘] %s 的上岗台账未落：%v", post.Person, err)
	}
	prog.Filled = filled + 1
	return prog
}

// stampFlagshipRanks re-stamps every seated flagship holder's
// governance rank — the boot's promote pass: the posts are Lv.8, so
// whoever holds one today carries it (idempotent; a hand-set lower
// level cannot survive the boot).
func (f *Fleet) stampFlagshipRanks(d *Dispatcher, rows []kb.EstablishmentRow) {
	if d.cfg.SetRank == nil {
		return
	}
	holders := map[string][]string{}
	for _, r := range rows {
		holders[r.Role] = append(holders[r.Role], r.Present...)
	}
	for _, p := range kb.FlagshipPosts {
		for _, name := range holders[p.Role] {
			d.cfg.SetRank(name, p.Rank)
		}
	}
}

// --- the per-project system-post recruiter (v2.5 分项目编制) ---------------

// projectRecruitPass runs one hiring beat over every ACTIVE project's
// room: 编排者/HR 每个项目各一套，开张/启动即自动招募——大厅先满编
// （门等它），项目随后，每拍至多一次出生（会话错峰，splash 无关）。
// Returns true when every project's system posts are seated — the
// loop's exit half. A refused birth or an unreadable table keeps the
// pass false and retries next beat (the lobby Blocked shape); the
// failures log, they never spam the rooms.
func (f *Fleet) projectRecruitPass() bool {
	if f.projs == nil {
		return true
	}
	for _, p := range f.projs.List() {
		if p.Status != projects.StatusActive || p.Key == chat.LobbyKey {
			continue
		}
		d := f.Get(p.Key)
		if d == nil {
			continue // 无房无调度器：不是本循环的事（未拨号的中途形态）
		}
		if !f.projectRecruitRound(d) {
			return false
		}
	}
	return true
}

// projectRecruitRound reconciles ONE project's system posts: the rows
// first (the activation leg already seeds; this is the recruiter's own
// belt-and-braces — the lobby ensurePosts shape), presence by LIVE
// SEATS ONLY (kb.LiveEstablishmentRows), then one birth per call for
// the first missing post — the project's OWN candidate: its offboarded
// former holder first (kick/归档 后复职，身份连续；已在他项目上班的
// 跳过——一期一人一时一房), else a fresh deterministic name from the
// pool. false = still hiring or blocked; true = seated.
func (f *Fleet) projectRecruitRound(d *Dispatcher) bool {
	if _, err := kb.EnsureProjectEstablishment(f.base.Docs, d.projectKey, "招聘"); err != nil {
		log.Printf("[招聘] 项目 %s 编制表登记未成：%v（下拍再试）", d.projectKey, err)
		return false
	}
	rows, perr := kb.LiveEstablishmentRows(d.hub, f.base.Docs, kb.EstablishmentDocKey(d.projectKey))
	if perr != "" {
		log.Printf("[招聘] 项目 %s 编制表不可读：%v（下拍再试）", d.projectKey, perr)
		return false
	}
	missing := kb.FlagshipShortage(rows)
	if len(missing) == 0 {
		return true
	}
	post := missing[0]
	name := f.projectFlagshipCandidate(d.projectKey, post)
	if name == "" {
		return false // 无名可取（不应发生）：下拍再试
	}
	if f.agents != nil {
		f.agents.Upsert(name, post.Role, post.Manual, "") // 岗位的手册锚（HR → roles/hr）
	}
	// 同旗舰岗带锚出生（--post）：项目系统岗从出生起就锚进本项目表。
	if _, err := d.BirthPost(name, post.Key, post.Role, "", "", ""); err != nil {
		log.Printf("[招聘] 项目 %s 招募 %s（%s）未成：%v（下拍再试）", d.projectKey, name, post.Name, err)
		return false
	}
	log.Printf("[招聘] 项目系统岗到岗：%s（%s，Lv.%d，%s）", name, post.Name, post.Rank, d.projectKey)
	d.hub.System(i18n.Sf("[招聘] %s 已到岗（%s，Lv.%d）——项目系统岗自动招募", name, post.Name, post.Rank))
	return false // 一拍一雇：下一拍重对账
}

// projectFlagshipCandidate picks the person to seat a project's system
// post: the project's own OFFBOARDED holder first (同一人回来——归档/
// 被踢后的复职，身份与档案连续), else a fresh name (freshProjectPerson).
// An occupying-elsewhere former holder is skipped (一期一人一时一房：
// 他在别的项目上班，这里另招新人，不在他背后反复撞墙).
func (f *Fleet) projectFlagshipCandidate(projectKey string, post kb.FlagshipPost) string {
	if f.staff != nil {
		for _, row := range f.staff.ListByProject(projectKey) {
			if row.State != staffing.StateOffboard {
				continue
			}
			role := row.ProjectRole
			if role == "" && f.agents != nil {
				if cfg, ok := f.agents.Get(row.Person); ok {
					role = cfg.Role
				}
			}
			if role != post.Role {
				continue
			}
			if _, occupying := f.staff.OccupancyOf(row.Person); occupying {
				continue
			}
			return row.Person
		}
	}
	return f.freshProjectPerson(projectKey, post)
}

// projectPersonPool names the per-project flagship hires: 大厅固定小牛/
// 小马，各项目从池里各取其名——编排者/HR 每项目各一套、互不重名
// （agents.Store 全局按名唯一，staffing 一人一时一房）。池序固定，挑
// 选按 (项目, 岗位) 确定性散列起步、线性避让已用名。
var projectPersonPool = []string{
	"小鹿", "小羊", "小猫", "小兔", "小狐", "小熊", "小鸟", "小鲤",
	"小杉", "小杏", "小云", "小溪", "小橘", "小栗", "小桂", "小禾",
	"小苗", "小竹", "小梅", "小鹤",
}

// freshProjectPerson rolls a project's own name for a system post: a
// deterministic pool pick that avoids every name already in the store;
// pool exhausted → the lobby person names the project's holder
// （小牛·<key> 形，键段截到 24 字名帽内）.
func (f *Fleet) freshProjectPerson(projectKey string, post kb.FlagshipPost) string {
	start := int(recruitHash(projectKey+"\x00"+post.Key) % uint32(len(projectPersonPool)))
	for i := 0; i < len(projectPersonPool); i++ {
		name := projectPersonPool[(start+i)%len(projectPersonPool)]
		if f.agents == nil {
			return name
		}
		if _, taken := f.agents.Get(name); !taken {
			return name
		}
	}
	name := post.Person + "·" + projectKey
	if r := []rune(name); len(r) > 24 {
		name = string(r[:24])
	}
	return name
}

// recruitHash is the stable string hash the pool pick drinks from
// (fnv-1a, the staffing.RollLook house shape — same semantics, local
// copy to keep the package boundary).
func recruitHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
