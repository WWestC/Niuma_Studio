package roomops

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/tasks"
	"github.com/WWestC/Niuma_Studio/util"
)

// OffboardStep is one ✓/✗/△ line of the step log (the CLI renders it
// verbatim; skip=true is △ — recorded, not an error).
type OffboardStep struct {
	Step string `json:"step"`
	OK   bool   `json:"ok"`
	Skip bool   `json:"skip,omitempty"`
	Note string `json:"note,omitempty"`
}

// offboardBlockedTask is one unhandled in-progress task of the block
// receipt.
type offboardBlockedTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
}

// offboardBlock is the ① interception body.
type offboardBlock struct {
	Reason string                `json:"reason"`
	Tasks  []offboardBlockedTask `json:"tasks"`
}

// OffboardResponse is the endpoint's JSON face.
type OffboardResponse struct {
	OK      bool           `json:"ok"`
	Steps   []OffboardStep `json:"steps"`
	Blocked *offboardBlock `json:"blocked,omitempty"`
	Note    string         `json:"note,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// handleOffboard runs the departure pipeline: POST /offboard with
// {name, project?, as?, reason?, reassign_to?, force?, dry_run?}.
func (rc *Face) HandleOffboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/offboard" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name       string `json:"name"`
		Project    string `json:"project"`
		As         string `json:"as"`
		Reason     string `json:"reason"`
		ReassignTo string `json:"reassign_to"`
		Force      bool   `json:"force"`
		DryRun     bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("name 必填（例：{\"name\":\"小明\",\"reassign_to\":\"小红\"}）"))
		return
	}
	if body.ReassignTo != "" && body.ReassignTo == name {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("reassign_to 不能是离职者本人"))
		return
	}
	actor := strings.TrimSpace(body.As)
	if actor == "" {
		actor = rc.Local
	}
	projectKey := body.Project
	if projectKey == "" {
		projectKey = chat.LobbyKey
	}

	// The project's room: the lobby is the server's own hub; any other
	// key resolves (and instantiates) through the registry — unknown,
	// draft and archived projects refuse here.
	hub := rc.Hub
	if projectKey != chat.LobbyKey {
		if rc.Registry == nil {
			httputil.WriteJSONErr(w, http.StatusNotFound, i18n.S("项目面未启用（无办公室登记表）——不带 project 即 Niuma_Studio 范围"))
			return
		}
		ph, err := rc.Registry.Hub(projectKey)
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusNotFound, err.Error())
			return
		}
		hub = ph
	}

	res := OffboardResponse{Steps: []OffboardStep{}}
	step := func(label string, state rune, note string) {
		res.Steps = append(res.Steps, OffboardStep{Step: label, OK: state == '✓', Skip: state == '△', Note: note})
	}
	fail := func(status int, errText string) {
		res.OK = false
		res.Error = errText
		writeOffboard(w, status, res)
	}

	// ① preflight: the project's in-progress tasks, archive state,
	// online presence and the departing role.
	if rc.Stores.Engine == nil {
		fail(http.StatusBadRequest, i18n.S("任务系统未启用——离职预检无从谈起"))
		return
	}
	var inprog []tasks.Task
	for _, t := range rc.Stores.Engine.ListFiltered(name, "", projectKey, "") {
		if t.Status == tasks.StatusDoing || t.Pending != nil {
			inprog = append(inprog, t)
		}
	}
	archived := false
	if rc.Stores.AgentStore != nil {
		if cfg, ok := rc.Stores.AgentStore.Get(name); ok {
			archived = cfg.Archived
		}
	}
	online := false
	for _, m := range hub.Members() {
		if m.Name == name {
			online = true
			break
		}
	}
	role := rc.roleIn(projectKey, name)
	step(i18n.Sf("预检（在办 %d 件 · 归档 %s · 在线 %s）", len(inprog), boolZH(archived), boolZH(online)), '✓', "")

	if archived && !online {
		res.OK = true
		res.Note = i18n.S("该成员已离岗归档且不在线，无需重复执行")
		writeOffboard(w, http.StatusOK, res)
		return
	}
	if len(inprog) > 0 && body.ReassignTo == "" && !body.Force {
		block := &offboardBlock{
			Reason: i18n.S("目标名下有在办任务，需 reassign_to 交接人或 force 带任务硬离"),
			Tasks:  make([]offboardBlockedTask, 0, len(inprog)),
		}
		for _, t := range inprog {
			label := "[" + tasks.StatusLabel(t.Status) + "]"
			if t.Pending != nil {
				label = "[" + i18n.S("待确认") + "]"
			}
			block.Tasks = append(block.Tasks, offboardBlockedTask{ID: t.ID, Status: label, Title: t.Title})
		}
		res.OK = false
		res.Blocked = block
		writeOffboard(w, http.StatusConflict, res)
		return
	}

	if body.DryRun {
		res.OK = true
		res.Note = rc.offboardDryRunNote(actor, name, role, projectKey, inprog, body.ReassignTo)
		writeOffboard(w, http.StatusOK, res)
		return
	}

	// ② reassign (facts first), then the notice speaks about them.
	for _, t := range inprog {
		if body.ReassignTo == "" {
			continue
		}
		out := rc.Stores.Engine.Update(t.ProjectKey, actor, t.ID, tasks.Patch{Assignee: body.ReassignTo})
		if out.Denied {
			step(i18n.Sf("改派 %s", t.ID), '✗', out.Reason)
			continue
		}
		snap := out.Task
		hub.Broadcast(chat.Message{Type: chat.MsgTask, Event: out.Event,
			Task: snap.Wire(), From: actor, Text: out.Text, TS: util.Now()})
		step(i18n.Sf("改派 %s → %s", t.ID, body.ReassignTo), '✓', "")
	}
	notice := i18n.Sf("%s 今日离岗%s。", name, reasonClause(body.Reason))
	switch {
	case body.ReassignTo != "":
		notice += i18n.Sf("在办 %d 件已改派 @%s", len(inprog), body.ReassignTo)
	case len(inprog) > 0:
		notice += i18n.Sf("有 %d 件在办任务未改派，保持原指派", len(inprog))
	}
	if said := rc.offboardNotice(hub, actor, notice); said {
		step(i18n.S("离岗通告 say"), '✓', i18n.S("已进历史")+reassignSuffix(body.ReassignTo))
	} else {
		step(i18n.S("离岗通告（系统留痕）"), '✓', i18n.S("已进历史")+reassignSuffix(body.ReassignTo))
	}

	// ③ kick out of the project room; not-in-room is the pure archive
	// path (△), any other refusal stops the pipeline with ①② standing.
	kickReceipt := rc.kickIn(hub, actor, name)
	switch {
	case kickReceipt == i18n.Sf("已将 %s 移出办公室", name):
		step(i18n.S("移出 kick"), '✓', "")
	case strings.Contains(kickReceipt, i18n.S("不在办公室")):
		step(i18n.S("移出 kick"), '△', i18n.S("目标不在办公室，跳过（纯归档路径）"))
	default:
		fail(http.StatusBadRequest, kickReceipt+i18n.S("（步骤①②已执行，③④⑤未执行）"))
		return
	}

	// ④ the staffing row: offboard + left_ts (the scope's own row).
	if rc.Stores.StaffStore != nil {
		if row, err := rc.Stores.StaffStore.Offboard(projectKey, name); err != nil {
			switch {
			// staffing 错误未迁移（恒中文），针脚保持中文才能匹配
			case strings.Contains(err.Error(), "已离编"):
				step(i18n.S("编制离编 staff offboard"), '△', i18n.S("该项目编制行已离编"))
			case strings.Contains(err.Error(), "编制行不存在"):
				step(i18n.S("编制离编 staff offboard"), '△', i18n.S("无编制行（未入编，v1 形态）"))
			default:
				fail(http.StatusBadRequest, i18n.Sf("编制离编失败：%v（步骤①②③已执行，④⑤未执行）", err))
				return
			}
		} else {
			step(i18n.S("编制离编 staff offboard"), '✓', fmt.Sprintf("left_ts=%d", row.LeftTS))
		}
	}

	// ④b the GLOBAL profile archive — only when every staffing row of
	// the person is offboard (model §3.2). A row still occupying
	// another project keeps the profile alive (借调/多项目中间态).
	if blocker, occupying := rc.offboardArchiveBlocker(name); occupying {
		step(i18n.S("归档 agent_archive"), '△', i18n.Sf("仍在项目 %s 编制，档案保留（全局归档需所有项目均已离编）", blocker))
	} else if receipt := rc.ArchiveAs(actor, name); strings.HasPrefix(receipt, i18n.S("agent_archive 被拒")) {
		if strings.Contains(receipt, i18n.S("档案不存在")) {
			step(i18n.S("归档 agent_archive"), '△', i18n.S("无档案（纯座位清理路径）"))
		} else {
			fail(http.StatusBadRequest, receipt+i18n.S("（步骤①②③④已执行，⑤未执行）"))
			return
		}
	} else {
		step(i18n.S("归档 agent_archive"), '✓', "")
	}

	// ⑤ the room-log ledger line (the record: fail loudly, earlier
	// steps stand) + the establishment table's auto-fill hint. A
	// project's ledger materializes on its first departure line (the
	// lobby's ops/room-log is a seeded doc; per-project p/<key>/
	// room-log docs are born here rather than by hand ceremony).
	ledgerKey := "ops/room-log"
	if projectKey != chat.LobbyKey {
		ledgerKey = "p/" + projectKey + "/room-log"
	}
	line := fmt.Sprintf("%s 已离岗（岗位 %s，操作者 %s，%s%s）", name, role, actor,
		reasonClauseLedger(body.Reason), time.Now().Format("2006-01-02"))
	if rc.Stores.Docs == nil {
		fail(http.StatusBadRequest, i18n.S("办公室记忆未启用，台账无处落笔（步骤①–④已执行）"))
		return
	}
	var meta kb.DocMeta
	var err error
	if _, gerr := rc.Stores.Docs.Get(ledgerKey, 0); gerr != nil {
		meta, err = rc.Stores.Docs.Write(ledgerKey, "离岗台账", line, actor)
	} else {
		// AppendRollover（v3）：台账超 32KB 自动分卷，不再有静默截断的悬崖
		meta, err = rc.Stores.Docs.AppendRollover(ledgerKey, line, actor, 0)
	}
	if err != nil {
		fail(http.StatusBadRequest, i18n.Sf("台账写入被拒: %v（步骤①–④已执行）", err))
		return
	}
	diff, more := rc.KbDiffFor(rc.Stores.Docs, meta)
	hub.Broadcast(chat.Message{Type: chat.MsgKb, Event: "appended",
		KbDoc: &chat.KbDoc{Key: meta.Key, Title: meta.Title, Owner: meta.Owner,
			UpdatedBy: meta.UpdatedBy, UpdatedTS: meta.UpdatedTS, Rev: meta.Rev, Size: meta.Size},
		Diff: diff, DiffMore: more,
		From: actor, TS: util.Now()})
	step(i18n.S("台账 room-log 已离岗"), '✓', ledgerKey)

	res.OK = true
	res.Note = rc.autoFillHint(projectKey, role)
	writeOffboard(w, http.StatusOK, res)
}

// writeOffboard emits the JSON face with the given status.
func writeOffboard(w http.ResponseWriter, status int, res OffboardResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}

// roleIn resolves the departing member's post label for the notice and
// the ledger: the staffing row's project role first (the project's own
// name for the hat), the global profile's role, else —.
func (rc *Face) roleIn(projectKey, name string) string {
	if rc.Stores.StaffStore != nil {
		if row, ok := rc.Stores.StaffStore.Get(projectKey, name); ok && row.ProjectRole != "" {
			return row.ProjectRole
		}
	}
	if rc.Stores.AgentStore != nil {
		if cfg, ok := rc.Stores.AgentStore.Get(name); ok && cfg.Role != "" {
			return cfg.Role
		}
	}
	return "—"
}

// offboardNotice speaks the departure notice into the room. The host's
// own lobby departure keeps the v1 shape — a say through the owner's
// real seat, exactly what the seatless CLI channel produced; every
// other room/operator records a system line (auditable, replayable,
// no seat to borrow).
func (rc *Face) offboardNotice(hub *chat.Hub, actor, notice string) bool {
	if hub == rc.Hub && actor == rc.Local {
		if _, ok := hub.SayAsOwner(notice); ok {
			return true
		}
	}
	hub.SystemRecorded(i18n.Sf("【离岗通告｜操作者 %s】%s", actor, notice))
	return false
}

// offboardArchiveBlocker reports an occupying staffing row of the
// person in a project OTHER than through this pipeline's scope — the
// global archive's precondition (model §3.2: archive only when every
// row is offboard). Without a staffing store the condition is
// vacuously clear (the v1 shape archived unconditionally).
func (rc *Face) offboardArchiveBlocker(name string) (project string, occupying bool) {
	if rc.Stores.StaffStore == nil {
		return "", false
	}
	for _, row := range rc.Stores.StaffStore.ListByPerson(name) {
		if row.Occupying() {
			return row.ProjectKey, true
		}
	}
	return "", false
}

// autoFillHint reads the scope's establishment table (ops/establishment
// in the lobby, p/<key>/establishment in a project) and reports the
// departing post's refill suggestion — the v0.6 六列契约's 自动补员
// column, same text the CLI used to print locally. Unreadable tables
// stay silent about guesses; broken ones say so.
func (rc *Face) autoFillHint(projectKey, role string) string {
	if rc.Stores.Docs == nil || role == "" || role == "—" {
		return ""
	}
	docKey := "ops/establishment"
	if projectKey != chat.LobbyKey {
		docKey = "p/" + projectKey + "/establishment"
	}
	doc, err := rc.Stores.Docs.Get(docKey, 0)
	if err != nil {
		return "" // no table for this scope: the host decides, silently
	}
	rows, perr := kb.EstablishmentRows(doc.Body)
	if perr != "" {
		return i18n.Sf("编制表解析失败（%s），自动补员提示跳过——请核对 %s", perr, docKey)
	}
	for _, row := range rows {
		if row.AutoFill && row.Role == role {
			return i18n.Sf("%s 岗自动补员=是，建议: niuma recruit --name <新成员> --role %q --manual %s", row.Name, row.Role, row.Manual)
		}
	}
	return i18n.S("编制表未对该岗位开自动补员，是否补位由房主定")
}

// offboardDryRunNote renders the would-run plan (the v1 CLI's dry-run
// block, server-side now).
func (rc *Face) offboardDryRunNote(actor, name, role, projectKey string, inprog []tasks.Task, reassignTo string) string {
	var b strings.Builder
	fmt.Fprint(&b, i18n.S("== dry-run: 五步将执行的请求与帧（不执行）==\n"))
	fmt.Fprint(&b, i18n.Sf("操作者 %s · 目标 %s（岗位 %s）· 项目 %s\n", actor, name, role, projectKey))
	fmt.Fprint(&b, i18n.Sf("① 预检已执行（只读）: 在办 %d 件\n", len(inprog)))
	for _, t := range inprog {
		if reassignTo != "" {
			fmt.Fprintf(&b, "② task_update %s assignee→%s\n", t.ID, reassignTo)
		} else {
			fmt.Fprint(&b, i18n.Sf("② 保留在办 %s（force）\n", t.ID))
		}
	}
	fmt.Fprint(&b, i18n.Sf("② say「%s今日离岗%s…」\n", name, reasonClause("")))
	fmt.Fprintf(&b, "③ kick {name:%s}\n", name)
	fmt.Fprint(&b, i18n.Sf("④ staffing offboard %s@%s（全局归档视其余编制行而定）\n", name, projectKey))
	ledgerKey := "ops/room-log"
	if projectKey != chat.LobbyKey {
		ledgerKey = "p/" + projectKey + "/room-log"
	}
	fmt.Fprint(&b, i18n.Sf("⑤ kb_append %s「%s 已离岗（岗位 %s，操作者 %s，…）」\n", ledgerKey, name, role, actor))
	return strings.TrimRight(b.String(), "\n")
}

func reasonClause(r string) string {
	if r == "" {
		return ""
	}
	return "（" + r + "）"
}

func reasonClauseLedger(r string) string {
	if r == "" {
		return "常规离岗，"
	}
	return "原因 " + r + "，"
}

func reassignSuffix(to string) string {
	if to == "" {
		return ""
	}
	return i18n.Sf("（@%s 已被唤醒）", to)
}

func boolZH(b bool) string {
	if b {
		return i18n.S("是")
	}
	return i18n.S("否")
}
