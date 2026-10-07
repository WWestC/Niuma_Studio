package roomops

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// The wire confirm tokens — the dialogs make the human type 清退/重置/删除,
// the wire carries the protocol token (resetConfirmToken's discipline).
const (
	dismissConfirmToken       = "DISMISS"
	projectResetConfirmToken  = "RESET"
	projectDeleteConfirmToken = "DELETE"
)

// handleProjectDismiss serves POST /p/{key}/dismiss {confirm:"DISMISS"}.
func (rc *Face) HandleProjectDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	key, ok := rc.purgePreflight(w, r, dismissConfirmToken)
	if !ok {
		return
	}
	actor := rc.Local
	res := OffboardResponse{Steps: []OffboardStep{}}
	if err := rc.purgeProjectMembers(key, actor, &res, true); err != nil {
		res.OK = false
		res.Error = err.Error()
		writeOffboard(w, http.StatusBadRequest, res)
		return
	}
	res.OK = true
	res.Note = i18n.Sf("已清退项目 %s 全部成员——编制行与档案已删，房间与历史保留", key)
	writeOffboard(w, http.StatusOK, res)
}

// handleProjectReset serves POST /p/{key}/reset {confirm:"RESET"}.
func (rc *Face) HandleProjectReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	key, ok := rc.purgePreflight(w, r, projectResetConfirmToken)
	if !ok {
		return
	}
	if p, exists := rc.Stores.ProjectStore.Get(key); exists && p.Status == projects.StatusArchived {
		httputil.WriteJSONErr(w, http.StatusConflict,
			i18n.S("已归档项目封存只读（历史原样回来是归档的契约）——需要清空请先复活再重置"))
		return
	}
	actor := rc.Local
	res := OffboardResponse{Steps: []OffboardStep{}}
	step := func(label string, state rune, note string) {
		res.Steps = append(res.Steps, OffboardStep{Step: label, OK: state == '✓', Skip: state == '△', Note: note})
	}
	fail := func(errText string) {
		res.OK = false
		res.Error = errText
		writeOffboard(w, http.StatusBadRequest, res)
	}

	// The crowd first (no departure notice — the history it would land
	// in is about to be wiped): kick, staffing rows, profiles, ranks,
	// ZCode sessions.
	if err := rc.purgeProjectMembers(key, actor, &res, false); err != nil {
		fail(err.Error())
		return
	}

	// Then the project's data, shelf by shelf.
	if rc.Stores.Requirements == nil {
		step(i18n.S("需求架已清"), '△', i18n.S("需求台账未启用"))
	} else {
		n := rc.Stores.Requirements.DropProject(rc.Subject, key)
		step(i18n.S("需求架已清"), '✓', i18n.Sf("%d 条，r_NN 重新从 r_01 起算", n))
	}
	if rc.Stores.Engine == nil {
		step(i18n.S("任务台账已清"), '△', i18n.S("任务系统未启用"))
	} else if n, err := rc.Stores.Engine.DeleteProject(key); err != nil {
		step(i18n.S("任务台账已清"), '✗', i18n.Sf("台账已清 %d 条，但 %v（下次启动可能复活，请手动删该文件）", n, err))
	} else {
		step(i18n.S("任务台账已清"), '✓', i18n.Sf("%d 条，t_NN 重新从 t_01 起算", n))
	}
	if rc.Stores.PlanStore != nil && rc.Stores.PlanStore.Drop(rc.Subject, key) {
		step(i18n.S("待审提案已清"), '✓', i18n.S("排队中的计划提案一并删除"))
	} else if rc.Stores.PlanStore != nil {
		step(i18n.S("待审提案已清"), '✓', i18n.S("本就无待审提案"))
	} else {
		step(i18n.S("待审提案已清"), '△', i18n.S("提案台账未启用"))
	}
	if rc.Stores.MergeStore != nil && rc.Stores.MergeStore.Drop(rc.Subject, key) {
		step(i18n.S("待审合并已清"), '✓', i18n.S("排队中的合并提案一并删除"))
	} else if rc.Stores.MergeStore != nil {
		step(i18n.S("待审合并已清"), '✓', i18n.S("本就无待审合并"))
	} else {
		step(i18n.S("待审合并已清"), '△', i18n.S("合并台账未启用"))
	}
	if rc.Stores.Meetings == nil {
		step(i18n.S("会议史已清"), '△', i18n.S("会议台账未启用"))
	} else {
		n := rc.Stores.Meetings.DropProject(key)
		step(i18n.S("会议史已清"), '✓', i18n.Sf("%d 场评审记录，m_NN 重新从 m_01 起算", n))
	}
	if rc.Stores.Docs == nil {
		step(i18n.S("项目文档已清"), '△', i18n.S("办公室记忆未启用"))
	} else {
		n := 0
		for _, meta := range rc.Stores.Docs.ListOpt(kb.ListOptions{Project: key}) {
			if err := rc.Stores.Docs.Delete(meta.Key, actor); err == nil {
				n++
			}
		}
		touched, err := kb.EnsureProjectEstablishment(rc.Stores.Docs, key, "重置")
		if err != nil {
			step(i18n.S("项目文档已清"), '✗', i18n.Sf("文档已清 %d 份，但编制表重建失败: %v（招聘循环无法就位系统岗）", n, err))
		} else {
			step(i18n.S("项目文档已清"), '✓', i18n.Sf("%d 份已删；编制表按开张态重建（系统岗 %d 行，将自动招聘到岗）", n, len(touched)))
		}
	}
	if rc.Stores.Notices == nil {
		step(i18n.S("群公告已清"), '△', i18n.S("公告存储未启用"))
	} else if err := rc.Stores.Notices.Clear(key, actor, 0); err != nil {
		step(i18n.S("群公告已清"), '✗', err.Error())
	} else {
		step(i18n.S("群公告已清"), '✓', "")
	}
	if rc.Stores.StaffStore != nil {
		rc.Stores.StaffStore.DeleteSettings(key) // 开关块回缺省：补员开、双开关关、成就清零
		step(i18n.S("编制开关复位"), '✓', i18n.S("补员/双开关/成就回开张缺省"))
	}

	// The room's durable memory: ring + jsonl + ledgers + terminal
	// journals, the replay days, the seat snapshot. A room that is not
	// live (draft) holds nothing in memory, but the files still go.
	hub, live := rc.projectHubIfLive(key)
	if live {
		if err := hub.ResetDurable(); err != nil {
			step(i18n.S("聊天史与回放已清"), '✗', err.Error()+i18n.S("（内存态已清，残留文件请手动删）"))
		} else {
			step(i18n.S("聊天史与回放已清"), '✓', i18n.S("历史/已读/收到/表情/提问/终端转录/一日回放全清"))
		}
	} else {
		step(i18n.S("聊天史与回放已清"), '△', i18n.S("项目未开张——无房可清"))
	}
	if rc.Registry != nil {
		root := rc.Registry.Root()
		if root != "" {
			if err := os.RemoveAll(replayDirOf(root, key)); err != nil {
				step(i18n.S("回放日文件已清"), '✗', err.Error())
			} else {
				rc.Replays.Forget(key)
				step(i18n.S("回放日文件已清"), '✓', "")
			}
		}
		if err := os.Remove(rc.Registry.SnapshotPath(key)); err == nil {
			step(i18n.S("座位快照已清"), '✓', "")
		}
	}

	// The fresh history opens with the reset's own receipt; the lobby
	// (studio memory) hears the news too.
	if live {
		hub.SystemRecorded(i18n.Sf("【项目重置｜操作者 %s】成员、对话、任务、需求与文档已全部清空——回到第一次开张的状态", actor))
		// 回执是这次破坏性操作的审计线：冲账落盘后才许应答 200。
		// 不冲的话回执只躺在 flusher 队列里——满载机器上调用方回头
		// 立读历史文件会读到空卷（linux CI 的偶红实录），崩溃更会
		// 丢回执本身。
		hub.FlushHistory()
	}
	rc.Hub.System(i18n.Sf("项目 %s 已重置：成员与全部台账清空，编制表回到开张态（系统岗将自动补齐）", key))

	// The receipt above promises 「系统岗将自动招聘到岗」, but the recruit
	// loop exits once every post is seated and only a boot's bridge
	// attach or a room's EnsureRoom ever re-kicks it — a reset does
	// neither (the room stays live), so without this leg the reseeded
	// posts sit vacant until a lucky restart. The dismiss face taps
	// nothing: it empties the room on purpose and promises no refill.
	if rc.Fleet != nil {
		rc.Fleet.KickRecruit()
	}

	res.OK = true
	res.Note = i18n.Sf("项目 %s 已重置——成员与全部数据清空，回到第一次开张的状态", key)
	writeOffboard(w, http.StatusOK, res)
}

