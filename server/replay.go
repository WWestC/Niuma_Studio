package server

// replay.go — the 公司一日回放's server face (r_32): the snapshot-frame
// persistence (the recording leg), the day player's read face, and the
// 分享 gate. 设计三条腿，全部踩在既有先例上：
//
//   存储 — append-only jsonl，一日一文件：replay/<key>/<yyyymmdd>.jsonl
//          （终端转录 terminal/<key>/<名>.jsonl 的同构；帧 t = 墙钟 ms，
//          跨重启/跨会话天然拼成一天的连续时间轴）。
//   读面 — 照 /p/{key}/history 的 since/limit 分页形状（oldest-first，
//          has_more + next_since），播放器按需翻页拉当日帧。
//   分享 — dayReplayGate：访客通道（r_19 visitor.go）的 token 纪律照抄
//          （16 字节随机 hex；错 token → 404 不确认存在性；重开换新）。
//          链接按 (项目, 日期) 粒度授权——分享的是房主挑的那一天，不是
//          实时房间的历史（r_19「访客不开放时间轴」的口径不破）。
//
// 防灌帽：单日单房文件 256MB——本机单用户本到不了这个量（活跃一天
// 10–20MB），它挡的是访客通道公网暴露时的写盘 DoS（/media 8MB 帽同款
// 考虑）。保留期复用聊天的 retention 旋钮（retention.go 的 sweep 里加
// 一段按文件名日期整文件删）。

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// dayRecorded reports whether the room's day file exists and is
// non-empty (the share mint's honest-day check).
func dayRecorded(root, key, day string) bool {
	fi, err := os.Stat(filepath.Join(replayDir(root, key), day+".jsonl"))
	return err == nil && fi.Size() > 0
}

const (
	// replayBatchCap bounds one POST's frame count (the recorder sends
	// every 20s ≈ 40 frames; the cap is the abuse fence, not the spec).
	replayBatchCap = 600
	// replayActorsCap bounds one frame's actor list (the office seats
	// dozens; a frame claiming thousands is not ours).
	replayActorsCap = 200
	// replayDayPat is the filename date layout (local day of frame t).
	replayDayPat = "20060102"
	// replayBodyCap bounds one upload request's body (the recorder sends
	// ~40 compact frames per 20s batch; 8MB is the /media-style fence
	// against oversized posts, not the spec).
	replayBodyCap = 8 << 20
)

// replayDayCap is the per-room per-day file size fence: 413 past it.
// A var, not a const — the 413 test shrinks it instead of writing a
// quarter gigabyte to a temp dir.
var replayDayCap int64 = 256 << 20

const (
	replayDefaultLimit = 2000
	replayMaxLimit     = 10000
)

// replayProbe validates one incoming frame WITHOUT reshaping it: the raw
// JSON line is stored verbatim and streamed back verbatim (r_32.2 root
// cure). 首版曾用结构体解码再编码落盘——前端加了字段后端没跟，字段
// 就被静默剥掉（r_32.1 的 bt/dk 断线即此类）。透传之后服务端对帧面
// schema 零认知：前端加列永不丢，本 bug 类根治。R 只是占位探测——房
// 间级视态对象整块透传，服务端不拆看。
type replayProbe struct {
	T int64             `json:"t"`
	A []json.RawMessage `json:"a"`
}

// probeReplay validates one raw frame line (t present/positive, actor
// count under the fence). Returns the probe, or nil when the line isn't
// ours (bad JSON / bad shape — the caller skips or rejects).
func probeReplay(raw []byte) *replayProbe {
	var p replayProbe
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	if p.T <= 0 || len(p.A) > replayActorsCap {
		return nil
	}
	return &p
}

// replayStore is the frame journal's single-writer face: one mutex covers
// every room's append (the recorder posts once per 20s — contention is
// not a thing) and the last-t watermark that makes re-sent batches
// (pagehide flush racing the interval flush) idempotent.
type replayStore struct {
	mu   sync.Mutex
	last map[string]int64 // "<key>/<yyyymmdd>" → last appended t
}

func newReplayStore() *replayStore {
	return &replayStore{last: map[string]int64{}}
}

// replayDir is key's slot under the v2 root.
func replayDir(root, key string) string {
	return filepath.Join(root, "replay", key)
}

// replayDayOf folds a wall-clock ms into its local day (the filename).
func replayDayOf(ms int64) string {
	return time.UnixMilli(ms).Format(replayDayPat)
}

