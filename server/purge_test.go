package server

// purge_test.go — 项目清退/重置两面（POST /p/{key}/dismiss | /reset）的
// 端到端契约：清退删人不动史（编制行/全局档案/等级/ZCode 会话深清，
// 在别项目在编者档案保留）；重置先清退再清台账（需求/任务/提案/会议/
// 文档/开关/房间持久记忆/回放日文件/座位快照），编制表按开张态重建；
// 拒绝路径（大厅/口令/未知项目/已归档）不动任何存储。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// purgeRig is the full-store rig both faces deserve: every ledger wired
// over one temp root so file-level assertions (架子文件真删、历史文件重
// 生) hold, the registry persisting under the same root, and the ZCode
// deep-clean hook captured into a slice instead of touching any desktop.
type purgeRig struct {
	s         *Server
	root      string
	proj      *projects.Store
	staff     *staffing.Store
	agent     *agents.Store
	engine    *tasks.Engine
	reqs      requirements.Store
	plans     *plan.Engine
	merges    *merge.Engine
	meets     meeting.Store
	docs      *kb.DocsStore
	notices   notice.Store
	purged    *[]string
	kicked    *int
	laneWiped *[]string
	registry  *chat.Registry
}

func startPurgeRig(t *testing.T) *purgeRig {
	t.Helper()
	root := t.TempDir()
	lobby := chat.NewHub()
	projStore, err := projects.Open(filepath.Join(root, "projects.json"))
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	staffStore, err := staffing.Open(filepath.Join(root, "staffing.json"))
	if err != nil {
		t.Fatalf("staffing.Open: %v", err)
	}
	agentStore, err := agents.Open(filepath.Join(root, "agents.json"))
	if err != nil {
		t.Fatalf("agents.Open: %v", err)
	}
	engine := tasks.MustOpenMemory("房主")
	engine.SetProjectKeys(func() []string { // main 同款：active ∪ draft
		var keys []string
		for _, p := range projStore.List() {
			if p.Status == projects.StatusActive || p.Status == projects.StatusDraft {
				keys = append(keys, p.Key)
			}
		}
		return keys
	})
	reqStore := requirements.OpenMemory()
	planStore := plan.OpenMemory()
	mergeStore := merge.OpenMemory()
	meetStore := meeting.OpenMemory()
	docs, err := kb.OpenDocs(filepath.Join(root, "docs"))
	if err != nil {
		t.Fatalf("kb.OpenDocs: %v", err)
	}
	notices := notice.NewMemory()
	registry := chat.NewRegistry(projStore, root, "房主", "老板")
	purged := &[]string{}
	kicked := new(int)
	laneWiped := &[]string{}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			StaffStore:   staffStore,
			AgentStore:   agentStore,
			Engine:       engine,
			Requirements: reqStore,
			PlanStore:    planStore,
			MergeStore:   mergeStore,
			Meetings:     meetStore,
			Docs:         docs,
			Notices:      notices,
		},
		LocalName: "房主",
		Registry:  registry,
		Fleet: FleetFuncs{
			PurgeMemberSessFn: func(ids []string) error { *purged = append(*purged, ids...); return nil },
			KickRecruitFn:     func() { *kicked++ },
			PurgeProjectLanesFn: func(project string) (int, error) {
				*laneWiped = append(*laneWiped, project)
				return 2, nil
			},
		}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return &purgeRig{s: s, root: root, proj: projStore, staff: staffStore, agent: agentStore,
		engine: engine, reqs: reqStore, plans: planStore, merges: mergeStore,
		meets: meetStore, docs: docs, notices: notices, purged: purged, kicked: kicked,
		laneWiped: laneWiped, registry: registry}
}

// newPurgeProject creates, activates and opens one project — the room
// instantiates through the registry, its history file slot attached.
func (r *purgeRig) newPurgeProject(t *testing.T, key string) {
	t.Helper()
	if _, err := r.proj.Create(projects.Project{Key: key, Name: key + "项目",
		Workspace: t.TempDir()}); err != nil {
		t.Fatalf("Create %s: %v", key, err)
	}
	if _, err := r.proj.Activate(key); err != nil {
		t.Fatalf("Activate %s: %v", key, err)
	}
	if _, err := r.registry.Hub(key); err != nil {
		t.Fatalf("Hub %s: %v", key, err)
	}
}

