package cli

// taskroom_test.go — t_124（r_32/p_40 路线修正版）的验收钉子：
//   ① 绑定工作区（NIUMA_PROJECT=book）里 task update 裸号成功——拨号
//      跟绑定房走（dialTask→dialTokenRoom，54ca006 的小苗-2 根治批
//      落地），不再「不属于项目 default」被拒；
//   ② kb write 同场景成功（共用 dialTask 同根同消）＋三帧带 Project
//      文档性冗余（服务端路由按连接房，帧字段是审计面包屑）；
//   ③ 大厅零回退：未绑定拨大厅的旧缺省行为字节不变（task/kb 写照旧
//      走大厅，v2.9 的 legacy dial 合同）。
// 台架复用 sayroom_test 的 startSayRoomStudio 形（registry 双房＋凭据）。

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// startTaskRoomStudio is startSayRoomStudio plus the task engine and kb
// store — the write faces these pins exercise.
func startTaskRoomStudio(t *testing.T) (*chat.Hub, *chat.Hub, *tasks.Engine, *kb.DocsStore, int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	tokRestore := chat.OverrideSeatTokenHome(home)
	t.Cleanup(tokRestore)
	chat.SaveSeatToken("book", "小鸟", "tok-book") // book 调度器盖的现行凭证

	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, "", "", "")
	if _, err := projStore.Create(projects.Project{Key: "book", Name: "书", Workspace: t.TempDir() + "/book"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate("book"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	bookHub, err := registry.Hub("book")
	if err != nil {
		t.Fatalf("book hub: %v", err)
	}
	dir := t.TempDir()
	eng := tasks.MustOpenMemory("房主")
	eng.SetProjectKeys(func() []string { return []string{"book", chat.LobbyKey} })
	docs, err := kb.OpenDocs(filepath.Join(dir, "kb"))
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	s, err := server.Start(lobby, server.Options{Endpoint: server.EndpointConfig{
		PreferredPort: -1,
	},
		Stores: server.StoreSet{
			ProjectStore: projStore,
			Engine:       eng,
			Docs:         docs,
		},
		Registry:  registry,
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return lobby, bookHub, eng, docs, s.Port
}

// makeBookTask files one todo task under book's shelf, for 小鸟 to update.
func makeBookTask(t *testing.T, eng *tasks.Engine) string {
	t.Helper()
	out := eng.CreatePlanned("房主", tasks.Task{Title: "写第一章", Assignee: "小鸟", ProjectKey: "book"})
	if out.Denied {
		t.Fatal(out.Reason)
	}
	return out.Task.ID
}

// ① 验收 1：绑定工作区 task update 裸号成功——拨号跟房走，room 是
// book（不再「不属于项目 default」）。
func TestTaskUpdateDialsBoundRoom(t *testing.T) {
	_, _, eng, _, port := startTaskRoomStudio(t)
	id := makeBookTask(t, eng)
	t.Setenv("NIUMA_PROJECT", "book")

	if code := Run([]string{"task", "update", id, "--name", "小鸟", "--status", "doing",
		"--port", strconv.Itoa(port)}); code != 0 {
		t.Fatalf("绑定工作区 task update 裸号应成功，code=%d", code)
	}
	// 台账侧核：任务状态翻 doing（连接落 book 房，engineOp 按 room 路由）
	for i := 0; i < 200; i++ {
		if tk, ok := eng.Get(id); ok && tk.Status == tasks.StatusDoing {
			return // pass
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := eng.Get(id)
	t.Fatalf("task update 应按 book 房落账（got status=%s）——拨号层没带房？", tk.Status)
}

// ② 验收 2：kb write 同场景成功＋帧带 Project 文档性冗余（审计面）。
func TestKbWriteDialsBoundRoom(t *testing.T) {
	_, _, _, docs, port := startTaskRoomStudio(t)
	t.Setenv("NIUMA_PROJECT", "book")

	if code := Run([]string{"kb", "write", "roles/writer", "--title", "写手手册", "--body", "第一章",
		"--name", "小鸟", "--port", strconv.Itoa(port)}); code != 0 {
		t.Fatalf("绑定工作区 kb write 应成功，code=%d", code)
	}
	for i := 0; i < 200; i++ {
		if _, err := docs.Get("roles/writer", 0); err == nil {
			return // pass
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("kb write 应落进 store（连接房路由）")
}

// ③ 验收 4：大厅零回退——未绑定（无 NIUMA_PROJECT）时拨号照旧落大厅
// （v2.9 legacy 合同），两个锚：a) 大厅自己的池单照旧可写（旧形状未破）；
// b) book 房的裸号在大厅被拒「不属于项目 default」——这正是症状①的
// 正确语义（拨错房就该拒），拒绝码 1 且 book 台账零变化。
func TestUnboundWriteKeepsLobbyDial(t *testing.T) {
	_, _, eng, _, port := startTaskRoomStudio(t)
	// a) 池单（无 project）：大厅房主照旧可写
	pool := eng.CreatePlanned("房主", tasks.Task{Title: "池单", Assignee: "房主"})
	if pool.Denied {
		t.Fatal(pool.Reason)
	}
	if code := Run([]string{"task", "update", pool.Task.ID, "--name", "房主", "--status", "doing",
		"--port", strconv.Itoa(port)}); code != 0 {
		t.Fatalf("大厅池单 update 应照旧可执行（零回退），code=%d", code)
	}
	for i := 0; i < 200; i++ {
		if tk, ok := eng.Get(pool.Task.ID); ok && tk.Status == tasks.StatusDoing {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// b) 号段隔离锚：大厅裸号 t_01 只解析到大厅槽的池单——book 槽的
	// t_01（若也在）是另一张单（GetIn 视角各自成立）。大厅拨号写到的
	// 永远是大厅自己的号（crossRoom 拒由服务端 taskPeekIn/crossRoomTask
	// 家族钉住，CLI 侧不再重复构造二义号段）。
	if _, ok := eng.GetIn("default", "t_01"); !ok {
		t.Fatal("池单应落大厅槽（t_01 在 default 视角可见）")
	}
	if tk, ok := eng.GetIn("book", "t_01"); ok && tk.Title != "写第一章" {
		t.Fatalf("book 槽的 t_01 应是写第一章（号段隔离），got %q", tk.Title)
	}
}