// Append folds frames into their day files (a batch crossing midnight
// simply lands in two). Frames at or below the day's watermark are
// skipped (out-of-order or duplicate re-sends), not errors. 帧行原样
// 落盘（r_32.2 透传——不 reshape，见 replayProbe）。
func (s *replayStore) Append(root, key string, frames []json.RawMessage) (stored, skipped int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range frames {
		raw := bytes.TrimSpace(frames[i])
		if len(raw) == 0 {
			skipped++
			continue
		}
		p := probeReplay(raw)
		if p == nil {
			skipped++
			continue
		}
		day := replayDayOf(p.T)
		lk := key + "/" + day
		if p.T <= s.last[lk] {
			skipped++ // 重复/乱序：水位线以下照丢（幂等腿）
			continue
		}
		dir := replayDir(root, key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return stored, skipped, err
		}
		path := filepath.Join(dir, day+".jsonl")
		if fi, err := os.Stat(path); err == nil && fi.Size() > replayDayCap {
			return stored, skipped, errReplayCap
		}
		fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return stored, skipped, err
		}
		_, werr := fh.Write(append(append([]byte{}, raw...), '\n'))
		fh.Close()
		if werr != nil {
			return stored, skipped, werr
		}
		s.last[lk] = p.T
		stored++
	}
	return stored, skipped, nil
}

// errReplayCap is the 413 twin (a sentinel the handler maps to status).
var errReplayCap = &replayCapError{}

type replayCapError struct{}

func (*replayCapError) Error() string {
	return i18n.S("当日回放文件已超出上限，今天的录制暂停落盘")
}

// forget drops the room's watermark entries — the project reset's
// half (the day files themselves go with the replay dir removal; the
// watermarks must not survive them, or a same-day re-recording would
// be silently swallowed by a stale last-t).
func (s *replayStore) Forget(key string) { s.forget(key) }

func (s *replayStore) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := key + "/"
	for k := range s.last {
		if strings.HasPrefix(k, prefix) {
			delete(s.last, k)
		}
	}
}

// Read streams one day's frames oldest-first past the since cursor (ms,
// exclusive) — the history face's paging shape (has_more + next_since).
// A missing file is an empty day, not an error (今天还没录是正常态).
// 行原样返回（json.RawMessage——响应组装时按原 JSON 嵌入）。
func (s *replayStore) Read(root, key, day string, since int64, limit int) (frames []json.RawMessage, hasMore bool, next int64, err error) {
	frames = []json.RawMessage{}
	path := filepath.Join(replayDir(root, key), day+".jsonl")
	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return frames, false, since, nil
		}
		return frames, false, since, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		p := probeReplay(line)
		if p == nil {
			continue // 坏行跳过——一日文件不因一行中断
		}
		if p.T <= since {
			continue
		}
		if len(frames) >= limit {
			hasMore = true
			return frames, hasMore, next, nil
		}
		// 拷贝再上账：Scanner 的缓冲复用，RawMessage 必须独占底层数组
		own := make([]byte, len(line))
		copy(own, line)
		frames = append(frames, json.RawMessage(own))
		next = p.T
	}
	if err := sc.Err(); err != nil {
		return frames, hasMore, next, err
	}
	return frames, false, next, nil
}

// Days lists the room's recorded days, newest first, dashed (YYYY-MM-DD).
func (s *replayStore) Days(root, key string) []string {
	entries, err := os.ReadDir(replayDir(root, key))
	if err != nil {
		return []string{}
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		day := strings.TrimSuffix(name, ".jsonl")
		if len(day) != 8 {
			continue
		}
		if _, err := time.Parse(replayDayPat, day); err != nil {
			continue
		}
		out = append(out, day[:4]+"-"+day[4:6]+"-"+day[6:8])
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// Prune removes day files older than the keep cutoff (retention.go's
// sweep leg — day-granular policy, whole-file delete, 永久 sweeps
// nothing). Returns the dropped file count.
func (s *replayStore) Prune(root, keepDay string) int {
	rooms, err := os.ReadDir(filepath.Join(root, "replay"))
	if err != nil {
		return 0
	}
	dropped := 0
	for _, room := range rooms {
		if !room.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, "replay", room.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			day := strings.TrimSuffix(name, ".jsonl")
			if len(day) != 8 || day >= keepDay {
				continue
			}
			if os.Remove(filepath.Join(root, "replay", room.Name(), name)) == nil {
				dropped++
			}
		}
	}
	return dropped
}

// ── 分享 gate（访客通道纪律的 (key,date) 粒度版）────────────────────

// dayReplayGate mints and checks the 一日回放 share token: one server-wide
// epoch token with per-(room,day) grants. Runtime memory only —重启即失效，
// 与访客通道同拍（服务器不在线链接本来也打不开）。
type dayReplayGate struct {
	mu     sync.Mutex
	token  string
	grants map[string]map[string]bool // key → set of dashed dates
}

// Grant adds (key, date) to the current epoch (minting one if closed)
// and returns the live token.
func (g *dayReplayGate) Grant(key, date string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" {
		buf := make([]byte, 16)
		// 忽略错误会让坏熵源铸出全零常量令牌（安全核查修复）——fail-hard。
		if _, err := rand.Read(buf); err != nil {
			panic(fmt.Sprintf("day-replay token: crypto/rand 不可用：%v", err))
		}
		g.token = hex.EncodeToString(buf)
		g.grants = map[string]map[string]bool{}
	}
	if g.grants[key] == nil {
		g.grants[key] = map[string]bool{}
	}
	g.grants[key][date] = true
	return g.token
}

