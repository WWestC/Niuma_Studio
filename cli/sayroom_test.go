package cli

// say 落座拨号跟工作区绑定房走（小苗-2 根治①）：成员的座位凭据只在
// 本人房间 redeem——从项目工作区跑 `niuma say` 必须拨自己的房，而不是
// 旧缺省的大厅。事故实录：book 的编排者每回合 `niuma say` 都落在大厅，
// 撞上迁房前留下的（被暂停房冻成永生的）宽限幽灵，被顶成「小苗-2」。
// 反向也钉住：不带绑定时的旧缺省拨大厅，门口现在拒绝而不是产分身。

import (
	"strconv"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server"
)

// startSayRoomStudio boots one registry server: the lobby hub carries a
// restored 小苗 ghost with the OLD room's token, and the book project
// room is active. The seat credential file points at a scratch home
// holding the NEW room's token — the exact migration-day state. It
// returns the lobby hub, the book hub and the server's port.
func startSayRoomStudio(t *testing.T) (*chat.Hub, *chat.Hub, int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	tokRestore := chat.OverrideSeatTokenHome(home)
	t.Cleanup(tokRestore)
	chat.SaveSeatToken("book", "小苗", "8d06-new") // 现行凭证：book 调度器盖的

	lobby := chat.NewHub()
	// 迁房前的大厅幽灵（快照复活形态）：token 是旧的 e5d0。
	lobby.RestoreSeats([]chat.SeatSnapshot{{Name: "小苗", Role: "排期编排", Token: "e5d0-old"}})

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
	s, err := server.Start(lobby, server.Options{Endpoint: server.EndpointConfig{PreferredPort: -1}, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return lobby, bookHub, s.Port
}

func hasSayFrom(h *chat.Hub, name string) bool {
	for _, m := range h.History() {
		if m.Type == chat.MsgSay && m.From == name {
			return true
		}
	}
	return false
}

// waitSayFrom polls the hub until name's line lands (the server's read
// loop processes the CLI's frames a beat after Run returns) or the
// deadline passes.
func waitSayFrom(t *testing.T, h *chat.Hub, name string) bool {
	t.Helper()
	for i := 0; i < 200; i++ {
		if hasSayFrom(h, name) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestSayDialsWorkspaceBoundRoom(t *testing.T) {
	lobby, book, port := startSayRoomStudio(t)
	t.Setenv("NIUMA_PROJECT", "book")

	if code := Run([]string{"say", "--name", "小苗", "产线就绪", "--port", strconv.Itoa(port)}); code != 0 {
		t.Fatalf("say 应成功落自己的房，code=%d", code)
	}
	if !waitSayFrom(t, book, "小苗") {
		t.Fatalf("发言应落在绑定房 book（署本名）：%+v | lobby=%+v", book.History(), lobby.History())
	}
	for _, m := range lobby.History() {
		if m.Type == chat.MsgRename || m.From == "小苗" {
			t.Fatalf("大厅不得再出现 小苗/-2 痕迹：%+v", m)
		}
	}
	if lobby.HasLiveMember("小苗-2") {
		t.Fatal("大厅不得出现 -2 分身")
	}
}

// 旧缺省（未绑定 cwd）拨大厅：门口拒绝错 token 拨幽灵，零分身。
func TestSayUnboundLobbyDialRefusedAtDoor(t *testing.T) {
	lobby, _, port := startSayRoomStudio(t)

	if code := Run([]string{"say", "--name", "小苗", "汇报", "--port", strconv.Itoa(port)}); code != 1 {
		t.Fatalf("撞大厅幽灵的免绑定 say 应被拒（code 1），got %d", code)
	}
	for _, m := range lobby.History() {
		if m.Type == chat.MsgRename {
			t.Fatalf("被拒拨号不得产生改名广播：%+v", m)
		}
	}
	if lobby.HasLiveMember("小苗-2") || lobby.HasLiveMember("小苗") {
		t.Fatal("被拒拨号不得留下座位")
	}
}