// handleProjectDelete serves POST /p/{key}/delete {confirm:"DELETE"} —
// the registry row's death (see the file header for the composition).
// The order the facts demand: the crowd first (their ZCode sessions
// are still resolvable), then the data shelves, then the non-live
// room's durable files, then the dispatcher's defensive retire (a
// draft never had one, an archived one already retired — both no-op),
// and the registry row LAST: every earlier step keys off the project
// existing.
func (rc *Face) HandleProjectDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	key, ok := rc.purgePreflight(w, r, projectDeleteConfirmToken)
	if !ok {
		return
	}
	p, _ := rc.Stores.ProjectStore.Get(key)
	if p.Status == projects.StatusActive {
		httputil.WriteJSONErr(w, http.StatusConflict,
			i18n.Sf("项目 %s 开张中——先归档（房间冻结、成员离编）再删除", key))
		return
	}
	actor := rc.Local
	res := OffboardResponse{Steps: []OffboardStep{}}
	step := func(label string, state rune, note string) {
		res.Steps = append(res.Steps, OffboardStep{Step: label, OK: state == '✓', Skip: state == '△', Note: note})
	}
	fail := func(errText string) {
		res.OK = false
		res.Error = errText
		writeOffboard(w, http.StatusBadRequest, res)
	}

	// The people (dismiss's exact semantics — a draft has no rows and
	// no room, the steps self-skip; an archived project is the pure
	// archive-residue path).
	if err := rc.purgeProjectMembers(key, actor, &res, false); err != nil {
		fail(err.Error())
		return
	}

	// The data shelves (reset's inventory, delete's wording — no shelf
	// is re-seeded: no project remains to seed for).
	if rc.Stores.Requirements == nil {
		step(i18n.S("需求架已删"), '△', i18n.S("需求台账未启用"))
	} else {
		n := rc.Stores.Requirements.DropProject(rc.Subject, key)
		step(i18n.S("需求架已删"), '✓', i18n.Sf("%d 条", n))
	}
	if rc.Stores.Engine == nil {
		step(i18n.S("任务台账已删"), '△', i18n.S("任务系统未启用"))
	} else if n, err := rc.Stores.Engine.DeleteProject(key); err != nil {
		step(i18n.S("任务台账已删"), '✗', i18n.Sf("台账已删 %d 条，但 %v（下次启动可能复活，请手动删该文件）", n, err))
	} else {
		step(i18n.S("任务台账已删"), '✓', i18n.Sf("%d 条", n))
	}
	if rc.Stores.PlanStore != nil {
		what := i18n.S("本就无待审提案")
		if rc.Stores.PlanStore.Drop(rc.Subject, key) {
			what = i18n.S("排队中的计划提案一并删除")
		}
		step(i18n.S("待审提案已清"), '✓', what)
	}
	if rc.Stores.MergeStore != nil {
		what := i18n.S("本就无待审合并")
		if rc.Stores.MergeStore.Drop(rc.Subject, key) {
			what = i18n.S("排队中的合并提案一并删除")
		}
		step(i18n.S("待审合并已清"), '✓', what)
	}
	if rc.Stores.Meetings == nil {
		step(i18n.S("会议史已删"), '△', i18n.S("会议台账未启用"))
	} else {
		n := rc.Stores.Meetings.DropProject(key)
		step(i18n.S("会议史已删"), '✓', i18n.Sf("%d 场评审记录", n))
	}
	if rc.Stores.Docs == nil {
		step(i18n.S("项目文档已删"), '△', i18n.S("办公室记忆未启用"))
	} else {
		n := 0
		for _, meta := range rc.Stores.Docs.ListOpt(kb.ListOptions{Project: key}) {
			if err := rc.Stores.Docs.Delete(meta.Key, actor); err == nil {
				n++
			}
		}
		step(i18n.S("项目文档已删"), '✓', i18n.Sf("%d 份（编制表不重建——项目已除名）", n))
	}
	if rc.Stores.Notices == nil {
		step(i18n.S("群公告已清"), '△', i18n.S("公告存储未启用"))
	} else if err := rc.Stores.Notices.Clear(key, actor, 0); err != nil {
		step(i18n.S("群公告已清"), '✗', err.Error())
	} else {
		step(i18n.S("群公告已清"), '✓', "")
	}
	if rc.Stores.StaffStore != nil {
		rc.Stores.StaffStore.DeleteSettings(key)
		step(i18n.S("编制开关已删"), '✓', "")
	}

	// The room's durable files. A draft/archived key never
	// instantiates a hub through the registry — but a room archived
	// behind the lifecycle's back (a hand-edited registry, a direct
	// store call) can still sit loaded: that residue state gets the
	// live-room wipe first (memory + files, the reset face's core),
	// then the unload, then PurgeFiles sweeps whatever disk slots
	// remain (the seat snapshot among them — ResetDurable doesn't
	// touch it). The clean path skips straight to PurgeFiles.
	if rc.Registry != nil {
		if hub, loaded := rc.Registry.Rooms()[key]; loaded {
			if err := hub.ResetDurable(); err != nil {
				step(i18n.S("残留活房已清"), '✗', err.Error())
			} else {
				step(i18n.S("残留活房已清"), '✓', i18n.S("房间未经生命周期归档（手工改库残局）——内存与文件先清"))
			}
			if err := rc.Registry.Unload(key); err != nil {
				step(i18n.S("残留活房已撤"), '✗', err.Error())
			}
		}
		if err := rc.Registry.PurgeFiles(key); err != nil {
			step(i18n.S("房间档案已删"), '✗', err.Error()+i18n.S("（残留文件请手动删）"))
		} else {
			step(i18n.S("房间档案已删"), '✓', i18n.S("历史/已读/收到/表情/终端转录/座位快照"))
		}
		root := rc.Registry.Root()
		if root != "" {
			if err := os.RemoveAll(replayDirOf(root, key)); err != nil {
				step(i18n.S("回放日文件已删"), '✗', err.Error())
			} else {
				rc.Replays.Forget(key)
				step(i18n.S("回放日文件已删"), '✓', "")
			}
		}
	}
	if rc.Fleet != nil {
		rc.Fleet.ProjectArchived(key) // draft 没有调度器、归档已退过——双态皆幂等
	}

	// The registry row itself — the verb's namesake, and last.
	if err := rc.Stores.ProjectStore.Delete(key); err != nil {
		fail(err.Error())
		return
	}
	rc.Hub.System(i18n.Sf("项目 %s 已删除（%s）：成员档案、台账与房间档案一并清除——工作区目录与 git 仓库原样保留", key, p.Name))

	res.OK = true
	res.Note = i18n.Sf("项目 %s（%s）已删除——项目册除名，工作区目录与 git 仓库未动", p.Name, key)
	writeOffboard(w, http.StatusOK, res)
}

