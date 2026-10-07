package server

// v2.9 项目隔离的读面/门禁钉：?project= 收口后的花名册与文档架、kb
// 写面按编制占用的本房门禁、plan/task 指派的本项目在册校验。口径：
// 彻底隔离——别家项目的人与单不可见，公共文档库全室共享。

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// startIsolatedStudio boots the two-project studio the scope tests
// drink from: lobby (房主＋大厅成员) + proj-a + proj-b rooms, staffing
// on, docs store seeded with one public, one proj-a and one proj-b
// doc, task engine with one task per project.
func startIsolatedStudio(t *testing.T) (*Server, *chat.Hub) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	for _, key := range []string{"proj-a", "proj-b"} {
		if _, err := projStore.Create(projects.Project{Key: key, Name: key,
			Workspace: t.TempDir() + "/" + key}); err != nil {
			t.Fatalf("Create %s: %v", key, err)
		}
		if _, err := projStore.Activate(key); err != nil {
			t.Fatalf("Activate %s: %v", key, err)
		}
	}
	staff, err := staffing.Open("")
	if err != nil {
		t.Fatalf("staffing.Open: %v", err)
	}
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	for key, body := range map[string]string{
		"readme":        "公共 readme",
		"p/proj-a/spec": "甲的私档",
		"p/proj-b/spec": "乙的私档",
	} {
		if _, err := docs.Write(key, key, body, "房主"); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	engine := tasks.MustOpenMemory("房主")
	engine.SetProjectKeys(func() []string { return []string{chat.LobbyKey, "proj-a", "proj-b"} })
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			StaffStore: staff,
			Docs:       docs,
			Engine:     engine,
		},
		Registry:  registry,
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	hubA, err := registry.Hub("proj-a")
	if err != nil {
		t.Fatalf("hub proj-a: %v", err)
	}
	hubB, err := registry.Hub("proj-b")
	if err != nil {
		t.Fatalf("hub proj-b: %v", err)
	}
	lobby.Join("Niuma_Studio 小明", "前台", false)
	hubA.Join("小红", "前端", false)
	hubB.Join("小蓝", "后端", false)
	// 在册未入座（招聘中）的成员：只落编制行
	if _, err := staff.Join("proj-a", "小紫", "测试", ""); err != nil {
		t.Fatalf("staff.Join: %v", err)
	}
	// 每项目一条任务
	out := engine.Create("", "房主", "甲的任务", "", "小红")
	if out.Denied {
		t.Fatalf("seed task a: %s", out.Reason)
	}
	if out = engine.Update("", "房主", out.Task.ID, tasks.Patch{Project: "proj-a"}); out.Denied {
		t.Fatalf("attach task a: %s", out.Reason)
	}
	out = engine.Create("", "房主", "乙的任务", "", "小蓝")
	if out.Denied {
		t.Fatalf("seed task b: %s", out.Reason)
	}
	if out = engine.Update("", "房主", out.Task.ID, tasks.Patch{Project: "proj-b"}); out.Denied {
		t.Fatalf("attach task b: %s", out.Reason)
	}
	return s, lobby
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

// TestScopedPeople：?project= 的花名册只见本房在座＋在册＋房主，
// 别家成员与大厅过客不进；房主恒在册。
func TestScopedPeople(t *testing.T) {
	s, _ := startIsolatedStudio(t)
	var people []kb.PersonSummary
	getJSON(t, addrHTTP(s)+"/kb/people?project=proj-a", &people)
	names := map[string]bool{}
	for _, p := range people {
		names[p.Name] = true
	}
	for _, want := range []string{"小红", "小紫", "房主"} {
		if !names[want] {
			t.Fatalf("proj-a 花名册应含 %s，got %v", want, names)
		}
	}
	for _, stranger := range []string{"小蓝", "Niuma_Studio 小明"} {
		if names[stranger] {
			t.Fatalf("proj-a 花名册不该见 %s，got %v", stranger, names)
		}
	}
	// 大厅也是独立一间房：大厅范围的花名册只见大厅在座＋房主，
	// 不见任何项目成员
	var hall []kb.PersonSummary
	getJSON(t, addrHTTP(s)+"/kb/people?project=default", &hall)
	hallNames := map[string]bool{}
	for _, p := range hall {
		hallNames[p.Name] = true
	}
	for _, want := range []string{"Niuma_Studio 小明", "房主"} {
		if !hallNames[want] {
			t.Fatalf("Niuma_Studio 花名册应含 %s，got %v", want, hallNames)
		}
	}
	for _, stranger := range []string{"小红", "小蓝", "小紫"} {
		if hallNames[stranger] {
			t.Fatalf("Niuma_Studio 花名册不该见项目侧 %s，got %v", stranger, hallNames)
		}
	}
	// 无参仍是全室面（房主的口径）
	getJSON(t, addrHTTP(s)+"/kb/people", &people)
	names = map[string]bool{}
	for _, p := range people {
		names[p.Name] = true
	}
	for _, want := range []string{"小红", "小蓝", "Niuma_Studio 小明", "房主"} {
		if !names[want] {
			t.Fatalf("全室花名册应含 %s，got %v", want, names)
		}
	}
	// 详情门同口径：别家成员 404，本房成员 200
	if code := getJSON(t, addrHTTP(s)+"/kb/people/小蓝?project=proj-a", nil); code != 404 {
		t.Fatalf("proj-a 读别家成员详情应 404，got %d", code)
	}
	if code := getJSON(t, addrHTTP(s)+"/kb/people/小紫?project=proj-a", nil); code != 200 {
		t.Fatalf("proj-a 读在册未入座成员应 200，got %d", code)
	}
}

