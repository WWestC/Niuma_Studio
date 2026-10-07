package roomops

import (
	"errors"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"github.com/WWestC/Niuma_Studio/util"
)

// 门禁类错误（区别于编译失败——后者已由核心全室通告，前者需要调用
// 侧自己告诉房间为什么没执行）。
var (
	ErrRebuildWired = errors.New("重编译通道未接线（内嵌/测试形态）")
	ErrRebuildBusy  = errors.New("已有一轮编译重启在进行——无需重复发起")
	errRebuildExit  = errors.New("工作室已在收摊/重启中——无需重复发起")
)

// handleRebuild serves POST /rebuild: bodyless (the op is non-destructive
// — no protocol-level token like /reset's RESET; the dialog confirmation
// is enough). The response arrives after the build — seconds warm,
// minutes cold — carrying either the restart receipt or the compile
// failure verbatim.
func (rc *Face) HandleRebuild(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/rebuild" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if rc.RebuildCh == nil {
		http.NotFound(w, r) // embeds/tests that don't wire the choreography
		return
	}
	if !rc.RebuildBusy.CompareAndSwap(false, true) {
		httputil.WriteJSONErr(w, http.StatusConflict, i18n.S(ErrRebuildBusy.Error()))
		return
	}
	defer rc.RebuildBusy.Store(false)

	took, err := rc.runRebuildCore(i18n.S("房主已发起源码重编译——编译期间工作室照常运行，完成后将自动重启（热缓存几秒、冷缓存一两分钟）"))
	if err != nil {
		switch {
		case errors.Is(err, errRebuildExit):
			httputil.WriteJSONErr(w, http.StatusConflict, i18n.S(err.Error()))
		default:
			httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	httputil.WriteJSON(w, map[string]any{
		"ok":         true,
		"restarting": true,
		"text":       i18n.Sf("编译成功（%s）——正在收摊重启，新窗口即将自动顶上", took.Truncate(time.Second)),
	})
}

// MemberRebuild is the AI-vote fire leg: a passed member vote reaches the
// same compile+swap+handoff the owner's button rides, announced under
// the tally instead of the owner. Gate failures (unwired / busy) are
// answered with a recorded line in the voting room itself — the room
// just heard 「投票通过」, silence would read as success; build failures
// are already announced studio-wide by the core, so the error rides
// home for the log only.
func (rc *Face) MemberRebuild(project, by, tally string) error {
	hub := rc.PlanHub(project)
	if rc.RebuildCh == nil {
		hub.SystemRecorded(i18n.S("[重编译投票] 投票通过，但重编译通道未接线（内嵌/测试形态）——本轮未执行"))
		return ErrRebuildWired
	}
	if !rc.RebuildBusy.CompareAndSwap(false, true) {
		hub.SystemRecorded(i18n.S("[重编译投票] 投票通过，但已有一轮编译重启在进行——本轮不重复发起"))
		return ErrRebuildBusy
	}
	defer rc.RebuildBusy.Store(false)
	_, err := rc.runRebuildCore(i18n.Sf(
		"成员投票通过（%s）——%s 发起源码重编译：编译期间工作室照常运行，完成后将自动重启（热缓存几秒、冷缓存一两分钟）",
		tally, by))
	return err
}

// runRebuildCore is the shared compile face: 全室开工挂号 → 前台编译＋
// 换位 → 成功通告＋编年史＋令牌。Callers hold the rebuildBusy CAS.
func (rc *Face) runRebuildCore(announce string) (time.Duration, error) {
	// 开工先全室挂号：编译期间各房照常运行，但这轮卡顿与随后的
	// 重启要有来路——大厅与每个已开张的项目房各记一条系统通告，
	// 编译中途重连的成员回放历史也看得见。
	rc.systemAllRooms(announce)

	// 编译＋换位在前台：失败（无工具链/无源码/编译错误/超时）全不是
	// 服务器的错，以错误带原因回家，旧进程原样服务、零感知
	took, err := util.RebuildSelf()
	if err != nil {
		rc.systemAllRooms(i18n.S("源码重编译失败——工作室按原样继续运行，未受影响"))
		return 0, err
	}
	// 成功通告（「已经 rebuild 了」）先于令牌：广播与历史落盘赶在
	// 收摊开始之前出门，落到每个在役房间的历史里，重启后回放仍在。
	rc.systemAllRooms(i18n.Sf(
		"源码已重新编译（耗时 %s）——工作室即将重启，调度成员将由调度器自动归位",
		took.Truncate(time.Second)))
	// r_10 编年史钩子（t_152）：rebuild 成功——记这笔即将上线的 HEAD 提交
	//（体例 §四·2：只收 rebuild 成功笔，WIP/fixup 天然不录；失败在收摊
	// 之前已 return，不会误录）
	rc.Chronicle.OnRebuildCommit()
	select {
	case rc.RebuildCh <- struct{}{}:
	default:
		// 理论不可达（busy 锁挡着重复进入）；万一主侧已在退场，收口错误
		return took, errRebuildExit
	}
	return took, nil
}

// rebuildVoteConfirmToken is the protocol-level opt-in for enabling the
// AI rebuild vote (the autopilot/reset precedent): handing the members
// a studio-restarting lever must never ride a bare POST body.
const rebuildVoteConfirmToken = "REBUILD"

// handleProjectRebuildVote is the per-project AI-rebuild-vote switch:
// GET {project, on}; POST {on, confirm} — enabling demands the REBUILD
// token AND a wired rebuild channel (a vote that can never fire is
// theater), disabling is always free and voids any vote still open in
// that room (Options.RebuildVoteCancel → the fleet → the dispatcher).
// Both directions land a recorded system line — the lever's state is
// history-grade news.
func (rc *Face) HandleProjectRebuildVote(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if rc.Stores.StaffStore == nil || rc.Stores.ProjectStore == nil || !rc.Stores.ProjectStore.Exists(key) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		httputil.WriteJSON(w, map[string]any{
			"project":      key,
			"rebuild_vote": rc.Stores.StaffStore.SettingsOf(key).RebuildVote,
		})
	case http.MethodPost:
		var body struct {
			On      bool   `json:"on"`
			Confirm string `json:"confirm"`
		}
		if err := httputil.ReadJSONBody(r, &body); err != nil {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.On {
			if body.Confirm != rebuildVoteConfirmToken {
				httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("开启 AI 重编译投票需确认令牌 confirm=REBUILD——成员投票超半数将直接重启整个工作室，请从前端设置卡的警告对话框发起"))
				return
			}
			if rc.RebuildCh == nil {
				httputil.WriteJSONErr(w, http.StatusConflict, i18n.S("重编译通道未接线（内嵌/测试形态）——投票通了也无从执行，开关不开启"))
				return
			}
		}
		set := rc.Stores.StaffStore.SettingsOf(key)
		if set.RebuildVote == body.On {
			httputil.WriteJSON(w, map[string]any{"project": key, "rebuild_vote": set.RebuildVote})
			return
		}
		set = rc.Stores.StaffStore.SetRebuildVote(key, body.On)
		hub := rc.PlanHub(key)
		if body.On {
			hub.SystemRecorded(i18n.S("[重编译投票] 房主开启了 AI 重编译投票：本房成员可回复「提议重编译」发起投票，全房超半数同意即自动重新编译并重启工作室（设置卡·智能 可随时关闭，关闭即作废进行中的投票）"))
		} else {
			hub.SystemRecorded(i18n.S("[重编译投票] 房主关闭了 AI 重编译投票：成员提议不再受理"))
			if rc.Fleet != nil {
				rc.Fleet.RebuildVoteCancel(key) // 作废进行中的投票（无票在投即无事发生）
			}
		}
		httputil.WriteJSON(w, map[string]any{"project": key, "rebuild_vote": set.RebuildVote})
	default:
		httputil.WriteJSONErr(w, http.StatusMethodNotAllowed, "GET/POST only")
	}
}

// systemAllRooms records one operator line in EVERY live room: the
// lobby (the server's own hub) plus each instantiated project room
// (Registry.Rooms()) — studio-level events shouldn't reach only the
// room the button lives in. Recorded rather than fire-and-forget so a
// member dialing back in mid-build replays why the studio is about to
// blink; the append hits disk synchronously, so the line survives the
// restart it announces. Rooms never instantiated have no audience and
// no history to miss. The lobby is deduped by pointer — the registry
// holds the same hub under LobbyKey when one is wired.
//
// Every line rides chat.EventRebuild: these are ambient studio news
// landing in all rooms at once, not conversation anyone owes a read —
// the marker tells clients to keep them badge-exempt (no unread dot,
// no notification ding), or one recompile would redden every project
// twice per restart.
func (rc *Face) systemAllRooms(text string) {
	if rc.Hub != nil {
		rc.Hub.SystemRecordedEvent(chat.EventRebuild, text)
	}
	if rc.Registry == nil {
		return
	}
	for _, h := range rc.Registry.Rooms() {
		if h == rc.Hub {
			continue
		}
		h.SystemRecordedEvent(chat.EventRebuild, text)
	}
}
