package server

// rebuildvote_test.go — AI 重编译投票的服务端契约：开关读写面的确认
// 令牌纪律（REBUILD——与 autopilot/reset 同门）、未知项目 404、无通道
// 409、幂等翻转不刷屏；关闭腿触发 RebuildVoteCancel；MemberRebuild 的
// 门禁（未接线/忙碌回房间行）与全室通告（计票发起词落进每个在役房间
// ——无源码环境编译必败，正好钉住「失败零重启＋失败也有收口通告」）。

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/WWestC/Niuma_Studio/server/roomops"
	"net/http"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// voteStage wires one ACTIVE project room (proj-rv) with the staffing
// store (the switch's truth), the rebuild channel, and a recorder for
// the cancel hook. rebuildCh==nil keeps the unwired shape.
type voteStage struct {
	s      *Server
	lobby  *chat.Hub
	room   *chat.Hub
	staff  *staffing.Store
	cancel []string
}

func startRebuildVoteStage(t *testing.T, wired bool) *voteStage {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	if _, err := projStore.Create(projects.Project{Key: "proj-rv", Name: "投票项目",
		Workspace: t.TempDir() + "/w"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("proj-rv"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatalf("staffing.Open: %v", err)
	}
	st := &voteStage{lobby: lobby, staff: staff}
	opts := Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			StaffStore:   staff,
		},
		LocalName: "房主",
		Registry:  registry,
		Fleet: FleetFuncs{RebuildVoteCancelFn: func(project string) {
			st.cancel = append(st.cancel, project)
		}}}
	if wired {
		opts.RebuildCh = make(chan struct{}, 1)
	}
	s, err := Start(lobby, opts)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	st.s = s
	if st.room, err = registry.Hub("proj-rv"); err != nil {
		t.Fatalf("registry hub: %v", err)
	}
	return st
}