// purgePreflight is the two faces' shared gate: method already checked
// by the caller; here the project store must exist, the key must be a
// real non-lobby project, and the confirm token must match. Writes the
// refusal itself; reports whether the run may proceed.
func (rc *Face) purgePreflight(w http.ResponseWriter, r *http.Request, token string) (key string, ok bool) {
	if rc.Stores.ProjectStore == nil {
		http.NotFound(w, r)
		return "", false
	}
	key = r.PathValue("key")
	if key == chat.LobbyKey {
		httputil.WriteJSONErr(w, http.StatusBadRequest,
			i18n.S("Niuma_Studio 是全工作室的公共房——整室清空走设置里的「重置工作室」"))
		return "", false
	}
	if _, exists := rc.Stores.ProjectStore.Get(key); !exists {
		http.NotFound(w, r)
		return "", false
	}
	if rc.Stores.StaffStore == nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest,
			i18n.S("编制表未启用——无法按项目圈定成员（内嵌/测试形态）"))
		return "", false
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return "", false
	}
	if body.Confirm != token {
		httputil.WriteJSONErr(w, http.StatusBadRequest,
			i18n.Sf("确认口令不符——该操作需要在请求体携带 \"confirm\":\"%s\"（前端确认对话框代填）", token))
		return "", false
	}
	return key, true
}