// Revoke kills the epoch: every link dies, the next Grant mints fresh.
func (g *dayReplayGate) Revoke() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.token = ""
	g.grants = nil
}

// Check validates one access (wrong token / ungranted day → false; the
// caller answers 404 — 存在性不对外确认).
func (g *dayReplayGate) Check(token, key, date string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.token != "" && token != "" && token == g.token &&
		g.grants[key] != nil && g.grants[key][date]
}

// On reports the share epoch's state + this room's granted days (the
// owner UI's read face).
func (g *dayReplayGate) On(key string) (bool, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token == "" {
		return false, []string{}
	}
	days := []string{}
	for d := range g.grants[key] {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	return true, days
}

// ── HTTP faces ────────────────────────────────────────────────────

// replayNormDay accepts YYYYMMDD or YYYY-MM-DD into the compact form
// ("" = malformed).
func replayNormDay(v string) string {
	d := strings.ReplaceAll(v, "-", "")
	if _, err := time.Parse(replayDayPat, d); err != nil {
		return ""
	}
	return d
}

// replayRoot resolves the store's base ("" = in-memory server: the
// whole face 404s — retention 的同款纪律).
func (s *Server) replayRoot() (string, bool) {
	if s.opts.Registry == nil || s.opts.Registry.Root() == "" {
		return "", false
	}
	return s.opts.Registry.Root(), true
}

// handleReplayFrames is /p/{key}/replay: POST = the recorder's batch
// append (localhost owner face), GET = the player's paged day read
// (owner face, or the share token path for 分享链接访客).
func (s *Server) handleReplayFrames(w http.ResponseWriter, r *http.Request) {
	root, ok := s.replayRoot()
	if !ok {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	// resolving through the registry is the availability check (the
	// history face's discipline — unknown/draft/archived refuse).
	if _, err := s.opts.Registry.Hub(key); err != nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.handleReplayUpload(w, r, root, key)
	case http.MethodGet:
		s.handleReplayRead(w, r, root, key)
	default:
		writeJSONErr(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

func (s *Server) handleReplayUpload(w http.ResponseWriter, r *http.Request, root, key string) {
	// r_32.2：体量护栏——透传落盘前的第一道闸（批数帽之外的单请求
	// 字节帽；/media 8MB 同款考虑）。录制约 40 帧/批 × 几 KB，8MB 是
	// 宽松上界。
	r.Body = http.MaxBytesReader(w, r.Body, replayBodyCap)
	var body struct {
		Frames []json.RawMessage `json:"frames"`
	}
	if err := httputil.ReadJSONBody(r, &body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
		return
	}
	if len(body.Frames) > replayBatchCap {
		writeJSONErr(w, http.StatusBadRequest, i18n.Sf("单批帧数超上限 %d", replayBatchCap))
		return
	}
	stored, skipped, err := s.replays.Append(root, key, body.Frames)
	if err != nil {
		if err == errReplayCap {
			writeJSONErr(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("回放帧落盘失败: %s", err.Error()))
		return
	}
	writeJSON(w, map[string]any{"stored": stored, "skipped": skipped})
}

func (s *Server) handleReplayRead(w http.ResponseWriter, r *http.Request, root, key string) {
	q := r.URL.Query()
	day := replayNormDay(q.Get("date"))
	if day == "" {
		writeJSONErr(w, http.StatusBadRequest, i18n.S("date 需为 YYYYMMDD 或 YYYY-MM-DD"))
		return
	}
	dashed := day[:4] + "-" + day[4:6] + "-" + day[6:8]
	// the share-token path: a token-bearing read only passes for its
	// granted (room, day) — everything else 404s (存在性不确认).
	if tok := q.Get("t"); tok != "" {
		if !s.dayReplay.Check(tok, key, dashed) {
			http.NotFound(w, r)
			return
		}
	}
	var since int64
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSONErr(w, http.StatusBadRequest, "bad since: want a non-negative ms cursor")
			return
		}
		since = n
	}
	limit := replayDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeJSONErr(w, http.StatusBadRequest, "bad limit: want a positive count")
			return
		}
		limit = min(n, replayMaxLimit)
	}
	frames, more, next, err := s.replays.Read(root, key, day, since, limit)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("回放帧读取失败: %s", err.Error()))
		return
	}
	writeJSONZip(w, r, map[string]any{
		"project":    key,
		"date":       dashed,
		"count":      len(frames),
		"frames":     frames,
		"has_more":   more,
		"next_since": next,
	})
}

