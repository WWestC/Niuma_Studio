package dispatch

// bridgepool.go — the app-server child pool (v3 SPOF 根治第一段).
// Spawn/health/respawn used to live in package main as one closure, one
// *zcode.Client variable and one anonymous watchdog goroutine — one
// child, one blast radius: a crash blinked EVERY room at once and the
// whole recovery ladder restarted on a single corpse. The pool owns
// that machinery behind two seams instead:
//
//   - BridgeSpawner: HOW a child is born stays in main (node/bundle
//     discovery, provider sync, room notices); the pool only knows
//     "" = live.
//   - Bridge (the existing driving seam): each member wraps its child
//     in its OWN sharedBridge, and a project's dispatcher is routed to
//     exactly one member (sticky, key-hashed) — a member's events fan
//     out only to the rooms it carries, so one corpse blinks only its
//     own rooms while the watchdog re-spawns it on the same 10s
//     throttle boot always had.
//
// Sessions are re-homed by resume, not migrated: the CLI-side session
// store is machine-shared, ANY child can resume ANY session id, so
// bridge affinity is per-dispatcher (project), and an adopted member
// simply comes back up on its room's bridge. Size comes from
// NIUMA_BRIDGE_POOL (default 1 — byte-for-byte the old single-child
// behavior: one member, one watchdog, the same room lines).

import (
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/util"
)

// BridgeSpawner attempts one app-server child. notify asks for room
// notices on failure (the spawner's own contract, inherited from
// startZCodeBridge); the returned fail is "" when the bridge is live,
// "missing" when the bundle is absent, else the Start error text.
type BridgeSpawner func(notify bool) (Bridge, string)

// healthBridge is the liveness/readiness face the real *zcode.Client
// carries. A member whose inner bridge lacks it (the test fakes) is
// treated as immortal-and-ready — pools over fakes never respawn.
type healthBridge interface {
	Alive() bool
	ReadyNow() bool
}

// closer is the shutdown ladder (stdin → SIGTERM → SIGKILL) the real
// client carries; pool.Close walks it for every live member.
type closer interface {
	Close() error
}

// PoolOptions configures NewBridgePool. Hub is the announcement room
// (nil silences the respawn/recovery lines — tests); Closing is the
// boot's shared quit flag (the watchdog must never respawn a child
// behind a closing room); OnSpawn fires after EVERY successful spawn —
// boot wires the fleet on the first one and re-warms the assistant on
// each.
type PoolOptions struct {
	Size    int
	Hub     *chat.Hub
	Closing *atomic.Bool
	OnSpawn func()
}

// BridgePool is N app-server children with sticky per-project routing.
// The zero value is not usable — NewBridgePool builds it.
type BridgePool struct {
	spawner  BridgeSpawner
	hub      *chat.Hub
	closing  *atomic.Bool
	onSpawn  func()
	size     int
	done     chan struct{}
	wg       sync.WaitGroup
	watchRun sync.Once

	mu       sync.Mutex
	members  []*poolMember
	assign   map[string]int // project key → member id (sticky)
	fail     string         // last spawn error ("" once any member is live)
	lastTry  time.Time      // pool-wide 10s throttle (the boot ladder's)
	spawning bool           // a spawn attempt is in the air (single-flight)
	started  time.Time      // earliest live member's spawn time
	everFail bool
}

type poolMember struct {
	id int
	sb *sharedBridge // nil until its first live spawn
}

// NewBridgePool builds the pool and starts its watchdog. No child is
// spawned yet — TrySpawn does that (boot's tryDispatch and the gate's
// retry both land there).
func NewBridgePool(spawner BridgeSpawner, opt PoolOptions) *BridgePool {
	if opt.Size < 1 {
		opt.Size = 1
	}
	p := &BridgePool{
		spawner: spawner, hub: opt.Hub, closing: opt.Closing,
		onSpawn: opt.OnSpawn, size: opt.Size,
		done:   make(chan struct{}),
		assign: map[string]int{},
	}
	for i := 0; i < opt.Size; i++ {
		p.members = append(p.members, &poolMember{id: i})
	}
	p.wg.Add(1)
	go util.Guard("dispatch: bridge pool watchdog", p.watch)
	return p
}

