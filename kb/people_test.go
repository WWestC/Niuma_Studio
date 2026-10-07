package kb

// People's cross-room assembly (v2 multi-room sync): a member seats
// into their OWN room's hub, so the people truth must walk every room
// — the lobby-only read this replaces stranded every project-room
// member offline on the people board while the sidebar roster (fed by
// the project room's own WS) showed them present. Pins the merge
// ladder: live seat anywhere > grace ghost > departed memory > saved
// config, and the Room field that names which office a seat lives in.

import (
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
)

// find returns the summary for name, or nil.
func find(list []PersonSummary, name string) *PersonSummary {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// TestPeopleCountsProjectRoomSeats pins the fix itself: a project-room
// member is online with Room naming the project, not stranded offline
// by a lobby-only read.
func TestPeopleCountsProjectRoomSeats(t *testing.T) {
	lobby, proj := chat.NewHub(), chat.NewHub()
	lobby.Join("房主", "boss", false)
	lobby.Join("小明", "后端·项目甲", false)
	proj.Join("小红", "前端·项目甲", false)

	people := People([]Room{
		{Key: chat.LobbyKey, Roster: lobby},
		{Key: "proj-a", Roster: proj},
	}, nil, "房主", nil)

	if p := find(people, "小红"); p == nil || !p.Online || p.Grace || p.Room != "proj-a" {
		t.Fatalf("项目房成员小红应在线且记房 proj-a（旧 bug：只读 Niuma_Studio 把她留在离线）：got %+v", find(people, "小红"))
	}
	if p := find(people, "小明"); p == nil || !p.Online || p.Room != chat.LobbyKey {
		t.Fatalf("Niuma_Studio 成员小明的房间应记为 Niuma_Studio，got %+v", find(people, "小明"))
	}
	if p := find(people, "房主"); p == nil || !p.Local {
		t.Fatal("房主应带 local 标")
	}
}

// TestPeopleLiveSeatBeatsGhost: the same name holding a live seat in
// one room and a grace ghost in another is online, not grace.
func TestPeopleLiveSeatBeatsGhost(t *testing.T) {
	lobby, proj := chat.NewHub(), chat.NewHub()
	// 小红 leaves the lobby into its presence grace (NewHub's default
	// grace keeps her a ghost), then is seated live in the project room.
	c, _ := lobby.Join("小红", "前端", false)
	lobby.Leave(c)
	if len(lobby.GraceNames()) != 1 {
		t.Fatalf("前置失败：小红应成 Niuma_Studio 幽灵，got %v", lobby.GraceNames())
	}
	proj.Join("小红", "前端·项目甲", false)

	people := People([]Room{
		{Key: chat.LobbyKey, Roster: lobby},
		{Key: "proj-a", Roster: proj},
	}, nil, "", nil)
	if p := find(people, "小红"); p == nil || !p.Online || p.Grace {
		t.Fatalf("任一房间的活座应压过别处幽灵：got %+v", find(people, "小红"))
	}
}

// TestPeopleGhostAndDepartedAcrossRooms: a ghost anywhere stays
// online+grace with its room named; a true departure (grace expired or
// kicked) falls back offline.
func TestPeopleGhostAndDepartedAcrossRooms(t *testing.T) {
	lobby, proj := chat.NewHub(), chat.NewHub()
	ghost, _ := lobby.Join("小明", "后端", false)
	lobby.Leave(ghost) // grace ghost, still rostered
	proj.Join("老王", "顾问", false)
	proj.Kick("老王") // departed, no grace

	people := People([]Room{
		{Key: chat.LobbyKey, Roster: lobby},
		{Key: "proj-a", Roster: proj},
	}, nil, "", nil)
	if p := find(people, "小明"); p == nil || !p.Online || !p.Grace || p.Room != chat.LobbyKey {
		t.Fatalf("Niuma_Studio 幽灵小明应在线宽限并记房：got %+v", find(people, "小明"))
	}
	if p := find(people, "老王"); p == nil || p.Online {
		t.Fatalf("被踢的老王应回落离线兜底行：got %+v", find(people, "老王"))
	}
}

// TestPeopleNewestIdentityWins: when the same name somehow seats twice
// (the -2 discipline usually prevents it), the fresher report carries
// the identity.
func TestPeopleNewestIdentityWins(t *testing.T) {
	lobby, proj := chat.NewHub(), chat.NewHub()
	a, _ := lobby.Join("小明", "后端·旧岗", false)
	b, _ := proj.Join("小明", "后端·新岗", false)
	// reports bump LastReport/TS; the project seat reports later
	// (LastReportTS is second-granular, so hop the boundary first)
	lobby.Report(a, "Niuma_Studio 的汇报")
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 20*time.Millisecond)))
	proj.Report(b, "项目房的汇报")

	people := People([]Room{
		{Key: chat.LobbyKey, Roster: lobby},
		{Key: "proj-a", Roster: proj},
	}, nil, "", nil)
	if p := find(people, "小明"); p == nil || p.LastReport != "项目房的汇报" {
		t.Fatalf("最新汇报的座位应携带身份：got %+v", find(people, "小明"))
	}
}
