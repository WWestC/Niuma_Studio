package server

// term.go — 终端转录的读面（r_17）：
//
//   - GET /p/{key}/term[?name=成员&tail=N] — 转录册快照（chat/term.go 的
//     TermSnapshot：文件尾行按 seq upsert 进内存镜像，各成员旧在前的
//     折叠终态条目）＋名册上的干活中戳。tail 缺省 500、上限 2000
//     （终端板的冷窗水合；直播增量走 "term" 帧）。?name= 收窄到一人。
//     未知/draft/归档项目 404——/p face 的规则，trace 同款。
//   - GET /p/{key}/term/file?name=成员 — 整册 jsonl 下载（房主本地留
//     档）。名字经 chat 的叶子折叠后拼路径、Base 复核，出界一律 404；
//     无 root（内存态）整面 404——/retention 的纪律。

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"

	"github.com/WWestC/Niuma_Studio/chat"
)

// termTailLimits: the hydration tail's default and ceiling (the board
// renders the recent stream and offers the download for the whole
// journal — a cold open has no business hauling megabytes).
const (
	termTailDefault = 500
	termTailMax     = 2000
)

// handleProjectTerm serves the room's 终端转录 snapshot:
//
//	{"project": key, "members": [
//	   {"from": 名字, "working": bool, "entries": [TraceEntry…]},
//	]}
//
// Rows follow roster order (the switcher's face), then departed
// members the journal still knows (换房/退场后旧册照出——终端是史册，
// 不是名册的镜子)。
func (s *Server) handleProjectTerm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Registry == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	hub, err := s.opts.Registry.Hub(key)
	if err != nil {
		writeJSONErr(w, http.StatusNotFound, err.Error())
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	tail := termTailDefault
	if v := strings.TrimSpace(r.URL.Query().Get("tail")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	if tail > termTailMax {
		tail = termTailMax
	}
	snapshot := hub.TermSnapshot(name, tail)
	working := map[string]bool{}
	for _, m := range hub.Members() {
		working[m.Name] = m.Working
	}
	rows := []map[string]any{}
	seen := map[string]bool{}
	for _, m := range hub.Members() {
		if name != "" && m.Name != name {
			continue
		}
		entries := snapshot[m.Name]
		if entries == nil {
			entries = []chat.TraceEntry{}
		}
		seen[m.Name] = true
		rows = append(rows, map[string]any{
			"from": m.Name, "working": working[m.Name], "entries": entries,
		})
	}
	// 退场/换房成员的存量册照出（名册上没有名字也有文件）。
	for who, entries := range snapshot {
		if seen[who] {
			continue
		}
		rows = append(rows, map[string]any{"from": who, "working": false, "entries": entries})
	}
	if name != "" && len(rows) == 0 {
		// 聚焦查询落空也给一行空册（面板好画「还没有记录」态）。
		rows = append(rows, map[string]any{
			"from": name, "working": false, "entries": []chat.TraceEntry{},
		})
	}
	writeJSON(w, map[string]any{"project": key, "members": rows})
}

// handleProjectTermFile serves one member's whole transcript journal as
// a download (GET /p/{key}/term/file?name=). The name folds through
// chat's leaf discipline and the result must stay the dir's direct
// child — anything else is a 404, not an error page.
func (s *Server) handleProjectTermFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	reg := s.opts.Registry
	if reg == nil || reg.Root() == "" {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSONErr(w, http.StatusBadRequest, i18n.S("缺少 name"))
		return
	}
	dir := reg.TermDir(key)
	leaf := chat.TermFileLeaf(name)
	path := filepath.Join(dir, leaf)
	if filepath.Dir(path) != dir || filepath.Base(path) != leaf {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+leaf+`"`)
	http.ServeContent(w, r, leaf, time.Time{}, f)
}