func postPurgeHTTP(t *testing.T, s *Server, path, confirm string) (int, OffboardResponse) {
	t.Helper()
	body := "{}"
	if confirm != "" {
		b, _ := json.Marshal(map[string]string{"confirm": confirm})
		body = string(b)
	}
	resp, err := http.Post(addrHTTP(s)+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var rep OffboardResponse
	// 拒绝路径的 404/405 是纯文本——解码失败按零值回（调用方只看状态码）
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		rep = OffboardResponse{}
	}
	return resp.StatusCode, rep
}

// TestProjectDismissDeletesPeople: the whole crowd of proj-d goes —
// staffing rows, the lone member's profile and rank, their ZCode session
// (hook-captured) — while 小红, who also staffs proj-q, keeps her profile
// (借调中间态), the room's history survives, and the other project's
// rows are untouched.
func TestProjectDismissDeletesPeople(t *testing.T) {
	r := startPurgeRig(t)
	r.newPurgeProject(t, "proj-d")
	r.newPurgeProject(t, "proj-q")
	// 小红先在 proj-d 入编再离编（一期一人一时一房），随后入编 proj-q——
	// 清退 proj-d 时的借调中间态：离编残留行在，人在别处在编。
	if _, err := r.staff.Join("proj-d", "小明", "后端", "sess-a"); err != nil {
		t.Fatalf("Join 小明: %v", err)
	}
	if _, err := r.staff.Join("proj-d", "小红", "前端", "sess-b"); err != nil {
		t.Fatalf("Join 小红@proj-d: %v", err)
	}
	if _, err := r.staff.MarkActive("proj-d", "小明"); err != nil {
		t.Fatalf("MarkActive 小明: %v", err)
	}
	if _, err := r.staff.MarkActive("proj-d", "小红"); err != nil {
		t.Fatalf("MarkActive 小红: %v", err)
	}
	if _, err := r.staff.Offboard("proj-d", "小红"); err != nil {
		t.Fatalf("Offboard 小红: %v", err)
	}
	if _, err := r.staff.Join("proj-q", "小红", "前端", "sess-c"); err != nil {
		t.Fatalf("Join 小红@proj-q: %v", err)
	}
	if _, err := r.staff.MarkActive("proj-q", "小红"); err != nil {
		t.Fatalf("MarkActive 小红@proj-q: %v", err)
	}
	r.agent.Upsert("小明", "后端", "", "提示词A")
	r.agent.Upsert("小红", "前端", "", "提示词B")
	r.engine.SetRank("小明", 3)
	hub, _ := r.registry.Hub("proj-d")
	hub.Join("小明", "后端", false)
	if !rosterHas(hub, "小明") {
		t.Fatal("前置失败：小明应在房")
	}
	hub.SystemRecorded("清退前的旧消息")

	status, rep := postPurgeHTTP(t, r.s, "/p/proj-d/dismiss", "DISMISS")
	if status != http.StatusOK || !rep.OK {
		t.Fatalf("清退应 200 ok，得 %d %+v", status, rep)
	}
	if got := len(r.staff.ListByProject("proj-d")); got != 0 {
		t.Fatalf("proj-d 编制行应清空，剩 %d 行", got)
	}
	if _, ok := r.agent.Get("小明"); ok {
		t.Fatal("小明的全局档案应已删除")
	}
	if _, ok := r.agent.Get("小红"); !ok {
		t.Fatal("小红在 proj-q 在编，档案应保留")
	}
	if rosterHas(hub, "小明") {
		t.Fatal("小明应已被踢出房间")
	}
	if r.engine.Rank("小明") != 1 {
		t.Fatalf("小明的等级应随档案删除回到 Lv.1，得 Lv.%d", r.engine.Rank("小明"))
	}
	if len(*r.purged) != 1 || (*r.purged)[0] != "sess-a" {
		t.Fatalf("ZCode 深清应只吃小明的会话 sess-a（小红的 sess-c 在别项目在用），得 %v", *r.purged)
	}
	if got := len(r.staff.ListByProject("proj-q")); got != 1 {
		t.Fatalf("proj-q 的编制行不许动，剩 %d 行", got)
	}
	hist := hub.History()
	if len(hist) == 0 || !strings.Contains(hist[len(hist)-1].Text, "清退通告") {
		t.Fatalf("清退后历史应保留且尾帧是清退通告，得 %d 帧", len(hist))
	}
}