// TestScopedDocs：?project= 的文档索引＝公共＋本房；别家 p/<key>/ 与
// 大厅 ops/ 私档的单篇读取与历史链都 404——大厅也是独立一间房。
func TestScopedDocs(t *testing.T) {
	s, _ := startIsolatedStudio(t)
	if _, err := s.opts.Stores.Docs.Write("ops/chronicle", "Niuma_Studio 志", "Niuma_Studio 的私档", "房主"); err != nil {
		t.Fatalf("seed ops/chronicle: %v", err)
	}
	var metas []kb.DocMeta
	getJSON(t, addrHTTP(s)+"/kb/docs?project=proj-a", &metas)
	keys := map[string]bool{}
	for _, m := range metas {
		keys[m.Key] = true
	}
	for _, want := range []string{"readme", "p/proj-a/spec"} {
		if !keys[want] {
			t.Fatalf("proj-a 文档架应含 %s，got %v", want, keys)
		}
	}
	for _, stranger := range []string{"p/proj-b/spec", "ops/chronicle"} {
		if keys[stranger] {
			t.Fatalf("proj-a 文档架不该见 %s（别家私架/Niuma_Studio 私档），got %v", stranger, keys)
		}
	}
	if code := getJSON(t, addrHTTP(s)+"/kb/docs/p/proj-b/spec?project=proj-a", nil); code != 404 {
		t.Fatalf("读别家文档应 404，got %d", code)
	}
	if code := getJSON(t, addrHTTP(s)+"/kb/docs/ops/chronicle?project=proj-a", nil); code != 404 {
		t.Fatalf("项目读 Niuma_Studio 私档应 404，got %d", code)
	}
	if code := getJSON(t, addrHTTP(s)+"/kb/docs/p/proj-b/spec/history?project=proj-a", nil); code != 404 {
		t.Fatalf("读别家文档历史应 404，got %d", code)
	}
	if code := getJSON(t, addrHTTP(s)+"/kb/docs/readme?project=proj-a", nil); code != 200 {
		t.Fatalf("公共文档在项目范围可读，got %d", code)
	}
	// 大厅范围：公共＋自己的 ops/ 私档，不见项目架
	getJSON(t, addrHTTP(s)+"/kb/docs?project=default", &metas)
	keys = map[string]bool{}
	for _, m := range metas {
		keys[m.Key] = true
	}
	if !keys["readme"] || !keys["ops/chronicle"] {
		t.Fatalf("Niuma_Studio 架应含公共＋ops/ 私档，got %v", keys)
	}
	if keys["p/proj-a/spec"] || keys["p/proj-b/spec"] {
		t.Fatalf("Niuma_Studio 不见项目架，got %v", keys)
	}
}

// TestScopedTaskFaces：?project= 的任务列表与单卡只见本房任务，
// 别家单卡 404、不确认存在性。
func TestScopedTaskFaces(t *testing.T) {
	s, _ := startIsolatedStudio(t)
	var list []tasks.Task
	getJSON(t, addrHTTP(s)+"/kb/tasks?project=proj-a", &list)
	if len(list) != 1 || list[0].Title != "甲的任务" {
		t.Fatalf("proj-a 任务列表应只含甲的任务，got %+v", list)
	}
	idB := ""
	for _, tk := range s.opts.Stores.Engine.List("", "") {
		if tk.Title == "乙的任务" {
			idB = tk.ID
		}
	}
	if idB == "" {
		t.Fatal("找不到乙的任务种子")
	}
	if code := getJSON(t, addrHTTP(s)+"/kb/tasks/"+idB+"?project=proj-a", nil); code != 404 {
		t.Fatalf("proj-a 读别家任务应 404，got %d", code)
	}
}

