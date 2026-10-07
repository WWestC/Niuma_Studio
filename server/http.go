package server

import (
	"compress/gzip"
	"fmt"
	"strconv"
	"strings"
	"time"

	"encoding/json"
	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
	"net/http"
)

// The read-only HTTP face: /members, the agent prompts, and the whole
// /kb/* projection the blackboard and the CLI read from. Handlers
// assemble; room decisions stay in the hub.

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{
		"name":    "niuma",
		"version": s.opts.Endpoint.Version,
		"ws":      s.Addr(),
		"members": s.hub.Members(),
	})
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.hub.Members())
}

// handleAgents lists the saved AI onboarding configs (summaries only;
// fetch /agents/{name}/prompt for the prompt body). Default lists the
// active ones (byte-equal to the pre-archive behavior — an unflagged
// config IS active); ?archived=1 flips to the offboarded ones (v0.8 M1).
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/agents" {
		http.NotFound(w, r)
		return
	}
	wantArchived := r.URL.Query().Get("archived") == "1"
	type summary struct {
		Name     string `json:"name"`
		Role     string `json:"role,omitempty"`
		Archived bool   `json:"archived,omitempty"`
	}
	out := []summary{}
	if s.opts.Stores.AgentStore != nil {
		for _, c := range s.opts.Stores.AgentStore.ListFiltered(wantArchived) {
			out = append(out, summary{Name: c.Name, Role: c.Role, Archived: c.Archived})
		}
	}
	writeJSON(w, out)
}

// handleAgentPrompt serves one config's prompt as plain text — the
// handoff point where an external AI picks up its instructions.
func (s *Server) handleAgentPrompt(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stores.AgentStore == nil {
		http.NotFound(w, r)
		return
	}
	cfg, ok := s.opts.Stores.AgentStore.Get(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(cfg.Prompt))
}

// handlePrompt serves the universal AI-onboarding prompt — the same
// text the AI tab displays — so it can be piped straight into an AI
// application: curl -s 127.0.0.1:<port>/prompt
func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(agents.JoinPrompt(agents.SelfExe(), s.Port)))
}

// --- blackboard knowledge base (/kb/*) ---------------------------------
//
// Read-only views over the embedded manual and the merged personnel
// roster — the same assembly the pixel UI's blackboard renders, so
// agents and humans always see the same board. (The notice page
// retired with the blackboard's editor; announcements live in the
// chat face now: GET /p/{key}/notice.)

// allRooms is the studio-wide room set the people faces read: the
// lobby first, then every live project room. Members seat into their
// own room's hub (a project dispatcher joins into that project's hub),
// so a lobby-only read would strand every project-room member offline
// on the people board while the sidebar roster — fed by the room's own
// WS — shows them present; the two faces must share one cross-room
// truth.
func (s *Server) allRooms() []kb.Room {
	rooms := []kb.Room{{Key: chat.LobbyKey, Roster: s.hub}}
	if s.opts.Registry != nil {
		for key, h := range s.opts.Registry.Rooms() {
			rooms = append(rooms, kb.Room{Key: key, Roster: h})
		}
	}
	return rooms
}

// handleKBIndex is the blackboard's table of contents.
func (s *Server) handleKBIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb" {
		http.NotFound(w, r)
		return
	}
	out := map[string]any{
		"name":         "blackboard",
		"version":      s.opts.Endpoint.Version,
		"manual_url":   "/kb/manual",
		"people_url":   "/kb/people",
		"people_count": len(kb.People(s.allRooms(), s.kbShelf(), s.opts.LocalName, s.kbLedger())),
	}
	if s.opts.Stores.Engine != nil {
		out["tasks_url"] = "/kb/tasks"
		out["task_count"] = s.opts.Stores.Engine.Count()
	}
	if s.opts.Stores.Docs != nil {
		out["docs_url"] = "/kb/docs"
		out["doc_count"] = s.opts.Stores.Docs.Count()
		out["establishment_url"] = "/kb/establishment"
	}
	writeJSON(w, out)
}