// TrySpawn attempts to bring every still-missing member up (the 10s
// pool-wide throttle is the boot ladder's, unchanged: a broken spawn
// must not fork-bomb). notify rides to the spawner's room notices.
// The attempt runs on its own goroutine, single-flight: the spawn now
// blocks through the account-registry push (see startZCodeBridge) and
// the splash-gate poll must never queue behind a child's boot.
func (p *BridgePool) TrySpawn(notify bool) {
	if p.isClosing() {
		return // shutdown in flight: never respawn behind a closing room
	}
	p.mu.Lock()
	if p.spawning {
		p.mu.Unlock()
		return // already in the air — the throttle plus this guard fork-bomb-proof it
	}
	p.spawning = true
	p.lastTry = time.Now()
	pending := make([]*poolMember, 0, len(p.members))
	for _, m := range p.members {
		if m.sb == nil || !p.innerAliveLocked(m) {
			pending = append(pending, m)
		}
	}
	p.mu.Unlock()
	go func() {
		defer func() {
			p.mu.Lock()
			p.spawning = false
			p.mu.Unlock()
		}()
		for _, m := range pending {
			if p.isClosing() {
				return
			}
			p.spawnOne(m, notify)
		}
	}()
}

// EverLive reports whether any child has ever come up — the gate's
// !alive projection uses it to tell "still on the first boot" (show
// 启动中) from "had a child and lost it" (show the respawn notice).
func (p *BridgePool) EverLive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.started.IsZero()
}

// spawnOne runs one member's spawn attempt and wires the result.
func (p *BridgePool) spawnOne(m *poolMember, notify bool) {
	nb, fail := p.spawner(notify)
	p.mu.Lock()
	if fail != "" {
		p.fail = fail
		p.everFail = true
		p.mu.Unlock()
		return
	}
	recovered := p.everFail
	corpse := m.sb != nil
	if m.sb == nil {
		m.sb = newSharedBridge(nb)
	} else {
		m.sb.swap(nb)
	}
	if p.started.IsZero() {
		p.started = time.Now()
	}
	p.fail = ""
	onSpawn, hub := p.onSpawn, p.hub
	size := p.size
	p.mu.Unlock()
	if hub != nil {
		switch {
		case corpse:
			// mid-shift death recovery: the member's dispatchers keep
			// their sharedBridge pointer — the swap re-points it at the
			// new child; their sessions re-resume through it.
			hub.System(p.memberLine("[调度] ZCode app-server 已退出——已自动重启，成员调度恢复", m, size))
		case recovered:
			hub.System(p.memberLine("[调度] 已识别 ZCode CLI——成员调度上线，牛马将陆续归位", m, size))
		}
	}
	if onSpawn != nil {
		onSpawn()
	}
}

// memberLine tags the room line with the member's slot when the pool
// runs more than one child (member 0 keeps the historical text).
func (p *BridgePool) memberLine(text string, m *poolMember, size int) string {
	if size <= 1 || m.id == 0 {
		return text
	}
	return fmt.Sprintf("[调度]#%d %s", m.id+1, text[len("[调度]"):])
}

// RetryDue reports whether the gate's poll may fire another attempt
// (the same 10s throttle; true while some member is missing).
func (p *BridgePool) RetryDue() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.lastTry) < 10*time.Second {
		return false
	}
	for _, m := range p.members {
		if m.sb == nil || !p.innerAliveLocked(m) {
			return true
		}
	}
	return false
}