// handleReplayDays is /p/{key}/replay/days: the player's date picker's
// data face (owner only — the share page arrives with its date fixed).
func (s *Server) handleReplayDays(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	root, ok := s.replayRoot()
	if !ok {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if _, err := s.opts.Registry.Hub(key); err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{"project": key, "days": s.replays.Days(root, key)})
}

// handleReplayShare is /p/{key}/replay/share: the owner-exclusive mint/
// revoke face (confirm token 与访客通道同纪律——对外暴露面) + the read
// face for the dialog's state.
func (s *Server) handleReplayShare(w http.ResponseWriter, r *http.Request) {
	root, ok := s.replayRoot()
	if !ok {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	hub, err := s.opts.Registry.Hub(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		on, days := s.dayReplay.On(key)
		writeJSON(w, map[string]any{"on": on, "days": days})
		return
	case http.MethodPost:
		var body struct {
			Date    string `json:"date"`
			Off     bool   `json:"off"`
			Confirm string `json:"confirm"`
		}
		if err := httputil.ReadJSONBody(r, &body); err != nil {
			writeJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.Off {
			s.dayReplay.Revoke()
			if hub != nil {
				hub.System(i18n.S("房主关闭了一日回放分享（旧链接全部失效）"))
			}
			writeJSON(w, map[string]any{"on": false})
			return
		}
		day := replayNormDay(body.Date)
		if day == "" {
			writeJSONErr(w, http.StatusBadRequest, i18n.S("date 需为 YYYYMMDD 或 YYYY-MM-DD"))
			return
		}
		if body.Confirm != "REPLAY" {
			writeJSONErr(w, http.StatusBadRequest, i18n.S("分享一日回放需确认令牌 confirm=REPLAY——回放含当日对话气泡文本，请从前端导出卡的对话框发起"))
			return
		}
		// the day must have something to show (a share of an empty day
		// is a dead link minted for nothing).
		dashed := day[:4] + "-" + day[4:6] + "-" + day[6:8]
		if !dayRecorded(root, key, day) {
			writeJSONErr(w, http.StatusConflict, i18n.Sf("%s 没有录制内容——挑一个有录像的日子再分享", dashed))
			return
		}
		token := s.dayReplay.Grant(key, dashed)
		if hub != nil {
			hub.System(i18n.Sf("房主分享了 %s 的一日回放（链接已生成）", dashed))
		}
		writeJSON(w, map[string]any{
			"on":    true,
			"date":  dashed,
			"token": token,
			"path":  "/dayreplay/" + key + "?t=" + token + "&date=" + dashed,
		})
		return
	}
	writeJSONErr(w, http.StatusMethodNotAllowed, "GET/POST only")
}

// handleDayReplayPage is /dayreplay/{key}: the token-gated share page —
// same shell as the main app (the day-replay mode is a frontend init
// branch, handleView's discipline). Wrong token/day → 404 (never 403).
func (s *Server) handleDayReplayPage(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	if project == "" {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.replayRoot(); !ok {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	day := replayNormDay(q.Get("date"))
	if day == "" {
		http.NotFound(w, r)
		return
	}
	dashed := day[:4] + "-" + day[4:6] + "-" + day[6:8]
	if !s.dayReplay.Check(q.Get("t"), project, dashed) {
		http.NotFound(w, r)
		return
	}
	if s.opts.Web.WebFS != nil {
		data, err := fs.ReadFile(s.opts.Web.WebFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data)
		return
	}
	http.NotFound(w, r)
}