// handleKBManual serves the embedded manual as plain text. The bare
// face stays byte-identical (the full book). Two cheap param faces
// serve on-demand slices (kb/manualsec.go): ?index=1 answers the
// numbered TOC, ?q=<词> only the sections whose text contains the
// keyword — a miss answers with the TOC so the caller can retry with a
// better word instead of re-pulling the whole book.
func (s *Server) handleKBManual(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb/manual" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("index") != "" {
		_, _ = w.Write([]byte(kb.ManualTOC()))
		return
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		hits, total := kb.QueryManual(q)
		if len(hits) == 0 {
			fmt.Fprintf(w, "（手册检索「%s」未命中——以下是全部 %d 节目录，换个词再试；全文: niuma kb manual）\n\n%s\n",
				q, total, kb.ManualTOC())
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "（手册检索「%s」：命中 %d/%d 节，只回命中节——全文: niuma kb manual；目录: niuma kb manual --index）\n\n", q, len(hits), total)
		for i, h := range hits {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(h.Body)
		}
		b.WriteByte('\n')
		_, _ = w.Write([]byte(b.String()))
		return
	}
	_, _ = w.Write([]byte(kb.Manual()))
}

// handleKBPeople lists everyone the board knows: online members, saved
// AI configs (offline) and the local human, merged by exact name.
// ?project= scopes the roster to one office (the isolation read face):
// that room's seats and departures, its occupying staffing rows, and
// the host — config-derived strangers from other rooms drop off.
func (s *Server) handleKBPeople(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb/people" {
		http.NotFound(w, r)
		return
	}
	if project := s.scopeQuery(r); project != "" {
		writeJSON(w, s.scopedPeople(project))
		return
	}
	writeJSON(w, kb.People(s.allRooms(), s.kbShelf(), s.opts.LocalName, s.kbLedger()))
}

// handleKBPerson serves one person's detail (identity, saved prompt,
// latest report, rank and task ledger); 404 when neither a member nor
// a config matches. Under ?project= the person must be on the scoped
// roster — another office's member is not nameable here.
func (s *Server) handleKBPerson(w http.ResponseWriter, r *http.Request) {
	project := s.scopeQuery(r)
	if project != "" && !s.scopedPeopleHas(project, r.PathValue("name")) {
		http.NotFound(w, r)
		return
	}
	p, ok := kb.Person(s.scopedRooms(project), s.kbShelf(), s.opts.LocalName, s.kbLedger(), r.PathValue("name"))
	if !ok {
		// Scoped rosters carry on-staff names a config-less assembly
		// cannot detail (recruited, never seated, no saved prompt) —
		// the roster row degrades into the detail face rather than 404.
		if project == "" {
			http.NotFound(w, r)
			return
		}
		var summary *kb.PersonSummary
		for _, sp := range s.scopedPeople(project) {
			if sp.Name == r.PathValue("name") {
				summary = &sp
				break
			}
		}
		if summary == nil {
			http.NotFound(w, r)
			return
		}
		p = kb.PersonDetail{PersonSummary: *summary}
	}
	writeJSON(w, p)
}

// reqTitler adapts the requirements store to kb.ReqTitler (r_26: the
// history endpoint's line-title resolver; a missing store keeps
// titles empty — groups still render). Resolution is project-scoped
// (v2.10: ids are per-project shelves; the line carries its task's
// project).
type reqTitler struct{ s requirements.Store }

// ReqTitler 的实现登记处（kb/implements_test.go 指路至此）：适配器
// 签名与接口漂移，这里先红。
var _ kb.ReqTitler = reqTitler{}

func (t reqTitler) ReqTitle(projectKey, id string) string {
	if t.s == nil {
		return ""
	}
	if rq, ok := t.s.GetIn("", projectKey, id); ok {
		return rq.Title
	}
	return ""
}

// handleKBPersonHistory serves one member's delivery dossier (r_26
// t_212, 定稿 §四契约): every done task grouped by requirement line,
// per-line hash color, joined anchor. Pure read off the task ledger —
// 履历非考核：no metrics beyond the group counts ever leave this face.
func (s *Server) handleKBPersonHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	name := r.PathValue("name")
	project := s.scopeQuery(r)
	if project != "" && !s.scopedPeopleHas(project, name) {
		http.NotFound(w, r)
		return
	}
	// 名字必须在册（与 handleKBPerson 同门：成员/存档配置/本地人任一）
	if _, ok := kb.Person(s.scopedRooms(project), s.kbShelf(), s.opts.LocalName, s.kbLedger(), name); !ok {
		http.NotFound(w, r)
		return
	}
	h, ok := kb.HistoryOf(s.kbLedger(), reqTitler{s.opts.Stores.Requirements}, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSONZip(w, r, h)
}