// Health is the splash door's projection input: fail is the last spawn
// error while NO member is live, alive/ready aggregate the live
// members (the door reflects the worst member), since is the earliest
// live member's spawn time (the 30s speechless-child wedged test).
func (p *BridgePool) Health() (fail string, alive, ready bool, since time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	live := 0
	alive, ready = true, true
	since = time.Time{}
	for _, m := range p.members {
		if m.sb == nil || !p.innerAliveLocked(m) {
			alive = false
			continue
		}
		live++
		hb, ok := m.sb.Bridge.(healthBridge)
		if ok {
			if !hb.Alive() {
				alive = false
			}
			if !hb.ReadyNow() {
				ready = false
			}
		}
		if since.IsZero() || p.started.Before(since) {
			since = p.started
		}
	}
	if live == 0 {
		return p.fail, false, false, time.Time{}
	}
	return "", alive, ready, since
}

// Route returns the project's sticky member bridge (nil while no
// member is live). The lobby pins to member 0 — the assistant and the
// flagship recruiter ride the same child they always did; other keys
// hash across the live set and stay put while their member lives.
func (p *BridgePool) Route(key string) *sharedBridge {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.assign[key]; ok {
		if m := p.members[id]; m.sb != nil && p.innerAliveLocked(m) {
			return m.sb
		}
	}
	var live []*poolMember
	for _, m := range p.members {
		if m.sb != nil && p.innerAliveLocked(m) {
			live = append(live, m)
		}
	}
	if len(live) == 0 {
		return nil
	}
	pick := live[0]
	if key != chat.LobbyKey && len(live) > 1 {
		h := fnv.New32a()
		_, _ = h.Write([]byte(key))
		pick = live[h.Sum32()%uint32(len(live))]
	}
	p.assign[key] = pick.id
	return pick.sb
}

// SwapDead swaps nb into the member whose inner bridge is dead (the
// legacy ReplaceBridge face; with a live-everywhere pool it swaps
// member 0). Reports whether a swap happened.
func (p *BridgePool) SwapDead(nb Bridge) bool {
	if nb == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	victim := p.members[0]
	for _, m := range p.members {
		if m.sb != nil && !p.innerAliveLocked(m) {
			victim = m
			break
		}
	}
	if victim.sb == nil {
		return false
	}
	victim.sb.swap(nb)
	return true
}

// Close stops the watchdog and walks every live member's shutdown
// ladder. Idempotent.
func (p *BridgePool) Close() {
	select {
	case <-p.done:
		return
	default:
	}
	close(p.done)
	p.wg.Wait()
	p.mu.Lock()
	members := append([]*poolMember(nil), p.members...)
	p.mu.Unlock()
	for _, m := range members {
		p.mu.Lock()
		inner := m.sb.Bridge
		p.mu.Unlock()
		if c, ok := inner.(closer); ok {
			_ = c.Close()
		}
	}
}

// watch is the watchdog the anonymous boot goroutine used to be: poll
// every member once a second, re-run TrySpawn on the 10s throttle when
// one is missing or dead. Fenced per beat (the house rule for
// long-lived loops: a panicking tick loses one beat, not the pool).
func (p *BridgePool) watch() {
	defer p.wg.Done()
	for {
		select {
		case <-p.done:
			return
		case <-time.After(time.Second):
		}
		if p.isClosing() {
			return
		}
		util.Guard("dispatch: bridge pool tick", func() {
			p.mu.Lock()
			last := p.lastTry
			missing := false
			for _, m := range p.members {
				if m.sb == nil || !p.innerAliveLocked(m) {
					missing = true
					break
				}
			}
			p.mu.Unlock()
			if missing && time.Since(last) >= 10*time.Second {
				p.TrySpawn(false)
			}
		})
	}
}

func (p *BridgePool) isClosing() bool {
	return p.closing != nil && p.closing.Load()
}

// innerAliveLocked reports the member child's liveness: a missing
// probe (test fakes) counts as alive, a nil bridge never does.
// Callers hold p.mu.
func (p *BridgePool) innerAliveLocked(m *poolMember) bool {
	if m.sb == nil {
		return false
	}
	hb, ok := m.sb.Bridge.(healthBridge)
	if !ok {
		return true
	}
	return hb.Alive()
}
