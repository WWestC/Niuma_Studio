package roomops

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/util"
)

// WatchInterval (the default beat) and WatchCooldown keep the v0.10
// PRD §3.4 ruling values; NIUMA_WATCH_EVERY retunes the interval.
const (
	WatchInterval = 5 * time.Minute
	WatchCooldown = 30 * time.Minute
)

// WatchEveryFromEnv resolves the keeper beat from NIUMA_WATCH_EVERY: a
// duration ("90s", "45m", "1h") or a bare positive integer (minutes)
// overrides the default; anything malformed keeps the default — a bad
// env var must never brick the keeper (or the room). This is the
// patrol clock's NIUMA_PATROL_EVERY pattern, applied to the keeper.
func WatchEveryFromEnv() time.Duration {
	v := strings.TrimSpace(util.Env("WATCH_EVERY"))
	if v == "" {
		return WatchInterval
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return WatchInterval
}

// KeeperConfig wires the keeper set. LobbyHub is the alarm surface and
// the lobby's own roster; Registry supplies the project rooms (a
// project nobody dialed holds no seats and is reconciled against an
// empty roster — its table stays authoritative); Recall is the
// dispatcher's refill hook (project, name) — the same path the manual
// recall rides. Tick, when non-nil, REPLACES the wall-clock timer (the
// fake-clock injection point; every value received fires one round).
type KeeperConfig struct {
	LobbyHub *chat.Hub
	Registry *chat.Registry
	Projects *projects.Store
	Docs     *kb.DocsStore
	Agents   *agents.Store
	Staff    *staffing.Store
	Recall   func(project, name string) bool

	// Every: 0 = WatchInterval, negative = never start (tests, opt-out).
	Every time.Duration
	// Tick: the injectable fake clock (tests drive rounds
	// deterministically).
	Tick <-chan time.Time
}

// Keepers is the per-project reconciliation loop. Zero seats, zero
// says in the watched rooms: alarms go to the lobby only, so no roster
// ever shows a "keeper".
type Keepers struct {
	cfg   KeeperConfig
	stop  chan struct{}
	debug chan struct{} // closed when one injected tick's round finished

	firstSeen map[string]time.Time // project\x00post → first shortfall sighting
	lastAct   map[string]time.Time // project\x00post → last alarm/refill
	parseLast map[string]time.Time // project → parse-failure alarm cooldown
}

// StartKeepers launches the keeper loop (nil docs = watch disabled,
// embedders' choice). Call after the lobby hub is up.
func StartKeepers(cfg KeeperConfig) *Keepers {
	if cfg.Docs == nil || cfg.LobbyHub == nil {
		return nil
	}
	every := cfg.Every
	if every == 0 {
		every = WatchInterval
	}
	if every < 0 {
		return nil
	}
	k := &Keepers{
		cfg:       cfg,
		stop:      make(chan struct{}),
		debug:     make(chan struct{}, 1),
		firstSeen: map[string]time.Time{},
		lastAct:   map[string]time.Time{},
		parseLast: map[string]time.Time{},
	}
	go k.loop(every)
	return k
}

// Stop ends the keeper loop (tests, graceful shutdown).
func (k *Keepers) Stop() { close(k.stop) }

// loop waits one beat between reconciliation rounds. cfg.Tick (the
// fake-clock injection point) replaces the wall-clock timer wholesale
// when set; each fired round is synchronous, so a test that sent a
// tick can await on done() before asserting.
func (k *Keepers) loop(every time.Duration) {
	var timer *time.Timer
	if k.cfg.Tick == nil {
		timer = time.NewTimer(every)
		defer timer.Stop()
	}
	for {
		var tick <-chan time.Time
		if timer != nil {
			tick = timer.C
		} else {
			tick = k.cfg.Tick
		}
		select {
		case <-k.stop:
			return
		case <-tick:
			if timer != nil {
				timer.Reset(every)
			}
			// The fence keeps the watch alive: a dead keeper loop is a
			// studio that stops alarming about shortfall — the worst
			// silent failure this package could have.
			util.Guard("server: keeper", func() { k.round(time.Now()) })
			select {
			case k.debug <- struct{}{}:
			default:
			}
		}
	}
}

// done reports (bounded wait) that one injected round has finished —
// the fake-clock tests' synchronization point.
func (k *Keepers) done() bool {
	select {
	case <-k.debug:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

// round is one reconciliation pass over the lobby plus every ACTIVE
// project with an instantiated room.
func (k *Keepers) round(now time.Time) {
	k.reconcile(chat.LobbyKey, k.cfg.LobbyHub, now)
	if k.cfg.Projects == nil || k.cfg.Registry == nil {
		return
	}
	rooms := k.cfg.Registry.Rooms()
	for _, p := range k.cfg.Projects.List() {
		if p.Status != projects.StatusActive || p.Key == chat.LobbyKey {
			continue
		}
		if hub, ok := rooms[p.Key]; ok {
			k.reconcile(p.Key, hub, now)
		}
	}
}

// establishDocKey defers to kb's one rule: the lobby keeps the v1
// ops/establishment; a project's table is p/<key>/establishment (the
// keeper, the orchestrator's self-registration and the boot recruiter
// all drink from the same address).
func establishDocKey(projectKey string) string {
	return kb.EstablishmentDocKey(projectKey)
}

// reconcile runs one project's table against its room roster.
func (k *Keepers) reconcile(projectKey string, hub *chat.Hub, now time.Time) {
	// 房间暂停：整房跳过——缺岗报警与自动补员都是「会自己动」的事，
	// 暂停期一概不动（首见计时也不走，恢复后从头看）。
	if k.cfg.Staff != nil && k.cfg.Staff.SettingsOf(projectKey).Paused {
		return
	}
	docKey := establishDocKey(projectKey)
	doc, err := k.cfg.Docs.Get(docKey, 0)
	if err != nil {
		// The lobby's missing table is the v1 alarm (a room that once
		// had one lost it); a project's missing table is the seeding
		// leg's failure shape (v2.5 起开张即播种——缺表只在播种失手或
		// 房主硬删后出现), so it stays unwatched here and the boot
		// convergence rebuilds it next start.
		if projectKey == chat.LobbyKey {
			k.alarmThrottled(projectKey, now, i18n.Sf("[看护] 编制表文档不可读：%v（用 kb write ops/establishment 重建，表头需为冻结六列）", err))
		}
		return
	}
	rows, perr := kb.EstablishmentRows(doc.Body)
	if perr != "" {
		k.alarmThrottled(projectKey, now, i18n.S("[看护] ")+tagFor(projectKey, i18n.Sf("编制表解析失败——%s（请核对 %s）", perr, docKey)))
		return
	}

	// present view — TWO anchors, 岗位锚 first (分项目根治): a
	// staffing row's PostKey names its post in THIS project's table,
	// and the keeper counts by it the moment any row of that post
	// carries the anchor — the 身份串 demotes to display text (the
	// 「·主笔」 suffix and an empty project_role stop reading as 缺岗). Rows with no
	// anchored holder keep the legacy exact-role match against the
	// room roster. Offline staffing rows do NOT count here either way
	// (the keeper watches physical presence; the report endpoint
	// carries the roster∪staffing view).
	online := map[string]bool{}
	present := map[string][]string{}     // 身份串 → names（legacy 锚）
	postHolders := map[string][]string{} // 岗位 key → names（岗位锚）
	for _, m := range hub.Members() {
		online[m.Name] = true
		if m.Role != "" {
			present[m.Role] = append(present[m.Role], m.Name)
		}
	}
	anchored := map[string]bool{}
	if k.cfg.Staff != nil {
		for _, row := range k.cfg.Staff.ListByProject(projectKey) {
			if row.PostKey == "" || !row.Occupying() || !online[row.Person] {
				continue
			}
			postHolders[row.PostKey] = append(postHolders[row.PostKey], row.Person)
			anchored[row.PostKey] = true
		}
	}

	grace := k.grace()
	// counted: names the TABLE counts into some row — watched or not
	//（在房未计入的分母：只数受看护行会把安静自建岗的在座者误报成失
	// 配——规划岗里坐着人是常态，不是串岗）。
	counted := map[string]bool{}
	for _, row := range rows {
		if row.Key == kb.AdvisorPost.Key {
			continue
		}
		names := present[row.Role]
		if anchored[row.Key] {
			names = postHolders[row.Key]
		}
		for _, n := range names {
			counted[n] = true
		}
	}
	for _, row := range rows {
		// the advisor row (小助手) is presence-exempt: it names no seat —
		// the assistant session lives on the bridge, not the roster, so
		// watching it would alarm forever about a post nobody can seat.
		if row.Key == kb.AdvisorPost.Key {
			continue
		}
		// the watch gate (v2.4): only 必须 posts (system keys carry the
		// stamp even if a hand edit stripped the column) and 自动补员=是
		// rows are watched. A plain custom row — 先定编后招牛马的规划岗
		// sitting at 编制 1 / 在岗 0 — is SILENT: the host planned
		// headcount, they did not ask for a nag every cooldown.
		if !row.Must && !row.AutoFill && !kb.SystemPostKey(row.Key) {
			continue
		}
		key := projectKey + "\x00" + row.Key
		names := present[row.Role]
		if anchored[row.Key] {
			names = postHolders[row.Key] // 岗位锚优先：身份串只是显示文本
		}
		got := len(names)
		if got >= row.Headcount {
			delete(k.firstSeen, key) // recovered: re-arm next time
			continue
		}
		first, seen := k.firstSeen[key]
		if !seen {
			k.firstSeen[key] = now // start the persistence window
			continue
		}
		if now.Sub(first) < grace {
			continue // still inside the reconnect window: hold
		}
		if now.Sub(k.lastAct[key]) < WatchCooldown {
			continue // cooldown: one alarm per half hour per post
		}
		k.lastAct[key] = now
		if row.AutoFill && k.autoRecall(projectKey) {
			if name := k.offlineCandidate(projectKey, hub, row); name != "" {
				if k.cfg.Recall != nil && k.cfg.Recall(projectKey, name) {
					log.Printf("[看护] %s 缺岗：已自动补员召回 %s", tagFor(projectKey, row.Name), name)
					k.cfg.LobbyHub.SystemRecorded(i18n.Sf("[看护] %s（%v）缺岗 %s，编制 %d / 在岗 %d：已自动补员召回 %s",
						tagFor(projectKey, row.Name), names, util.ShortDur(now.Sub(first)), row.Headcount, got, name))
					continue
				}
				k.cfg.LobbyHub.SystemRecorded(i18n.Sf("[看护] %s（%v）缺岗 %s，编制 %d / 在岗 %d；自动补员失败（召回 %s 未成，详见办公室系统消息）",
					tagFor(projectKey, row.Name), names, util.ShortDur(now.Sub(first)), row.Headcount, got, name))
				continue
			}
		}
		k.cfg.LobbyHub.SystemRecorded(i18n.Sf("[看护] %s（%v）缺岗 %s，编制 %d / 在岗 %d%s",
			tagFor(projectKey, row.Name), names, util.ShortDur(now.Sub(first)), row.Headcount, got,
			k.uncountedNote(projectKey, online, counted)))
	}
}

// uncountedNote renders the 在房未计入 diagnostic for a shortfall
// alarm: seated members holding an OCCUPYING staffing row of this
// project that no table row counted — the mismatch evidence the old
// alarm swallowed (an empty （[]） read as 「没人」, while the writers
// sat there all along with drifted role strings). Empty when everyone
// seated is counted (a true absence needs no editorial).
func (k *Keepers) uncountedNote(projectKey string, online, counted map[string]bool) string {
	if k.cfg.Staff == nil {
		return ""
	}
	type strayed struct{ name, role, post string }
	var out []strayed
	for _, row := range k.cfg.Staff.ListByProject(projectKey) {
		if !row.Occupying() || !online[row.Person] || counted[row.Person] {
			continue
		}
		role := row.ProjectRole
		if role == "" && k.cfg.Agents != nil {
			if cfg, ok := k.cfg.Agents.Get(row.Person); ok {
				role = cfg.Role
			}
		}
		out = append(out, strayed{name: row.Person, role: role, post: row.PostKey})
	}
	if len(out) == 0 {
		return ""
	}
	parts := make([]string, 0, len(out))
	for _, s := range out {
		switch {
		case s.post != "":
			parts = append(parts, i18n.Sf("%s（岗位 %s，未被编制表计入）", s.name, s.post))
		case s.role != "":
			parts = append(parts, i18n.Sf("%s（角色「%s」，与身份列不一致）", s.name, s.role))
		default:
			parts = append(parts, i18n.Sf("%s（无角色、未锚岗位）", s.name))
		}
	}
	return i18n.Sf("；在房未计入（身份串与编制表不一致，宜按 --post 重锚）：%s", strings.Join(parts, "、"))
}

// tagFor renders the alarm's project scope: the lobby keeps the v1
// bare-post shape; a project's post reads [proj-x] 后端.
func tagFor(projectKey, name string) string {
	if projectKey == chat.LobbyKey {
		return name
	}
	return "[" + projectKey + "] " + name
}

// autoRecall is the staffing-domain master switch (P5-a 实施选位:
// model §4.1 puts the behavior switches in staffing — a companion
// settings file; default ON = v1 parity, the per-post 自动补员 column
// still gates each row on top).
func (k *Keepers) autoRecall(projectKey string) bool {
	if k.cfg.Staff == nil {
		return true // no staffing face: the v1 shape, column-only gating
	}
	return k.cfg.Staff.SettingsOf(projectKey).AutoRecall
}

// grace is the shortfall-persistence window: the presence grace (the
// same window a reconnect may reclaim its ghost in).
func (k *Keepers) grace() time.Duration { return chat.PresenceGrace }

// offlineCandidate picks the refill target for a missing post, three
// legs in order (岗位锚 first):
//
//  1. an OCCUPYING staffing row of this project anchored to the post
//     whose member holds no live seat (the departed member themselves
//     — their session binding rides Recall, Birth's formula);
//  2. an OFFBOARDED row of the same post whose person occupies
//     nothing anywhere — seatGone's AUTOMATIC offboard must not bury
//     the recall path (the row keeps binding and anchor; Recall's
//     Join reinstates it), while 一期一人一时一房 still stands: a
//     person seated in ANOTHER project is skipped, not fought over;
//  3. the legacy shape (no anchored row): an occupying row whose
//     effective role equals the table's 身份, else (lobby only, the
//     v1 fallback) an archived-or-offline profile of the post.
func (k *Keepers) offlineCandidate(projectKey string, hub *chat.Hub, row kb.EstablishmentRow) string {
	online := map[string]bool{}
	for _, m := range hub.Members() {
		online[m.Name] = true
	}
	if k.cfg.Staff != nil {
		rows := k.cfg.Staff.ListByProject(projectKey)
		if row.Key != "" {
			for _, r := range rows {
				if r.PostKey == row.Key && r.Occupying() && !online[r.Person] {
					return r.Person
				}
			}
			for _, r := range rows {
				if r.PostKey == row.Key && !r.Occupying() {
					if _, elsewhere := k.cfg.Staff.OccupancyOf(r.Person); !elsewhere {
						return r.Person
					}
				}
			}
		}
		for _, r := range rows {
			if !r.Occupying() || online[r.Person] {
				continue
			}
			if k.effectiveRole(r) == row.Role {
				return r.Person
			}
		}
	}
	if projectKey != chat.LobbyKey || k.cfg.Agents == nil {
		return ""
	}
	for _, c := range k.cfg.Agents.ListFiltered(true) { // archived first: recall is their rehire
		if c.Role == row.Role && !online[c.Name] {
			return c.Name
		}
	}
	for _, c := range k.cfg.Agents.ListFiltered(false) {
		if c.Role == row.Role && !online[c.Name] {
			return c.Name
		}
	}
	return ""
}

// effectiveRole is the row's seat role: project role, profile default
// when unset (birthStaffed's own fallback rule).
func (k *Keepers) effectiveRole(row staffing.Entry) string {
	if row.ProjectRole != "" {
		return row.ProjectRole
	}
	if k.cfg.Agents != nil {
		if cfg, ok := k.cfg.Agents.Get(row.Person); ok && cfg.Role != "" {
			return cfg.Role
		}
	}
	return ""
}

// alarmThrottled emits a keeper alarm on the parse-failure cooldown.
func (k *Keepers) alarmThrottled(projectKey string, now time.Time, text string) {
	if now.Sub(k.parseLast[projectKey]) < WatchCooldown {
		return
	}
	k.parseLast[projectKey] = now
	k.cfg.LobbyHub.SystemRecorded(text)
}
