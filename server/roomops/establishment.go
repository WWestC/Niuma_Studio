package roomops

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/gitops"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/util"
)

// Face is the room-lifecycle domain's whole world: the seat (stores/
// fleet/routing — cross-domain), the replay journal's narrow face
// (purge/retention), the rebuild busy latch, the retention sweep's
// stop channel and the plan-room resolver. Split from the shell's
// establishment/offboard/purge/kick/rank/watch/pause/resume/rebuild/
// retention/archive/project_ops twelve-pack.
type Face struct {
	*verbs.Seat
	Replays       Replays
	RebuildBusy   *atomic.Bool
	retentionStop chan struct{}
	PlanHub       func(projectKey string) *chat.Hub
	// GitBootstrap is the git domain's project-bootstrap seam (sibling
	// domain, injected by the shell — the family's one cross-domain tie).
	GitBootstrap func(p projects.Project) gitops.BootResult
	// SnapshotTo persists one hub's seat snapshot at path (the shell's
	// port.go owns the format; lifecycle ops borrow the writer).
	SnapshotTo func(path string, h *chat.Hub)
	// Chronicle is the 工作室志's narrow face this domain taps.
	Chronicle Chronicle
	// RebuildCh is the recompile-restart signal line (Options.RebuildCh,
	// nil = the endpoint 404s — embeds/tests).
	RebuildCh chan struct{}
	// ResetCh is the factory-reset signal line (Options.ResetCh).
	ResetCh chan struct{}
}

// Replays is the 一日回放 journal's narrow face this domain drinks
// (prune on the retention sweep, forget on wipe).
type Replays interface {
	Prune(root, oldestDay string) int
	Forget(key string)
}

// FaceOf adapts a seat plus the shell's replay/plan-hub seams.
func FaceOf(st *verbs.Seat, replays Replays, planHub func(string) *chat.Hub) *Face {
	return &Face{Seat: st, Replays: replays, PlanHub: planHub, RebuildBusy: new(atomic.Bool)}
}

// KbShelf adapts the agents store to kb's read face, keeping a
// typed-nil store reading as a nil interface (kb's nil-tolerance
// must survive the interface boundary).
func (rc *Face) KbShelf() kb.ConfigShelf {
	if rc.Stores.AgentStore == nil {
		return nil
	}
	return rc.Stores.AgentStore
}

// KbDiffFor renders one successful write's old→new rows for the kb
// broadcast frame (黑板红绿对比) — moved beside its consumer.
func (rc *Face) KbDiffFor(store *kb.DocsStore, meta kb.DocMeta) ([]chat.DiffLine, int) {
	if store == nil {
		return nil, 0
	}
	old, cur, err := store.DiffAround(meta.Key, meta.Rev)
	if err != nil {
		return nil, 0
	}
	return chat.LineDiff(old, cur)
}

// Chronicle is the 工作室志's narrow face (the rebuild commit line).
type Chronicle interface {
	OnRebuildCommit()
}

// StartRetention boots the retention sweep loop (the shell calls it at
// Start when a registry root exists; Close via the returned channel).
func (rc *Face) StartRetention(every time.Duration) chan struct{} {
	rc.retentionStop = make(chan struct{})
	go rc.retentionLoop(every)
	return rc.retentionStop
}