// purgeProjectMembers is the dismiss core both faces share: collect
// the project's staffing rows, kick everyone out of the room, delete
// the rows, delete the global profiles of everyone who staffs nowhere
// else (a row elsewhere keeps the person alive — 借调/多项目中间态,
// offboardArchiveBlocker's rule), drop their ranks, and hand the
// session ids to main's ZCode deep-clean hook. recordNotice speaks the
// departure line into the room's history (skipped by the reset face —
// that history is about to be wiped).
func (rc *Face) purgeProjectMembers(key, actor string, res *OffboardResponse, recordNotice bool) error {
	step := func(label string, state rune, note string) {
		res.Steps = append(res.Steps, OffboardStep{Step: label, OK: state == '✓', Skip: state == '△', Note: note})
	}

	rows := rc.Stores.StaffStore.ListByProject(key)
	names := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if _, dup := seen[r.Person]; dup {
			continue
		}
		seen[r.Person] = struct{}{}
		names = append(names, r.Person)
	}
	// ZCode sessions safe to deep-clean: this project's bindings whose
	// person is not currently occupying a seat elsewhere (that session
	// is still their live workplace).
	var ids []string
	for _, r := range rows {
		if r.SessionID == "" || rc.personOccupiesBesides(r.Person, key) {
			continue
		}
		ids = append(ids, r.SessionID)
	}
	step(i18n.Sf("采集编制行（%d 行 · %d 人）", len(rows), len(names)), '✓', "")

	hub, live := rc.projectHubIfLive(key)
	if live {
		kicked := 0
		for _, m := range hub.Members() {
			if m.Name == rc.Local {
				continue // the host's own seat stays
			}
			if _, ok := hub.Kick(m.Name); ok {
				kicked++
			}
		}
		hub.Forget(names)
		step(i18n.Sf("清房 kick（移出 %d 人）", kicked), '✓', "")
	} else {
		step(i18n.S("清房 kick"), '△', i18n.S("项目未开张或已归档——无房可清（纯档案清理路径）"))
	}

	removed := 0
	for _, r := range rows {
		if rc.Stores.StaffStore.RemoveRow(key, r.Person) {
			removed++
		}
	}
	step(i18n.Sf("删编制行（%d 行）", removed), '✓', "")

	gone, kept := 0, 0
	for _, name := range names {
		if rc.personOccupiesBesides(name, key) {
			kept++
			continue
		}
		if rc.Stores.AgentStore != nil {
			rc.Stores.AgentStore.Remove(name)
		}
		if rc.Stores.Engine != nil && len(rc.Stores.StaffStore.ListByPerson(name)) == 0 {
			rc.Stores.Engine.RemoveRank(name)
		}
		gone++
	}
	note := i18n.Sf("%d 人档案已删", gone)
	if kept > 0 {
		note += i18n.Sf("，%d 人在别项目在编、档案保留", kept)
	}
	if rc.Stores.AgentStore == nil {
		step(i18n.S("删全局档案"), '△', i18n.S("档案存储未启用（纯座位清理路径）"))
	} else {
		step(i18n.S("删全局档案"), '✓', note)
	}

	switch {
	case rc.Fleet == nil:
		step(i18n.S("ZCode 侧深清"), '△', i18n.S("未接线（内嵌/测试形态）——桌面侧栏与会话残留由房主手动清理"))
	case len(ids) == 0:
		step(i18n.S("ZCode 侧深清"), '△', i18n.S("无可清会话"))
	default:
		if err := rc.Fleet.PurgeMemberSessions(ids); err != nil {
			step(i18n.S("ZCode 侧深清"), '✗', i18n.Sf("%v（档案已删；桌面侧栏可能残留行，重开桌面端即散）", err))
		} else {
			step(i18n.S("ZCode 侧深清"), '✓', i18n.Sf("已删 %d 个会话（侧栏行/会话正文/会话文件）", len(ids)))
		}
	}

	// The lanes these people were holding (and every past member's
	// residue under this project's key) are undelivered cargo for people
	// who no longer exist — dead weight in the inbox, and poison when a
	// reset's fresh hire reuses a predecessor's name (the 2026-10-04
	// book incident: the re-hired 小苗 answered the former 小苗's
	// pre-reset queue before noticing the reset had voided it).
	switch {
	case rc.Fleet == nil:
		step(i18n.S("投递车道已清"), '△', i18n.S("未接线（内嵌/测试形态）——车道文件由房主手动清理"))
	default:
		if n, err := rc.Fleet.PurgeProjectLanes(key); err != nil {
			step(i18n.S("投递车道已清"), '✗', i18n.Sf("%v（已处理 %d 份；残留文件请手动删）", err, n))
		} else {
			step(i18n.S("投递车道已清"), '✓', i18n.Sf("%d 份成员车道文件的未达消息已清（名字复用不继承前任积压）", n))
		}
	}

	if recordNotice && live {
		hub.SystemRecorded(i18n.Sf("【清退通告｜操作者 %s】项目成员已全部清退（%d 人，编制行与档案已删）", actor, len(names)))
	}
	return nil
}

// projectHubIfLive resolves the project's room when one can exist —
// tolerant by design: a draft or archived project has no room, and the
// dismiss face still has residue to delete. The bool reports whether
// the hub is live.
func (rc *Face) projectHubIfLive(key string) (*chat.Hub, bool) {
	if rc.Registry == nil {
		return nil, false
	}
	hub, err := rc.Registry.Hub(key)
	if err != nil {
		return nil, false
	}
	return hub, true
}

// personOccupiesBesides reports whether name holds an occupying
// staffing row in any project other than exceptKey — the global
// profile's survival rule (model §3.2, offboardArchiveBlocker's twin).
func (rc *Face) personOccupiesBesides(name, exceptKey string) bool {
	for _, row := range rc.Stores.StaffStore.ListByPerson(name) {
		if row.ProjectKey != exceptKey && row.Occupying() {
			return true
		}
	}
	return false
}

// replayDirOf is replay.go's path convention, mirrored here for the
// wipe legs (unifies when the reads domain moves; kept byte-identical).
func replayDirOf(root, key string) string {
	return filepath.Join(root, "replay", key)
}