// handleKBTasks lists the whole task ledger, newest update first,
// optionally filtered by ?assignee= and ?status= (combinable), plus
// the v2 P4-a narrows ?project= and ?version= (project=default also
// sees unattached tasks — the read-side lobby default).
func (s *Server) handleKBTasks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb/tasks" || s.opts.Stores.Engine == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	writeJSONZip(w, r, s.opts.Stores.Engine.ListFiltered(q.Get("assignee"), q.Get("status"),
		q.Get("project"), q.Get("version")))
}

// handleProjectTasks serves one project's task ledger (GET /p/{key}/tasks,
// v2 P4-a): the /kb/tasks shape — a plain []Task, newest update first —
// narrowed to the project, with ?assignee=&status=&version= refining
// further. The key resolves through the project store (unknown → 404);
// draft and archived projects still read — sealed is not invisible, and
// the lobby (default) read also sees unattached (empty project_key)
// tasks.
func (s *Server) handleProjectTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	key := r.PathValue("key")
	if s.opts.Stores.Engine == nil || s.opts.Stores.ProjectStore == nil || !s.opts.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	writeJSONZip(w, r, s.opts.Stores.Engine.ListFiltered(q.Get("assignee"), q.Get("status"), key, q.Get("version")))
}

// handleKBTask serves one task's detail with its change log; 404 when
// the id is unknown. ?project= resolves the bare id on THAT shelf
// (v2.10: ids are per-project, the scope IS the disambiguator — a
// cross-shelf id simply doesn't exist here); absent = the host face's
// studio-wide read, which only resolves numbers one shelf minted.
func (s *Server) handleKBTask(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stores.Engine == nil {
		http.NotFound(w, r)
		return
	}
	var t tasks.Task
	var ok bool
	if project := s.scopeQuery(r); project != "" {
		t, ok = s.opts.Stores.Engine.GetIn(project, r.PathValue("id"))
	} else {
		t, ok = s.opts.Stores.Engine.Get(r.PathValue("id"))
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, t)
}

// handleKBEstablishment serves the establishment reconciliation diff
// (GET /kb/establishment, v0.8 M3): the frozen six-column contract
// parsed against the live roster. A broken table is NOT an HTTP error —
// 200 with parse_error set and rows empty, so callers get a judgeable
// failure instead of a table of guesses.

// handleKBTask serves one task's detail with its change log; 404 when
// the id is unknown.

func (s *Server) handleKBEstablishment(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb/establishment" || s.opts.Stores.Docs == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, kb.Establishment(s.hub, s.kbShelf(), s.opts.Stores.Docs))
}

// handleKBDocsIndex serves the manual-docs index (GET /kb/docs): the
// store's metadata rows, most recently updated first. ?archived=1
// flips to the archive shelf; ?q= is a case-insensitive substring
// over key+title (v3 governance filters); ?project= scopes the shelf
// to one office — 公共文档库＋本房项目文档库，别家的架子不可见.
func (s *Server) handleKBDocsIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb/docs" || s.opts.Stores.Docs == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	writeJSON(w, s.opts.Stores.Docs.ListOpt(kb.ListOptions{
		Archived: q.Get("archived") == "1",
		Q:        q.Get("q"),
		Project:  s.scopeQuery(r),
	}))
}

// handleKBDocs serves one doc (GET /kb/docs/{key}) or its version
// chain (GET /kb/docs/{key}/history). Keys are one-to-three slash
// levels (roles/hr, p/proj-x/establishment — P5-a), so the subtree
// remainder is parsed by hand instead of a {key} wildcard, which only
// matches a single segment. Under ?project= a foreign shelf's key
// answers 404 — same discipline as an unknown key, never a leak.
func (s *Server) handleKBDocs(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stores.Docs == nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/kb/docs/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	key := rest
	wantHistory := false
	if strings.HasSuffix(rest, "/history") {
		key = strings.TrimSuffix(rest, "/history")
		wantHistory = true
	}
	if kb.ValidateKey(key) != nil {
		http.NotFound(w, r)
		return
	}
	if project := s.scopeQuery(r); project != "" && !kb.KeyInScope(key, project) {
		http.NotFound(w, r)
		return
	}
	if wantHistory {
		chain, err := s.opts.Stores.Docs.History(key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		out := make([]map[string]any, 0, len(chain))
		for _, h := range chain {
			out = append(out, map[string]any{
				"rev": h.Rev, "by": h.By, "ts": h.TS, "title": h.Title,
				"size": len(h.Body), // the bodies stay behind /kb/docs/{key}?rev=N
			})
		}
		writeJSON(w, out)
		return
	}
	rev := 0
	if v := r.URL.Query().Get("rev"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			http.NotFound(w, r)
			return
		}
		rev = n
	}
	doc, err := s.opts.Stores.Docs.Get(key, rev)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, doc)
}

