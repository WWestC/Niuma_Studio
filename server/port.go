package server

import (
	"encoding/json"
	"fmt"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/staffing"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The port's whole life (范式重构 S7): the fixed-7777 discipline —
// occupy-and-takeover (an older niuma holding the port is SIGTERMed
// and the port assumed; anything else is a hard error), the discovery
// file (~/.niuma_port "port PID"), graceful restart with the seat
// snapshot hand-over, and the probes. The AGENTS rules live here:
// 7777 always, never fall forward, and — 禁止多开 — ONE instance per
// machine (a --port sidestep is refused in main via OtherLiveInstance:
// two concurrent instances would share-corrupt the ledgers).

// bindHost normalizes a BindAddr into the host leg for Listen and the
// HTTP probes: empty/localhost stay the historical loopback literal;
// wildcard bindings probe via loopback (that is the one address every
// wildcard bind certainly serves); a specific IP probes itself.
func bindHost(bind string) string {
	switch strings.TrimSpace(bind) {
	case "", "localhost":
		return "127.0.0.1"
	case "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return strings.Trim(bind, "[]")
	}
}

// listenPort binds the one fixed port. When it is already held by
// another niuma instance, that instance is terminated so this
// process can take the port over (restart semantics: callers must be
// able to rely on a single, constant port). A port held by anything
// else is a hard error. bind selects the interface (BindAddr, empty =
// the historical loopback-only root).
//
// Takeover guard (ARB-1): a probe-healthy room is only ever taken over
// from an interactive TTY (the human host's restart) or with the
// explicit --force-takeover flag. A non-interactive start (AI script,
// mistyped bare command) against a healthy room refuses with usage —
// probePort doubles as the health probe: an HTTP 200 with our name
// means alive, anything else is a zombie that still gets cleaned up
// automatically.
func listenPort(port int, bind string, discoFile string, force, interactive bool) (net.Listener, error) {
	host := bindHost(bind)
	if port < 0 {
		// ephemeral mode: the kernel picks; nothing to take over
		return net.Listen("tcp", net.JoinHostPort(host, "0"))
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		return ln, nil
	}
	if !probePort(port, bind) {
		return nil, fmt.Errorf("port %d is occupied by a non-niuma process; free it or start with --port: %w", port, err)
	}
	if !force && !interactive {
		return nil, fmt.Errorf("port %d holds a healthy niuma room; a non-interactive start refuses to take it over "+
			"(re-run from a terminal, or pass --force-takeover to restart it)", port)
	}
	// The listener verified as niuma; collect its pid from the
	// discovery file and (unix) from lsof, since the file may be stale.
	// Every candidate except ourselves gets terminated.
	pids := map[int]bool{}
	if pid, ok := instancePID(port, discoFile); ok {
		pids[pid] = true
	}
	for _, pid := range portHolders(port) {
		pids[pid] = true
	}
	delete(pids, os.Getpid())
	if len(pids) == 0 {
		return nil, fmt.Errorf("port %d is held by another niuma but its pid is unknown (stale discovery file and no holder could be identified); stop it manually or start with --port", port)
	}

	// v0.6 graceful takeover (§3.2): ask each holder to exit politely
	// first — a v0.6+ instance answers SIGTERM by announcing the
	// restart, waiting TakeoverGraceDelay and closing; the room's guards
	// read the notice instead of vanishing blind. A pre-v0.6 instance
	// ignores SIGTERM (default handler exits on SIGTERM actually — but
	// without the notice) or anything still holding the port after the
	// window falls through to the hard Kill below, so takeover always
	// completes within the bounded wait.
	asked := 0
	for pid := range pids {
		if p, err := os.FindProcess(pid); err == nil {
			log.Printf("port %d held by niuma (pid %d): requesting graceful restart (SIGTERM)", port, pid)
			if gracefulTerminate(p) {
				asked++
			}
		}
	}
	if asked > 0 {
		if ln, err = awaitPortFree(port, bind, TakeoverGraceDelay+5*time.Second); err == nil {
			return ln, nil
		}
		log.Printf("port %d not released gracefully within %s: falling back to hard termination", port, TakeoverGraceDelay+5*time.Second)
	}

	for pid := range pids {
		log.Printf("terminating niuma pid %d (hard)", pid)
		if p, kerr := os.FindProcess(pid); kerr == nil {
			if kerr := p.Kill(); kerr != nil {
				log.Printf("kill pid %d: %v", pid, kerr)
			}
		}
	}
	ln, err = awaitPortFree(port, bind, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("port %d did not free up after terminating pids %v: %w", port, pids, err)
	}
	return ln, nil
}

// awaitPortFree polls every 100ms until the port binds again or the
// budget runs out.
func awaitPortFree(port int, bind string, budget time.Duration) (net.Listener, error) {
	host := bindHost(bind)
	for i := 0; i < int(budget/(100*time.Millisecond)); i++ {
		time.Sleep(100 * time.Millisecond)
		if ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
			return ln, nil
		}
	}
	return nil, fmt.Errorf("port %d still held", port)
}