// handleProjectEstablishment serves the project's establishment diff
// (GET) and flips the staffing auto_recall switch (POST
// {auto_recall:bool}). Unknown, draft and archived projects are 404
// (the /p/{key}/staffing rule); the lobby reads ops/establishment.
func (rc *Face) HandleProjectEstablishment(w http.ResponseWriter, r *http.Request) {
	if rc.Stores.ProjectStore == nil || rc.Stores.Docs == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	p, ok := rc.Stores.ProjectStore.Get(key)
	if !ok || p.Status != projects.StatusActive {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		report, _ := rc.projectEstablishment(key)
		autoRecall := true
		if rc.Stores.StaffStore != nil {
			autoRecall = rc.Stores.StaffStore.SettingsOf(key).AutoRecall
		}
		httputil.WriteJSON(w, struct {
			Project    string                `json:"project"`
			AutoRecall bool                  `json:"auto_recall"`
			Rows       []kb.EstablishmentRow `json:"rows"`
			ParseError string                `json:"parse_error"`
			Rev        int                   `json:"rev"`
		}{Project: key, AutoRecall: autoRecall, Rows: report.Rows, ParseError: report.ParseError, Rev: report.Rev})
	case http.MethodPost:
		if rc.Stores.StaffStore == nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("编制域未启用（无 staffing 存储）"))
			return
		}
		var body struct {
			AutoRecall *bool `json:"auto_recall"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil || body.AutoRecall == nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S(`body 须为 {"auto_recall": true|false}`))
			return
		}
		set := rc.Stores.StaffStore.SetAutoRecall(key, *body.AutoRecall)
		httputil.WriteJSON(w, struct {
			Project    string `json:"project"`
			AutoRecall bool   `json:"auto_recall"`
		}{Project: key, AutoRecall: set.AutoRecall})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}

// handleKBEstablishmentRow serves the lobby's row face (POST
// /kb/establishment/row): the CRUD core over ops/establishment, the
// lobby's own table.
func (rc *Face) HandleKBEstablishmentRow(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/kb/establishment/row" {
		http.NotFound(w, r)
		return
	}
	rc.establishmentRowOp(w, r, chat.LobbyKey)
}

// handleProjectEstablishmentRow serves one project's row face (POST
// /p/{key}/establishment/row): the same CRUD core over
// p/<key>/establishment, active projects only (the GET's 404 rule).
func (rc *Face) HandleProjectEstablishmentRow(w http.ResponseWriter, r *http.Request) {
	if rc.Stores.ProjectStore == nil || rc.Stores.Docs == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if p, ok := rc.Stores.ProjectStore.Get(key); !ok || p.Status != projects.StatusActive {
		http.NotFound(w, r)
		return
	}
	rc.establishmentRowOp(w, r, key)
}

// estRowRequest is the row face's body: op names the verb, key the
// target row's 岗位 key (the identity an update may not change), row
// the new fields (add/update only), expect_rev the optimistic anchor
// — the rev the caller's GET read (0 = the table must not exist yet,
// the add-creates-doc case only).
type estRowRequest struct {
	Op        string               `json:"op"`
	Key       string               `json:"key"`
	Row       *kb.EstablishmentRow `json:"row"`
	ExpectRev int                  `json:"expect_rev"`
}

// establishmentRowOp runs one row mutation against the scope's table
// and answers with the scope's fresh report (rows + rev — the caller
// re-renders from it, never from its own echo).
func (rc *Face) establishmentRowOp(w http.ResponseWriter, r *http.Request, projectKey string) {
	if rc.Stores.Docs == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req estRowRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	by := rc.Local
	if by == "" {
		by = "房主"
	}
	docKey := kb.EstablishmentDocKey(projectKey)
	doc, err := rc.Stores.Docs.Get(docKey, 0)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("编制表读取失败：%s", err.Error()))
		return
	}

	// added names the hiring event a successful write leaves behind (a
	// new row, or an update that raises headcount — both are 加编);
	// prev is the row's former headcount, 0 for the brand-new row.
	var added *kb.EstablishmentRow
	var prevHead int

	switch req.Op {
	case "add":
		if req.Row == nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("add 需要 row 字段"))
			return
		}
		if err := kb.ValidatePostRow(*req.Row); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if !missing {
			rows, perr := kb.EstablishmentRows(doc.Body)
			if perr != "" {
				httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("编制表损坏，拒绝改行：%s", perr))
				return
			}
			for _, x := range rows {
				if x.Key == req.Row.Key {
					httputil.WriteJSONErr(w, http.StatusConflict, i18n.Sf("岗位 key「%s」已在编制表中，不可重复添加", x.Key))
					return
				}
			}
		}
		// the append rides EnsureEstablishmentPost's own bounded-retry
		// optimistic lock (it also creates a deleted table whole).
		if _, err := kb.EnsureEstablishmentPost(rc.Stores.Docs, docKey,
			req.Row.Key, req.Row.Name, req.Row.Role, req.Row.Headcount,
			req.Row.AutoFill, req.Row.Manual, false, by); err != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("岗位行写入未成：%s", err.Error()))
			return
		}
		row := *req.Row
		added, prevHead = &row, 0
	case "update":
		if req.Row == nil || req.Key == "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("update 需要 key 与 row 字段"))
			return
		}
		if missing {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("编制表不存在，无可更新行"))
			return
		}
		if kb.SystemPostKey(req.Key) {
			httputil.WriteJSONErr(w, http.StatusForbidden,
				i18n.Sf("「%s」是系统岗（必须）——由系统自动招募维护，界面与接口均不可修改（Niuma_Studio 随启动招聘；项目随开张自动招聘，每项目各一套）", req.Key))
			return
		}
		if req.Row.Key != "" && req.Row.Key != req.Key {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("岗位 key 是行的身份，不可修改（如需换 key：删行重加）"))
			return
		}
		next := *req.Row
		next.Key = req.Key
		if err := kb.ValidatePostRow(next); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		rows, perr := kb.EstablishmentRows(doc.Body)
		if perr != "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("编制表损坏，拒绝改行：%s", perr))
			return
		}
		hit := -1
		for i, x := range rows {
			if x.Key == req.Key {
				hit = i
				break
			}
		}
		if hit < 0 {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("岗位 key「%s」不在编制表中", req.Key))
			return
		}
		prevHead = rows[hit].Headcount
		rows[hit] = next
		if werr := rc.rewriteEstablishment(doc, rows, req.ExpectRev, by); werr != nil {
			rc.answerRowWriteErr(w, werr)
			return
		}
		if next.Headcount > prevHead { // 扩编也是加编：缺的人是新的招聘义务
			landed := next
			added = &landed
		}
	case "delete":
		if req.Key == "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("delete 需要 key 字段"))
			return
		}
		if missing {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("编制表不存在，无可删除行"))
			return
		}
		if kb.SystemPostKey(req.Key) {
			httputil.WriteJSONErr(w, http.StatusForbidden,
				i18n.Sf("「%s」是系统岗（必须）——由系统自动招募维护，不可删除（Niuma_Studio 随启动招聘；项目随开张自动招聘）", req.Key))
			return
		}
		rows, perr := kb.EstablishmentRows(doc.Body)
		if perr != "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("编制表损坏，拒绝改行：%s", perr))
			return
		}
		out := rows[:0]
		hit := false
		for _, x := range rows {
			if x.Key == req.Key {
				hit = true
				continue
			}
			out = append(out, x)
		}
		if !hit {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.Sf("岗位 key「%s」不在编制表中", req.Key))
			return
		}
		if werr := rc.rewriteEstablishment(doc, out, req.ExpectRev, by); werr != nil {
			rc.answerRowWriteErr(w, werr)
			return
		}
	default:
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("op 须为 add | update | delete"))
		return
	}

	rc.broadcastEstablishmentWrite(projectKey, docKey, by)
	if added != nil {
		rc.notifyEstablishmentAdded(projectKey, *added, prevHead, by)
	}
	report := rc.scopeEstablishment(projectKey)
	httputil.WriteJSON(w, report)
}

// notifyEstablishmentAdded is the add's hiring leg. The recorded room
// line is the dialogue hint — the panel write is host-only knowledge,
// the room hears the [编制] trace the way it hears the keeper's — and
// the Options tap wakes HR off the HTTP path (planLandedNotify's
// contract: a member's cold-session reactivation must not stall the
// row face's answer). A tap failure is one more ambient note telling
// the host to @HR themselves; the row is already written.
func (rc *Face) notifyEstablishmentAdded(projectKey string, row kb.EstablishmentRow, prev int, by string) {
	hub := rc.Hub
	if projectKey != chat.LobbyKey && rc.Registry != nil {
		hub = rc.Registry.Rooms()[projectKey]
	}
	if hub != nil {
		hub.SystemRecorded(establishmentAddedLine(projectKey, row, prev, by))
	}
	if rc.Fleet == nil {
		return
	}
	go util.Guard("server: fleet notify", func() {
		if err := rc.Fleet.EstablishmentAdded(projectKey, row, prev, by); err != nil {
			log.Printf("[编制] %s 加编后提醒 HR 失败：%v", projectKey, err)
			if hub != nil {
				hub.System(i18n.Sf("[编制] 加编提醒未送达 HR（人事）：%v——请在对话里 @HR 跟进招聘", err))
			}
		}
	})
}

// establishmentAddedLine renders the scope room's recorded 加编 hint:
// who added what, in full six-column fidelity (the same fields HR's
// notice carries), and where the hiring duty went.
func establishmentAddedLine(projectKey string, row kb.EstablishmentRow, prev int, by string) string {
	scope := i18n.S("Niuma_Studio")
	if projectKey != chat.LobbyKey {
		scope = i18n.Sf("项目 %s", projectKey)
	}
	auto := i18n.S("否")
	if row.AutoFill {
		auto = i18n.S("是")
	}
	if prev <= 0 {
		return i18n.Sf("[编制] %s 在%s加了岗位行：%s（key %s，身份「%s」，编制 %d，自动补员 %s）——已提醒 HR（人事）按手册跟进招聘",
			by, scope, row.Name, row.Key, row.Role, row.Headcount, auto)
	}
	return i18n.Sf("[编制] %s 扩了%s的编：%s（key %s）编制 %d → %d——已提醒 HR（人事）跟进补员",
		by, scope, row.Name, row.Key, prev, row.Headcount)
}

// rewriteEstablishment rewrites the doc from rows under the caller's
// optimistic anchor.
func (rc *Face) rewriteEstablishment(doc kb.Doc, rows []kb.EstablishmentRow, expectRev int, by string) error {
	body, ok := kb.RebuildEstablishmentBody(doc.Body, rows)
	if !ok {
		return errors.New(i18n.S("编制表未找到冻结表头行，拒绝改行"))
	}
	if expectRev <= 0 {
		return errors.New(i18n.S("缺少 expect_rev（以 GET 拿到的 rev 为准；表已被他人改动时服务端会回 409 与最新 rev）"))
	}
	_, err := rc.Stores.Docs.WriteExpect(doc.Key, doc.Title, body, by, expectRev)
	return err
}

// answerRowWriteErr maps a rewrite failure to the HTTP shape: a rev
// conflict is a 409 carrying current_rev (re-read, merge, retry); the
// validation refuses are 400s; anything else is a 500 with the reason.
func (rc *Face) answerRowWriteErr(w http.ResponseWriter, err error) {
	var rce *kb.RevConflictError
	if errors.As(err, &rce) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "current_rev": rce.CurrentRev})
		return
	}
	httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
}

// broadcastEstablishmentWrite tells the scope's room the doc moved —
// the same reminder-only kb event kbOp broadcasts (diff rows included,
// same kbDiffFor), so every open workbench (and the mirror) hears the
// table changed.
func (rc *Face) broadcastEstablishmentWrite(projectKey, docKey, by string) {
	hub := rc.Hub
	if projectKey != chat.LobbyKey && rc.Registry != nil {
		hub = rc.Registry.Rooms()[projectKey]
	}
	if hub == nil {
		return
	}
	doc, err := rc.Stores.Docs.Get(docKey, 0)
	if err != nil {
		return
	}
	diff, more := rc.KbDiffFor(rc.Stores.Docs, doc.DocMeta)
	hub.Broadcast(chat.Message{Type: chat.MsgKb, Event: "written",
		KbDoc: &chat.KbDoc{Key: doc.Key, Title: doc.Title, Owner: doc.Owner,
			UpdatedBy: doc.UpdatedBy, UpdatedTS: doc.UpdatedTS, Rev: doc.Rev, Size: doc.Size},
		Diff: diff, DiffMore: more,
		From: by, TS: time.Now().Unix()})
}

// scopeEstablishment answers the scope's fresh report: the lobby reads
// kb.Establishment (roster ∪ configs), a project its own staffing view.
func (rc *Face) scopeEstablishment(projectKey string) kb.EstablishmentReport {
	if projectKey == chat.LobbyKey {
		return kb.Establishment(rc.Hub, rc.KbShelf(), rc.Stores.Docs)
	}
	report, _ := rc.projectEstablishment(projectKey)
	return report
}

// projectEstablishment reconciles one project's establishment table
// against its staffing rows and room roster (the report view — present
// counts an offline occupying row; the keeper's stricter
// live-presence view lives in watch.go). The lobby reads
// ops/establishment; a project reads p/<key>/establishment. A missing
// project table still answers an EMPTY report (not a parse error) —
// v2.5 起编制是必须面（开张即播种、启动即收敛），缺表只在播种腿失手
// 或房主硬删文档后出现，GET 仍须答开关与形状，看护下一拍照常报警。
func (rc *Face) projectEstablishment(key string) (kb.EstablishmentReport, bool) {
	docKey := establishDocKey(key)
	doc, err := rc.Stores.Docs.Get(docKey, 0)
	if err != nil {
		if key == chat.LobbyKey {
			return kb.EstablishmentReport{ParseError: i18n.S("编制表文档不可读（用 kb write ops/establishment 重建，表头需为冻结六列）")}, false
		}
		return kb.EstablishmentReport{Rows: []kb.EstablishmentRow{}}, false
	}
	parsed, perr := kb.EstablishmentRows(doc.Body)
	if perr != "" {
		return kb.EstablishmentReport{ParseError: perr}, false
	}

	// roleHolders: room roster first (live ∪ grace), then occupying
	// staffing rows whose seat role matches — ordered, deduped.
	roleHolders := map[string][]string{}
	seen := map[string]bool{}
	var hub *chat.Hub
	if key == chat.LobbyKey || rc.Registry == nil {
		hub = rc.Hub
	} else {
		hub = rc.Registry.Rooms()[key] // nil = no room, staffing rows only
	}
	if hub != nil {
		for _, m := range hub.Members() {
			if m.Role != "" && !seen[m.Name] {
				seen[m.Name] = true
				roleHolders[m.Role] = append(roleHolders[m.Role], m.Name)
			}
		}
	}
	if rc.Stores.StaffStore != nil {
		for _, row := range rc.Stores.StaffStore.ListByProject(key) {
			if !row.Occupying() || seen[row.Person] {
				continue
			}
			role := row.ProjectRole
			if role == "" && rc.Stores.AgentStore != nil {
				if cfg, ok := rc.Stores.AgentStore.Get(row.Person); ok {
					role = cfg.Role
				}
			}
			if role == "" {
				continue
			}
			seen[row.Person] = true
			roleHolders[role] = append(roleHolders[role], row.Person)
		}
	}

	rows := make([]kb.EstablishmentRow, 0, len(parsed))
	rows = append(rows, parsed...)
	kb.ReconcileRows(rows, roleHolders)
	kb.OrderReportRows(rows) // 展示序：小助手最上、系统岗在前（文档行序不动）
	return kb.EstablishmentReport{Rows: rows, Rev: doc.Rev}, true
}