// --- project history (/p/{key}/history, v2 P3-c) ----------------------
//
// The per-room full-history read: a linear scan over the room's
// append-only jsonl — the only full form beyond the in-memory ring of
// 100 (model §4.4, US-C4; an FTS5 index is the volume-threshold
// upgrade path, not v2). Read-only and seatless: the walk is a plain
// file read, the room keeps serving. Filters compose: q is a
// case-insensitive substring over text and speaker; since keeps only
// frames with a LATER seq (the catch-up cursor); limit caps the batch
// at the OLDEST matches first, so paging forward with next_since until
// has_more clears is lossless. tail=1 flips the batch to the NEWEST
// limit matches instead (a cold open must land at the conversation's
// end, not its head — oldest-first alone pinned the first hydration to
// the file's first page and silently hid everything newer, v2.8.1):
// messages stay oldest-first, has_more stays false (nothing sits
// beyond the newest batch; next_since is the seen-cursor as always)
// and since is refused alongside it. format=md renders the same
// filtered set as a human-readable export (without an explicit limit
// it dumps the whole filtered log). Unknown, draft and archived
// projects are 404 — the registry refuses them; so is the whole /p/
// face without a registry (the pre-P3 embedding shape).

// handleProjectReads serves one room's read-receipt ledger (飞书式已
// 读, GET): {project, since, reads:[{seq, readers}]}. since is the
// tracking floor — frames older than it predate the ledger's boot (or
// the feature); the workbench renders no chips for them. reads rows are
// oldest-first with sorted reader names; a reader is a member whose
// turn CONSUMED the line (the dispatcher flushes the read cargo at the
// turn's terminal — accepted ≠ read, chat/reads.go owns the
// semantics). queues rows carry the delivery-queue projection (排队中:
// members whose next-turn lane still holds the line — not yet injected,
// not lost; chat/queues.go), re-derived from the inbox lanes at boot.
// Unknown, draft and archived projects are 404 — the registry refuses
// them.
func (s *Server) handleProjectReads(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	since, rows := hub.ReadReceipts()
	writeJSON(w, map[string]any{"project": key, "since": since, "reads": rows, "queues": hub.QueueRows()})
}

const (
	historyDefaultLimit = 100
	historyMaxLimit     = 1000
)

func (s *Server) handleProjectHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Registry == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	// resolving through the registry is the availability check (unknown
	// / draft / archived refuse); the instantiation it may cause is
	// memory-only — a read never writes, and a room nobody dials holds
	// nothing worth persisting anyway.
	if _, err := s.opts.Registry.Hub(key); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	tail := q.Get("tail") == "1"
	var since int64
	if v := q.Get("since"); v != "" {
		if tail {
			writeJSONErr(w, http.StatusBadRequest, i18n.S("since 与 tail=1 互斥——tail 从末尾整批读，无需游标"))
			return
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSONErr(w, http.StatusBadRequest, i18n.S("since 需为非负整数（seq 游标，取上一页的 next_since）"))
			return
		}
		since = n
	}
	md := q.Get("format") == "md"
	limit := -1 // unset: JSON mode's default batch / md mode's full export
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeJSONErr(w, http.StatusBadRequest, i18n.S("limit 需为正整数"))
			return
		}
		limit = min(n, historyMaxLimit)
	} else if !md {
		limit = historyDefaultLimit
	}
	needle := strings.ToLower(q.Get("q"))

	// the file is the read model and the flusher runs behind the ring:
	// drain the room's pending lines first, or a catch-up cursor would
	// miss everything said since the last batch landed.
	s.opts.Registry.FlushHistory(key)

	var (
		frames = []chat.Message{}
		more   bool
		last   int64
	)
	match := func(m chat.Message) bool {
		if m.Seq <= since {
			return false
		}
		return needle == "" || strings.Contains(strings.ToLower(m.From+" "+m.Text), needle)
	}
	var err error
	if tail {
		// tail mode: a sliding window over the matches keeps the newest
		// `limit` of them (dropping the oldest as it overflows) — the
		// scan walks the whole file once, the window is the batch.
		// has_more stays false: nothing sits beyond the newest batch, so
		// a forward-chasing client terminates after this one page.
		err = chat.ScanHistory(s.opts.Registry.HistoryPath(key), func(m chat.Message) bool {
			if !match(m) {
				return true
			}
			if limit >= 0 && len(frames) == limit {
				frames = frames[1:]
			}
			frames = append(frames, m)
			last = m.Seq
			return true
		})
	} else {
		// one streaming pass: filter (seq cursor + substring), keep the
		// first `limit` matches (oldest-first paging), remember whether
		// anything matched beyond them.
		err = chat.ScanHistory(s.opts.Registry.HistoryPath(key), func(m chat.Message) bool {
			if !match(m) {
				return true
			}
			if limit >= 0 && len(frames) >= limit {
				more = true
				return false // the batch is full: older-first paging continues via next_since
			}
			frames = append(frames, m)
			last = m.Seq
			return true
		})
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("聊天历史读取失败: %s", err))
		return
	}

	if md {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		fmt.Fprint(w, i18n.Sf("# %s 聊天历史\n\n", key))
		for _, m := range frames {
			fmt.Fprintf(w, "- %s\n", historyMDLine(m))
		}
		return
	}
	writeJSON(w, map[string]any{
		"project":    key,
		"count":      len(frames),
		"messages":   frames,
		"has_more":   more,
		"next_since": last,
	})
}