// TestKbWriteGate：写面按编制占用——座位只写本房拥有的键（项目＝
// p/<key>/，大厅＝ops/＋p/default/），公共文档库全室共享、房主执笔；
// 房主全通。
func TestKbWriteGate(t *testing.T) {
	s, _ := startIsolatedStudio(t)
	if err := s.kbWriteGate("小红", "p/proj-a/spec"); err != nil {
		t.Fatalf("甲座写甲架不该拒：%v", err)
	}
	if err := s.kbWriteGate("小红", "p/proj-b/spec"); err == nil {
		t.Fatal("甲座写乙架应拒")
	}
	if err := s.kbWriteGate("小红", "readme"); err == nil {
		t.Fatal("甲座写公共架应拒（公共归房主）")
	}
	if err := s.kbWriteGate("小红", "ops/chronicle"); err == nil {
		t.Fatal("甲座写 Niuma_Studio 私档应拒")
	}
	if err := s.kbWriteGate("房主", "p/proj-b/spec"); err != nil {
		t.Fatalf("房主写任何架不该拒：%v", err)
	}
	if err := s.kbWriteGate("房主", "readme"); err != nil {
		t.Fatalf("房主写公共架不该拒：%v", err)
	}
	// 大厅座（大厅小明坐在大厅 hub）：写自己的 ops/ 与 p/default/ 可，
	// 公共与项目架拒——大厅不能影响其他独立项目。
	if err := s.kbWriteGate("Niuma_Studio 小明", "ops/chronicle"); err != nil {
		t.Fatalf("Niuma_Studio 座写 Niuma_Studio 私档不该拒：%v", err)
	}
	if err := s.kbWriteGate("Niuma_Studio 小明", "p/default/x"); err != nil {
		t.Fatalf("Niuma_Studio 座写 p/default/ 防御位不该拒：%v", err)
	}
	if err := s.kbWriteGate("Niuma_Studio 小明", "readme"); err == nil {
		t.Fatal("Niuma_Studio 座写公共架应拒（公共归房主）")
	}
	if err := s.kbWriteGate("Niuma_Studio 小明", "p/proj-a/spec"); err == nil {
		t.Fatal("Niuma_Studio 座写项目架应拒")
	}
}

// TestPlanAssigneeScope：提案指派只认本项目在册（在座/编制中/房主），
// 别家成员拒；改派同样校验。
func TestPlanAssigneeScope(t *testing.T) {
	s, _ := startIsolatedStudio(t)
	p := &plan.Plan{ProjectKey: "proj-a", Tasks: []plan.PlanTask{
		{Title: "a", Assignee: "小红"},
		{Title: "b", Assignee: "房主"},
		{Title: "c", Assignee: "小紫"},
	}}
	if err := s.plansFace().CheckPlanRefs(p); err != nil {
		t.Fatalf("在册指派不该拒：%v", err)
	}
	p.Tasks = append(p.Tasks, plan.PlanTask{Title: "d", Assignee: "小蓝"})
	if err := s.plansFace().CheckPlanRefs(p); err == nil {
		t.Fatal("指派别家成员小蓝应拒")
	}
	p.Tasks = p.Tasks[:3]
	p.Tasks = append(p.Tasks, plan.PlanTask{Title: "e", Assignee: "Niuma_Studio 小明"})
	if err := s.plansFace().CheckPlanRefs(p); err == nil {
		t.Fatal("指派 Niuma_Studio 过客应拒（不在 proj-a 编制）")
	}

	// 名单判定（改派门与提案门共用的同一函数）：在座/房主/编制中认，
	// 别家成员与过客不认。
	if s.projectRosterHas("proj-a", "小蓝") {
		t.Fatal("projectRosterHas 不该认别家成员")
	}
	if !s.projectRosterHas("proj-a", "小红") || !s.projectRosterHas("proj-a", "房主") || !s.projectRosterHas("proj-a", "小紫") {
		t.Fatal("projectRosterHas 应认在座/房主/编制中")
	}
}