// rosterHas reports whether the hub's member roster (live seats and
// grace ghosts alike) still carries the name.
func rosterHas(h *chat.Hub, name string) bool {
	for _, m := range h.Members() {
		if m.Name == name {
			return true
		}
	}
	return false
}

// TestProjectResetWipesToFirstOpen: after the reset the project's every
// ledger is empty AND its shelf files are gone (an empty file would
// resurrect on boot), the establishment table is re-seeded with its
// system posts, the room's durable memory starts over (history file has
// only the reset receipt), and the replay days plus seat snapshot are
// removed.
func TestProjectResetWipesToFirstOpen(t *testing.T) {
	r := startPurgeRig(t)
	r.newPurgeProject(t, "proj-w")
	if _, err := r.staff.Join("proj-w", "小牛", "后端", "sess-w"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	r.agent.Upsert("小牛", "后端", "", "提示词")
	r.staff.SetAutoRecall("proj-w", false) // 开关被拧过——重置应回缺省
	if out := r.engine.Create("proj-w", "房主", "旧任务", "", "小牛"); out.Denied {
		t.Fatalf("Create task: %s", out.Reason)
	}
	if _, err := r.reqs.Create("", "proj-w", "旧需求", "", "房主"); err != nil {
		t.Fatalf("req Create: %v", err)
	}
	r.plans.Submit("", "proj-w", "房主", plan.Plan{Title: "旧提案", Tasks: []plan.PlanTask{{Title: "条目"}}}, 1)
	hub, _ := r.registry.Hub("proj-w")
	hub.SystemRecorded("重置前的旧消息")
	if _, err := r.docs.Write("p/proj-w/room-log", "离岗台账", "旧台账一行", "房主"); err != nil {
		t.Fatalf("docs Write: %v", err)
	}
	// 回放日文件＋座位快照各落一份，重置后必须都消失
	replayDir := filepath.Join(r.root, "replay", "proj-w")
	if err := os.MkdirAll(replayDir, 0o755); err != nil {
		t.Fatalf("mkdir replay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, "20260101.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write replay day: %v", err)
	}
	if err := os.WriteFile(r.registry.SnapshotPath("proj-w"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	status, rep := postPurgeHTTP(t, r.s, "/p/proj-w/reset", "RESET")
	if status != http.StatusOK || !rep.OK {
		t.Fatalf("重置应 200 ok，得 %d %+v", status, rep)
	}
	if got := len(r.engine.ListFiltered("", "", "proj-w", "")); got != 0 {
		t.Fatalf("任务应清空，剩 %d 条", got)
	}
	if _, err := os.Stat(filepath.Join(r.root, "tasks", "proj-w.json")); !os.IsNotExist(err) {
		t.Fatal("任务架文件应删除（空文件会在下次启动复活）")
	}
	if got := len(r.reqs.ListByProject("", "proj-w")); got != 0 {
		t.Fatalf("需求应清空，剩 %d 条", got)
	}
	if _, err := os.Stat(filepath.Join(r.root, "reqs", "proj-w.json")); !os.IsNotExist(err) {
		t.Fatal("需求架文件应删除")
	}
	if p := r.plans.Pending("", "proj-w"); p != nil {
		t.Fatalf("待审提案应清空，剩 %+v", p)
	}
	if _, ok := r.agent.Get("小牛"); ok {
		t.Fatal("成员档案应已删除")
	}
	if got := len(r.staff.ListByProject("proj-w")); got != 0 {
		t.Fatalf("编制行应清空，剩 %d 行", got)
	}
	if !r.staff.SettingsOf("proj-w").AutoRecall {
		t.Fatal("编制开关应回开张缺省（auto_recall=true）")
	}
	// 编制表按开张态重建：系统岗在，旧台账文档没了
	if _, err := r.docs.Get("p/proj-w/establishment", 0); err != nil {
		t.Fatalf("编制表应重建: %v", err)
	}
	if _, err := r.docs.Get("p/proj-w/room-log", 0); err == nil {
		t.Fatal("旧项目文档应已删除")
	}
	// 房间持久记忆重开：历史文件只剩重置回执一帧
	var lines []string
	if err := chat.ScanHistory(r.registry.HistoryPath("proj-w"), func(m chat.Message) bool {
		lines = append(lines, m.Text)
		return true
	}); err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "项目重置") {
		t.Fatalf("重置后历史文件应以重置回执开卷，得 %v", lines)
	}
	if len(lines) > 2 { // 重置回执（＋可能的踢房帧）之外不许有旧消息
		t.Fatalf("重置后历史应近乎全空，得 %d 帧: %v", len(lines), lines)
	}
	if _, err := os.Stat(replayDir); !os.IsNotExist(err) {
		t.Fatal("回放日目录应删除")
	}
	if _, err := os.Stat(r.registry.SnapshotPath("proj-w")); !os.IsNotExist(err) {
		t.Fatal("座位快照应删除")
	}
}

// TestProjectResetRekicksRecruiter: 重置回执承诺「系统岗将自动招聘到
// 岗」，但招聘循环一旦满编就退出，且只有 boot 桥接/项目开张会重燃它——
// 重置不动房间的生死，成功路径必须亲手把循环点回来，否则重播的系统岗
// 要等一次幸运的重启才有人招（2026-10-04 book 项目重置后空窗 2 分 44 秒
// 的事故形状）。清退面与口令不符的拒绝路径不许点：清退是房主故意清房、
// 不承诺补员，拒绝路径什么都没做成。
func TestProjectResetRekicksRecruiter(t *testing.T) {
	r := startPurgeRig(t)
	r.newPurgeProject(t, "proj-k")
	if _, err := r.staff.Join("proj-k", "小牛", "后端", "sess-k"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if status, _ := postPurgeHTTP(t, r.s, "/p/proj-k/reset", "WRONG"); status != http.StatusBadRequest {
		t.Fatalf("口令不符应 400，得 %d", status)
	}
	if *r.kicked != 0 {
		t.Fatalf("拒绝路径不许点火，已点 %d 次", *r.kicked)
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-k/dismiss", "DISMISS"); status != http.StatusOK || !rep.OK {
		t.Fatalf("清退应 200 ok，得 %d %+v", status, rep)
	}
	if *r.kicked != 0 {
		t.Fatalf("清退面不许点火（清房是故意的，不承诺补员），已点 %d 次", *r.kicked)
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-k/reset", "RESET"); status != http.StatusOK || !rep.OK {
		t.Fatalf("重置应 200 ok，得 %d %+v", status, rep)
	}
	if *r.kicked != 1 {
		t.Fatalf("重置成功应恰好重燃招聘循环一次，得 %d 次", *r.kicked)
	}
}

// TestPurgeWipesDeliveryLanes: 清退与重置都经成员清理核心，投递车道随
// 之清空——未达消息随人走，重置后的同名新聘不继承前任积压（2026-10-04
// book 事故的第二半：复用的「小苗」接到前任小苗重置前的排队批）。拒绝
// 路径（口令不符）什么都不许碰。
func TestPurgeWipesDeliveryLanes(t *testing.T) {
	r := startPurgeRig(t)
	r.newPurgeProject(t, "proj-l")
	if _, err := r.staff.Join("proj-l", "小禾", "后端", "sess-l"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if status, _ := postPurgeHTTP(t, r.s, "/p/proj-l/dismiss", "WRONG"); status != http.StatusBadRequest {
		t.Fatalf("口令不符应 400，得 %d", status)
	}
	if len(*r.laneWiped) != 0 {
		t.Fatalf("拒绝路径不许清车道，已清 %v", *r.laneWiped)
	}

	status, rep := postPurgeHTTP(t, r.s, "/p/proj-l/dismiss", "DISMISS")
	if status != http.StatusOK || !rep.OK {
		t.Fatalf("清退应 200 ok，得 %d %+v", status, rep)
	}
	if len(*r.laneWiped) != 1 || (*r.laneWiped)[0] != "proj-l" {
		t.Fatalf("清退应清 proj-l 车道恰一次，得 %v", *r.laneWiped)
	}
	if !stepsNoteOK(rep.Steps, "投递车道已清") {
		t.Fatalf("清退回执应带「投递车道已清 ✓」步骤，得 %+v", rep.Steps)
	}

	// 重置同款：重新入编一人再重置，车道再清一次
	if _, err := r.staff.Join("proj-l", "小禾", "后端", "sess-l2"); err != nil {
		t.Fatalf("re-Join: %v", err)
	}
	status, rep = postPurgeHTTP(t, r.s, "/p/proj-l/reset", "RESET")
	if status != http.StatusOK || !rep.OK {
		t.Fatalf("重置应 200 ok，得 %d %+v", status, rep)
	}
	if len(*r.laneWiped) != 2 {
		t.Fatalf("重置应再清车道一次（共 2 次），得 %v", *r.laneWiped)
	}
	if !stepsNoteOK(rep.Steps, "投递车道已清") {
		t.Fatalf("重置回执应带「投递车道已清 ✓」步骤，得 %+v", rep.Steps)
	}
}

// stepsNoteOK reports whether the step log carries the label as a
// successful (✓) step.
func stepsNoteOK(steps []OffboardStep, label string) bool {
	for _, s := range steps {
		if s.Step == label {
			return s.OK
		}
	}
	return false
}

// TestPurgeGuards: the lobby refuses (its crowd is the studio's), an
// unknown key 404s, a wrong confirm token 400s without touching a row,
// and an archived project refuses a reset (封存只读) while its dismiss
// still cleans departure residue.
func TestPurgeGuards(t *testing.T) {
	r := startPurgeRig(t)
	r.newPurgeProject(t, "proj-g")
	if _, err := r.staff.Join("proj-g", "小石", "测试", "sess-g"); err != nil {
		t.Fatalf("Join: %v", err)
	}

	if status, _ := postPurgeHTTP(t, r.s, "/p/default/dismiss", "DISMISS"); status != http.StatusBadRequest {
		t.Fatalf("Niuma_Studio 清退应 400，得 %d", status)
	}
	if status, _ := postPurgeHTTP(t, r.s, "/p/nope/reset", "RESET"); status != http.StatusNotFound {
		t.Fatalf("未知项目应 404，得 %d", status)
	}
	if status, _ := postPurgeHTTP(t, r.s, "/p/proj-g/dismiss", "WRONG"); status != http.StatusBadRequest {
		t.Fatalf("口令不符应 400，得 %d", status)
	}
	if got := len(r.staff.ListByProject("proj-g")); got != 1 {
		t.Fatalf("拒绝路径编制行不许动，剩 %d 行", got)
	}

	// 归档：重置拒绝（409 封存只读）；清退放行（删离编残留）
	if _, err := r.proj.Archive("proj-g"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-g/reset", "RESET"); status != http.StatusConflict || rep.OK {
		t.Fatalf("归档项目重置应 409，得 %d %+v", status, rep)
	}
	if status, rep := postPurgeHTTP(t, r.s, "/p/proj-g/dismiss", "DISMISS"); status != http.StatusOK || !rep.OK {
		t.Fatalf("归档项目清退残留应 200 ok，得 %d %+v", status, rep)
	}
	if got := len(r.staff.ListByProject("proj-g")); got != 0 {
		t.Fatalf("归档清退后编制行应清空，剩 %d 行", got)
	}
}