// portHolders returns the pids currently listening on the port, via lsof
// where available (unix). Best-effort: empty on error or on platforms
// without lsof. Callers must only use it after probePort confirmed the
// listener is a niuma instance.
func portHolders(port int) []int {
	if runtime.GOOS == "windows" {
		return nil
	}
	// LISTEN-only: a bare tcp:PORT match also returns every CLIENT
	// connected to the room, and killing them on takeover silently
	// SIGKILLed all members' guard/wait/listen processes on every
	// restart (the collective vanishings of v10/v11).
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// instancePID reads the pid recorded for the given port in the
// discovery file ("port pid" per line; empty path means the
// conventional ~/.niuma_port). ok is false when the file is
// missing, unreadable, or refers to a different port.
func instancePID(port int, path string) (pid int, ok bool) {
	if path == "" {
		var err error
		if path, err = PortFilePath(); err != nil {
			return 0, false
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var p int
	if _, err := fmt.Sscanf(string(b), "%d %d", &p, &pid); err != nil || p != port {
		return 0, false
	}
	return pid, true
}

// InstancePID resolves the pid the discovery file records for the
// given port (empty path means the conventional file). The shell's
// room-window focus consumes it (v2 P5-c); the file-format truth
// stays here, single home.
func InstancePID(port int) (int, bool) { return instancePID(port, "") }

// recordedInstance reads the machine's instance record: the port and
// pid the discovery file names ("port pid"), ok=false when absent,
// unreadable, or empty.
func recordedInstance(path string) (port, pid int, ok bool) {
	if path == "" {
		var err error
		if path, err = PortFilePath(); err != nil {
			return 0, 0, false
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(string(b), "%d %d", &port, &pid); err != nil {
		return 0, 0, false
	}
	return port, pid, true
}

// pidAlive probes a process with signal 0 (existence check; harmless
// on Windows too).
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// OtherLiveInstance is the 禁止多开 probe: it reports a live, HEALTHY
// niuma instance of this machine recorded on a port other than port
// (ok=true → a second instance must not start). The pid must be alive
// AND its port must still answer the niuma probe — a stale record
// (crashed instance, recycled pid) never blocks a fresh start. The
// same-port case is deliberately NOT reported: that is the designed
// takeover-restart flow, which converges to one instance.
func OtherLiveInstance(port int) (recPort, recPID int, ok bool) {
	recPort, recPID, ok = recordedInstance("")
	if !ok || recPort == port || recPID == os.Getpid() || !pidAlive(recPID) || !probePort(recPort, "") {
		return 0, 0, false
	}
	return recPort, recPID, true
}

// Addr returns the ws:// URL of the endpoint.

// TakeoverGraceDelay is how long a v0.6 instance asked to restart
// (SIGTERM) waits between announcing the restart and closing — the
// guards' heads-up window (v0.6 §3.2, default N=3s). The taker-over
// waits this plus 5s before degrading to a hard kill.
const TakeoverGraceDelay = 3 * time.Second

// GracefulRestart is what a v0.6 instance runs on SIGTERM during a
// takeover: announce the restart into history (reconnecting members
// replay it and know why the room blinked), hold the room open for
// delay so guards read the notice, then close the listener and remove
// the discovery file. The caller exits the process afterwards — the
// pixel UI cannot be torn down from a signal goroutine portably.
func GracefulRestart(hub *chat.Hub, srv *Server, delay time.Duration) {
	hub.SystemRecorded(i18n.Sf(
		"办公室将于 %d 秒后重启（新实例接管），值守成员请按退避重连", int(delay.Seconds())))
	time.Sleep(delay)
	srv.Close()
}

// Close shuts the server down and removes the discovery file.

// roomSnapshot is the on-disk handover payload (v0.10 seat-M2).
type roomSnapshot struct {
	Version int                 `json:"version"`
	LastSeq int64               `json:"last_seq"`
	SavedAt int64               `json:"saved_at"`
	Seats   []chat.SeatSnapshot `json:"seats"`
}

// writeSnapshot persists the lobby roster (opts.SnapshotPath) and, when
// a registry is attached, every instantiated project room's roster
// under its own v2 slot (room/<key>.json, v2 P3-a) — all atomically
// (temp + rename) on the graceful-exit path. A failed write logs and
// continues: the next instance falls back to the previous snapshot
// (stale beats none).
func (s *Server) writeSnapshot() {
	if s.noFlush.Load() {
		return // 熔断（重置擦除后）：内存里的旧房间不得再写回已擦净的盘
	}
	s.writeSnapshotTo(s.opts.Endpoint.SnapshotPath, s.hub)
	if s.opts.Registry == nil {
		return
	}
	for key, h := range s.opts.Registry.Rooms() {
		if key == chat.LobbyKey {
			continue // the lobby snapshot is opts.SnapshotPath above
		}
		s.writeSnapshotTo(s.opts.Registry.SnapshotPath(key), h)
	}
}

// SuppressSnapshots trips the snapshot breaker — one-way, for the
// factory reset: the choreography flushes once (srv.Close BEFORE the
// wipe), then trips this so any LATER close on the same process
// (window close racing the exit, a SIGTERM's GracefulRestart, the
// deferred flush) can never resurrect the wiped rooms from memory.
func (s *Server) SuppressSnapshots() { s.noFlush.Store(true) }

// writeSnapshotTo persists one hub's seat snapshot at path ("" skips).
// r_20 t_195：Working（中断席位）在此刻提炼接续要点——先采要点再
// teardown（定稿 §五·2 顺序铁律）；提炼失败（无 git/无 note）退空，
// 注入侧自然落泛模板。
func (s *Server) writeSnapshotTo(path string, h *chat.Hub) {
	if path == "" {
		return
	}
	seats := h.Seats()
	for i := range seats {
		if seats[i].Working {
			seats[i].ContextNotes = s.roomopsFace().DistillResumeNotes(h, seats[i].Name)
		}
	}
	snap := roomSnapshot{Version: 1, SavedAt: time.Now().UnixMilli(), Seats: seats}
	snap.LastSeq = h.LastSeq()
	b, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if err := persist.Save(path, b, 0o600); err != nil {
		log.Printf("room snapshot write: %v", err)
	}
}

// LoadRoomSnapshot restores a snapshot into a fresh hub before the
// owner joins: every seat becomes a full-window grace ghost, lastSeq
// floors the monotonic counter. It also reports the seats the farewell
// snapshot caught mid-turn (Working) — the caller rolls them into the
// restart's continue prompt; the ghosts themselves never carry the
// flag (a restored seat works nowhere until its dispatcher returns).
// A corrupt file is renamed *.corrupt-<ts> and ignored — a snapshot
// must never refuse boot.
//
// roomKey+staff reconcile the seats against the staffing table: a
// seat whose name is actively staffed in ANOTHER project's room is
// dropped (the 小苗-2 loop: the member migrated lobby→book, the old
// room's snapshot kept resurrecting their leftover ghost — and a
// paused room freezes the grace clock forever, so every same-name CLI
// dial twinned into "-2"). One person, one room at a time is the
// house rule; the snapshot's memory of the old room loses to the
// ledger. A nil store (tests, bare embeds) keeps every seat.
func LoadRoomSnapshot(path string, h *chat.Hub, roomKey string, staff *staffing.Store) []chat.InterruptedSeat {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // absent: cold start
	}
	var snap roomSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		_ = os.Rename(path, fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix()))
		log.Printf("room snapshot unreadable, cold start: %v", err)
		return nil
	}
	var interrupted []chat.InterruptedSeat
	seats := snap.Seats
	if staff != nil {
		kept := make([]chat.SeatSnapshot, 0, len(seats))
		for _, st := range seats {
			if e, ok := staff.OccupancyOf(st.Name); ok && e.ProjectKey != roomKey {
				log.Printf("room snapshot: 房 %s 丢弃他房在编座位 %s（现于 %s 上班，不恢复宽限幽灵）", roomKey, st.Name, e.ProjectKey)
				continue
			}
			kept = append(kept, st)
		}
		seats = kept
	}
	for _, st := range seats {
		if st.Working && st.Name != "" {
			interrupted = append(interrupted, chat.InterruptedSeat{Name: st.Name, Since: st.WorkingSince, ContextNotes: st.ContextNotes})
		}
	}
	h.RestoreSeats(seats)
	h.FloorSeq(snap.LastSeq)
	return interrupted
}

// PortFilePath returns the conventional discovery file location:
// ~/.niuma_port.
func PortFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_port"), nil
}

// DiscoverPort finds a running instance via the discovery file, falling
// back to a probe of the default port. The server binds one fixed port
// (7777 unless --port was used), so no range scan is needed.
func DiscoverPort() (int, error) {
	if f, err := PortFilePath(); err == nil {
		if b, err := os.ReadFile(f); err == nil {
			var p int
			if _, err := fmt.Sscanf(string(b), "%d", &p); err == nil && probePort(p, "") {
				return p, nil
			}
		}
	}
	const def = 7777
	if probePort(def, "") {
		return def, nil
	}
	return 0, fmt.Errorf("no running niuma found (checked ~/.niuma_port and port 7777)")
}

func probePort(p int, bind string) bool {
	url := fmt.Sprintf("http://%s/", net.JoinHostPort(bindHost(bind), strconv.Itoa(p)))
	cl := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := cl.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var info struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false
	}
	return info.Name == "niuma"
}

// --- HTTP handlers -----------------------------------------------------