func votePost(t *testing.T, s *Server, path, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(addrHTTP(s)+path, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func voteGet(t *testing.T, s *Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// voteSys flattens one hub's recorded system lines for Contains checks.
func voteSys(h *chat.Hub) string {
	var b strings.Builder
	for _, m := range h.History() {
		if m.Type == chat.MsgSystem {
			b.WriteString(m.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestRebuildVoteSwitchConfirmDiscipline(t *testing.T) {
	st := startRebuildVoteStage(t, true)
	s := st.s

	// GET 回显缺省（关）
	if code, out := voteGet(t, s, "/p/proj-rv/rebuild-vote"); code != 200 || out["rebuild_vote"] != false {
		t.Fatalf("GET 缺省回显: code=%d out=%v", code, out)
	}
	// 未知项目 404
	if code, _ := voteGet(t, s, "/p/nope/rebuild-vote"); code != 404 {
		t.Fatalf("未知项目 GET = %d, want 404", code)
	}
	// 开启缺令牌 → 400，且错误文案把令牌说出来
	if code, out := votePost(t, s, "/p/proj-rv/rebuild-vote", `{"on":true}`); code != 400 ||
		!strings.Contains(out["error"].(string), "confirm=REBUILD") {
		t.Fatalf("缺令牌开启: code=%d out=%v", code, out)
	}
	// 错令牌同样 400
	if code, _ := votePost(t, s, "/p/proj-rv/rebuild-vote", `{"on":true,"confirm":"AUTOPILOT"}`); code != 400 {
		t.Fatalf("错令牌 = %d, want 400", code)
	}
	// 令牌正确 → 200＋落库＋GET 回显
	if code, out := votePost(t, s, "/p/proj-rv/rebuild-vote", `{"on":true,"confirm":"REBUILD"}`); code != 200 ||
		out["rebuild_vote"] != true {
		t.Fatalf("正确令牌开启: code=%d out=%v", code, out)
	}
	if !st.staff.SettingsOf("proj-rv").RebuildVote {
		t.Fatal("开关没写进 staffing")
	}
	// 重复开启幂等：不重复落系统行
	before := strings.Count(voteSys(st.room), "房主开启了 AI 重编译投票")
	if code, _ := votePost(t, s, "/p/proj-rv/rebuild-vote", `{"on":true,"confirm":"REBUILD"}`); code != 200 {
		t.Fatalf("幂等开启 = %d, want 200", code)
	}
	if after := strings.Count(voteSys(st.room), "房主开启了 AI 重编译投票"); after != before {
		t.Fatalf("幂等开启不该重复刷系统行（%d → %d）", before, after)
	}
	// 关闭免确认
	if code, out := votePost(t, s, "/p/proj-rv/rebuild-vote", `{"on":false}`); code != 200 || out["rebuild_vote"] != false {
		t.Fatalf("免确认关闭: code=%d out=%v", code, out)
	}
	// 方法纪律
	if r, err := http.NewRequest(http.MethodPut, addrHTTP(s)+"/p/proj-rv/rebuild-vote", nil); err != nil {
		t.Fatal(err)
	} else {
		resp, _ := http.DefaultClient.Do(r)
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("PUT = %d, want 405", resp.StatusCode)
		}
	}
}

// 无通道（内嵌/测试形态）：开关可读可关，但开启被 409 拒——投了也无
// 从执行的投票不开门。
func TestRebuildVoteEnableRefusedWithoutChannel(t *testing.T) {
	st := startRebuildVoteStage(t, false)
	if code, out := votePost(t, st.s, "/p/proj-rv/rebuild-vote", `{"on":true,"confirm":"REBUILD"}`); code != 409 {
		t.Fatalf("无通道开启 = %d, want 409（out=%v）", code, out)
	}
	if st.staff.SettingsOf("proj-rv").RebuildVote {
		t.Fatal("拒绝后开关不该落库为开")
	}
}

// 关闭腿触发取消钩子（作废在投的票）：开启不触发，关闭触发一次并带
// 对的项目键。
func TestRebuildVoteCancelHookTappedOnOff(t *testing.T) {
	st := startRebuildVoteStage(t, true)
	if _, out := votePost(t, st.s, "/p/proj-rv/rebuild-vote", `{"on":true,"confirm":"REBUILD"}`); out["rebuild_vote"] != true {
		t.Fatalf("开启失败: %v", out)
	}
	if len(st.cancel) != 0 {
		t.Fatalf("开启不该触发取消钩子: %v", st.cancel)
	}
	votePost(t, st.s, "/p/proj-rv/rebuild-vote", `{"on":false}`)
	if len(st.cancel) != 1 || st.cancel[0] != "proj-rv" {
		t.Fatalf("关闭该触发一次取消钩子（带项目键）: %v", st.cancel)
	}
}

// MemberRebuild 的门禁与通告：未接线回房间行（不说谎的成功）；忙碌同
// 理；接线后（无源码环境编译必败）发起词带计票落进每个在役房间、失
// 败有收口通告、绝不投令牌。
func TestMemberRebuildGatesAndAnnounce(t *testing.T) {
	// 未接线：投票通过的房间要有说实话的行
	bare := startRebuildVoteStage(t, false)
	if err := bare.s.roomopsFace().MemberRebuild("proj-rv", "甲", "2/3 同意"); !errors.Is(err, roomops.ErrRebuildWired) {
		t.Fatalf("未接线 = %v, want roomops.ErrRebuildWired", err)
	}
	if txt := voteSys(bare.room); !strings.Contains(txt, "投票通过，但重编译通道未接线") {
		t.Fatalf("未接线该落房间行:\n%s", txt)
	}

	st := startRebuildVoteStage(t, true)
	// 忙碌门：占住 busy 锁，房间行说实话，不动编译
	st.s.roomopsFace().RebuildBusy.Store(true)
	if err := st.s.roomopsFace().MemberRebuild("proj-rv", "甲", "2/3 同意"); !errors.Is(err, roomops.ErrRebuildBusy) {
		t.Fatalf("忙碌 = %v, want roomops.ErrRebuildBusy", err)
	}
	if txt := voteSys(st.room); !strings.Contains(txt, "已有一轮编译重启在进行") {
		t.Fatalf("忙碌该落房间行:\n%s", txt)
	}
	st.s.roomopsFace().RebuildBusy.Store(false)

	// 编译道（测试环境无源码必败）：发起词带计票＋发起人，全室两房都
	// 听得见；失败有收口；令牌绝不下发。
	if err := st.s.roomopsFace().MemberRebuild("proj-rv", "甲", "2/3 同意"); err == nil {
		t.Fatal("无源码环境编译必败，err 不该为 nil")
	}
	for name, h := range map[string]*chat.Hub{"Niuma_Studio": st.lobby, "项目房": st.room} {
		txt := voteSys(h)
		if !strings.Contains(txt, "成员投票通过（2/3 同意）——甲 发起源码重编译") {
			t.Errorf("%s：发起通告缺计票/发起人（%q）", name, txt)
		}
		if !strings.Contains(txt, "重编译失败") {
			t.Errorf("%s：失败收口通告缺失（%q）", name, txt)
		}
		if strings.Contains(txt, "已重新编译") {
			t.Errorf("%s：编译失败不该有成功通告（%q）", name, txt)
		}
	}
	select {
	case <-st.s.opts.RebuildCh:
		t.Fatal("失败的编译绝不能投重启令牌")
	default:
	}
}
