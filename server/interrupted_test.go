package server

// 中断接续的快照往返（v2.5）：writeSnapshot 落盘在飞席位 →
// LoadRoomSnapshot 恢复幽灵名册的同时把被中断的席位报出来——重启
// 那棒（main.go）拿这份名单按席位去向分房播「让 TA 继续」卡
// （v2.12 卡跟房走：大厅只收大厅自己的席位）。缺席/坏档冷启动
// 是既有铁律：绝不能因提示缺失拒绝开机。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/staffing"
)

func TestSnapshotInterruptedRoundTrip(t *testing.T) {
	h := chat.NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwnerSeat(owner) // 生产语义：房主自己的席不进快照（Seats 跳过 ownerClient）
	_, _ = h.Join("小牛", "编排者", false)
	乙, _ := h.Join("小马", "HR", false)
	h.SetWorkState("小牛", true, 12345) // 干到一半被退出
	h.SetWorkState("小马", false, 0)    // 闲着的不算被中断
	_ = owner

	s := &Server{}
	path := filepath.Join(t.TempDir(), "room.json")
	s.writeSnapshotTo(path, h)

	fresh := chat.NewHub()
	fresh.SetOwner("房主") // 与启动序一致：恢复前房主已定名
	got := LoadRoomSnapshot(path, fresh, "", nil)
	if len(got) != 1 || got[0].Name != "小牛" || got[0].Since != 12345 {
		t.Fatalf("被中断席位该恰好报出小牛: %+v", got)
	}
	// 名册照旧恢复成幽灵位（在飞标志不得漏到幽灵上——恢复的座位在
	// 调度器回来之前哪儿也不干活）。
	if len(fresh.Seats()) != 2 {
		t.Fatalf("幽灵名册应含两席: %+v", fresh.Seats())
	}
	for _, st := range fresh.Seats() {
		if st.Working {
			t.Fatalf("恢复的幽灵不该带在飞状态: %+v", st)
		}
	}
	_ = 乙
}

func TestLoadRoomSnapshotColdStartStillQuiet(t *testing.T) {
	fresh := chat.NewHub()
	// 缺席：冷启动，无提示。
	if got := LoadRoomSnapshot(filepath.Join(t.TempDir(), "absent.json"), fresh, "", nil); got != nil {
		t.Fatalf("缺席快照不该报中断: %+v", got)
	}
	// 坏档：改名搁置、冷启动——提示缺失绝不拒绝开机。
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadRoomSnapshot(bad, fresh, "", nil); got != nil {
		t.Fatalf("坏档快照不该报中断: %+v", got)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("坏档应被改名搁置: %v", err)
	}
}

// 小苗-2 根治③：快照恢复对账编制——名字已在他房在编（active）的
// 座位不再恢复成此房幽灵。事故实录：小苗从大厅迁到 book，大厅快照
// 每次重启都复活它的残留幽灵，暂停房又把宽限钟冻成永生，成员每次
// CLI 拨大厅都被顶成「小苗-2」。本房在编/无编制的座位照旧恢复。
func TestLoadRoomSnapshotDropsForeignStaffedSeats(t *testing.T) {
	h := chat.NewHub()
	owner, _ := h.Join("房主", "", false)
	h.SetOwnerSeat(owner)
	_, _ = h.Join("小苗", "排期编排", false)
	_, _ = h.Join("小牛", "排期编排", false)
	_, _ = h.Join("小笔", "写手", false)

	s := &Server{}
	path := filepath.Join(t.TempDir(), "room.json")
	s.writeSnapshotTo(path, h)

	// 编制真相：小苗、小笔在 book 上班（active）；小牛在本房（default）。
	staff, err := staffing.Open(filepath.Join(t.TempDir(), "staffing.json"))
	if err != nil {
		t.Fatalf("staffing open: %v", err)
	}
	if _, err := staff.Join("book", "小苗", "排期编排", "sess-1"); err != nil {
		t.Fatalf("join 小苗: %v", err)
	}
	if _, err := staff.Join("book", "小笔", "写手", "sess-2"); err != nil {
		t.Fatalf("join 小笔: %v", err)
	}
	if _, err := staff.Join("default", "小牛", "排期编排", "sess-3"); err != nil {
		t.Fatalf("join 小牛: %v", err)
	}

	fresh := chat.NewHub()
	fresh.SetOwner("房主")
	_ = LoadRoomSnapshot(path, fresh, "default", staff)
	names := map[string]bool{}
	for _, st := range fresh.Seats() {
		names[st.Name] = true
	}
	if names["小苗"] || names["小笔"] {
		t.Fatalf("他房在编座位不该恢复成幽灵：%v", names)
	}
	if !names["小牛"] {
		t.Fatalf("本房在编座位照旧恢复：%v", names)
	}
}