// historyMDLine renders one frame for the markdown export: timestamp,
// speaker, text — non-chat frames (system notices, rename trails) tag
// their type instead of a speaker. Image-carrying says append the 附图
// count (the bytes stay behind GET /media/{id}, the export names them).
func historyMDLine(m chat.Message) string {
	ts := ""
	if m.TS > 0 {
		ts = time.Unix(m.TS, 0).Format("2006-01-02 15:04:05")
	}
	imgTag := ""
	if len(m.Images) > 0 {
		imgTag = i18n.Sf("（附图 %d 张：%s）", len(m.Images), imageNames(m))
	}
	switch m.Type {
	case chat.MsgSay, chat.MsgReport:
		return fmt.Sprintf("%s **%s**：%s%s", ts, m.From, m.Text, imgTag)
	case chat.MsgRename:
		return fmt.Sprintf("%s `%s` %s", ts, m.Type, m.Text)
	default:
		return fmt.Sprintf("%s `%s` %s", ts, m.Type, m.Text)
	}
}

// imageNames joins the say's image filenames (id fallback) for exports.
func imageNames(m chat.Message) string {
	names := make([]string, 0, len(m.Images))
	for _, im := range m.Images {
		if im.Name != "" {
			names = append(names, im.Name)
		} else {
			names = append(names, im.ID)
		}
	}
	return strings.Join(names, "、")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONErr is writeJSON's error twin: an HTTP status with a JSON
// {"error": "..."} body, so read faces can fail with a reason the web
// panels render instead of a bare status line. The reason rides through
// i18n.S — static Chinese reasons are dictionary keys, so the whole
// error family flips language with the UI setting without per-call
// edits (composed reasons migrate at their call sites with Sf).
func writeJSONErr(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": i18n.S(reason)})
}

// gzipMinSize is the wire-compression floor: below it gzip's dictionary
// and trailer eat the bytes it saves — small answers ride plain.
const gzipMinSize = 1024

// acceptsGzip reports whether the client advertised gzip in
// Accept-Encoding (WKWebView 和浏览器都带；不带的客户端拿明文).
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "gzip" {
			return true
		}
	}
	return false
}

// writeJSONZip is writeJSON's heavy-read-face twin: byte-identical JSON
// (Encoder 的行尾换行保留), gzipped when the client accepts it and the
// body clears gzipMinSize — /kb/tasks 一拍 190KB、驾驶舱 20s 一撞，
// 压完 ~30KB，局域网开面板的速度就是这一刀省出来的。在线压缩（这些
// 面每分钟至多几发，sync.Pool 不值当）；gzip 路径不设 Content-Length
// （压后尺寸写完才知道，chunked 走起），304/ETag 语义不涉足。
func writeJSONZip(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("序列化失败：%s", err.Error()))
		return
	}
	body = append(body, '\n') // json.Encoder 的行尾换行，明文/密文同一形状
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if !acceptsGzip(r) || len(body) < gzipMinSize {
		_, _ = w.Write(body)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Set("Vary", "Accept-Encoding")
	gz := gzip.NewWriter(w)
	_, _ = gz.Write(body)
	_ = gz.Close()
}

// --- WebSocket ---------------------------------------------------------
