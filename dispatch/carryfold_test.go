package dispatch

// 并车投递与重启自动接续的行为面（房主实录 14:52 五连点名后的两枚修）：
//   - 并车：一次注入除自己的线外可捎走车道里已过 StaleDelay 的陈线
//     （封顶 FoldCap 条，FIFO 从队头顺序取），读/回执货随线合并进本
//     轮；新鲜线（未过阈）不并——谁的回合谁见，逐条节奏不破；
//   - 接续：重启中断名单不再等人点卡——空车道注入一条 ContinueText，
//     压着停车道的成员走自己的车道；房还没起调度器的名单在 fleet
//     存桩，start 消费；归位（attach）即把恢复的停车道踢上路。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/staffing"
)

// TestStaleLaneFoldsIntoDelivery — 直投一条点名，车道里两条陈线折进
// 同一次注入（货合并），压在陈线后面的新鲜线不并——留给自己的回合。
func TestStaleLaneFoldsIntoDelivery(t *testing.T) {
	_, d, gb := startKickRoom(t)
	m := d.lookup("甲")
	old := time.Now().Add(-2 * time.Hour).Unix()
	d.enqueueAt(m, "陈线一", []int64{11}, []int64{11}, old, nil)
	d.enqueueAt(m, "陈线二", []int64{12}, []int64{12}, old+60, nil)
	d.enqueueAt(m, "新鲜线", []int64{13}, []int64{13}, time.Now().Unix(), nil)

	d.deliver("甲", "新来的点名", []int64{20}, []int64{20})

	texts := gb.sendTexts("s-kick-a")
	if len(texts) != 1 {
		t.Fatalf("应恰好一次注入，实到 %d", len(texts))
	}
	text := texts[0]
	for _, want := range []string{"新来的点名", "排队积压一并送达", "〔1〕", "陈线一", "〔2〕", "陈线二"} {
		if !strings.Contains(text, want) {
			t.Fatalf("并车注入缺 %q：%s", want, text)
		}
	}
	if strings.Contains(text, "新鲜线") {
		t.Fatal("新鲜线不该被并车——逐条节奏留给自己的回合")
	}
	d.mu.Lock()
	left := len(d.members["甲"].lanes.queue)
	reads := append([]int64(nil), d.members["甲"].cargo.readSeqs...)
	acks := append([]int64(nil), d.members["甲"].cargo.ackSeqs...)
	d.mu.Unlock()
	if left != 1 {
		t.Fatalf("车道应只剩新鲜线 1 条，剩 %d", left)
	}
	// 货合并：陈线的读/回执 seq 并进本轮——终点一次清账，无需各线各轮
	for _, want := range []int64{11, 12, 20} {
		if !containsSeq(reads, want) || !containsSeq(acks, want) {
			t.Fatalf("seq %d 未随并车进本轮货单（读 %v／回执 %v）", want, reads, acks)
		}
	}
}

func containsSeq(seqs []int64, want int64) bool {
	for _, s := range seqs {
		if s == want {
			return true
		}
	}
	return false
}

// TestFoldCapLimitsBatch — 深积压一轮只折 FoldCap 条：并车是提速不
// 是倾倒，剩余陈线下轮再捎。
func TestFoldCapLimitsBatch(t *testing.T) {
	_, d, gb := startKickRoom(t)
	m := d.lookup("甲")
	old := time.Now().Add(-3 * time.Hour).Unix()
	for i := 1; i <= 7; i++ {
		d.enqueueAt(m, "陈线"+strings.Repeat("又", i), []int64{int64(10 + i)}, []int64{int64(10 + i)}, old+int64(i)*60, nil)
	}
	d.deliver("甲", "新令", nil, nil)

	texts := gb.sendTexts("s-kick-a")
	if len(texts) != 1 {
		t.Fatalf("应恰好一次注入，实到 %d", len(texts))
	}
	text := texts[0]
	if !strings.Contains(text, "〔"+string(rune('0'+FoldCap))+"〕") {
		t.Fatalf("封顶前应折满 %d 条（缺第 %d 节）：%s", FoldCap, FoldCap, text)
	}
	d.mu.Lock()
	left := len(d.members["甲"].lanes.queue)
	d.mu.Unlock()
	if left != 7-FoldCap {
		t.Fatalf("并车后应剩 %d 条，剩 %d", 7-FoldCap, left)
	}
}

// TestContinueInterruptedPokes — 重启自动接续的调度器面：空车道成员
// 收到一条 ContinueText 注入；压着停车道的成员走自己的车道（不再另
// 注一条）；未归位的名字安静跳过（attach 已在房里报过）。
func TestContinueInterruptedPokes(t *testing.T) {
	_, d, gb := startKickRoom(t)
	d.enqueueAt(d.lookup("乙"), "车道上压着的接续线", []int64{7}, []int64{7}, time.Now().Unix(), nil)

	d.ContinueInterrupted([]string{"甲", "乙", "查无此人"})

	waitFor(t, func() bool { return gb.sendCount("s-kick-a") == 1 }, "空车道的甲应收到接续注入")
	if got := gb.sendTexts("s-kick-a")[0]; !strings.Contains(got, "自动接续") {
		t.Fatalf("甲的接续线口径走样：%s", got)
	}
	waitFor(t, func() bool { return gb.sendCount("s-kick-b") == 1 }, "乙应走自己的停车道")
	if got := gb.sendTexts("s-kick-b")[0]; !strings.Contains(got, "车道上压着的接续线") {
		t.Fatalf("乙的接续应走停车道里的线，不是另注一条：%s", got)
	}
}

// TestAdoptKicksRestoredLane — 归位即发车：attach 读出落盘车道后立刻
// 把队头踢上路，不等房主点名也不等第一拍看护。
func TestAdoptKicksRestoredLane(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	inbox := t.TempDir()
	// 槽位键是 projectKey——Start 把空 ProjectKey 补成 LobbyKey；
	// queue_at 缺席＝预戳文件（读零＝新鲜，与迟到头同一口径）
	lanes := map[string]map[string][]string{
		chat.LobbyKey: {"queue": []string{"车道上恢复的一条旧线", "预戳文件的第二条"}, "inject": []string{}},
	}
	b, err := json.Marshal(lanes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "丙.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	gb := newGateBridge()
	d := Start(hub, store, gb, Config{
		Workspace: t.TempDir(), InboxDir: inbox,
		PatrolEvery: -1, DailyAt: "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("丙", "工程师", "s-fold-c"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return gb.sendCount("s-fold-c") == 1 }, "归位即发车：恢复的停车线应立刻上路")
	if got := gb.sendTexts("s-fold-c")[0]; !strings.Contains(got, "车道上恢复的一条旧线") {
		t.Fatalf("上路的应是车道里那条线：%s", got)
	}
	// 零戳＝新鲜：预戳文件的第二条不并车（一轮一线），留在车道等下一轮
	if got := gb.sendTexts("s-fold-c")[0]; strings.Contains(got, "排队积压一并送达") || strings.Contains(got, "预戳文件的第二条") {
		t.Fatalf("零戳线按新鲜对待，不该被并车：%s", got)
	}
}

// TestFleetStashesContinueForRoomlessDispatchers — 无桥/无房的 fleet
// 收下中断名单进桩：大厅席位落 LobbyKey 槽，项目席位落项目槽——等
// start 消费。
func TestFleetStashesContinueForRoomlessDispatchers(t *testing.T) {
	f := inertFleet(t)
	f.ContinueInterrupted([]chat.InterruptedSeat{{Name: "甲"}, {Name: "乙", Project: "p-late"}})
	f.contMu.Lock()
	lob := append([]string(nil), f.contPending[chat.LobbyKey]...)
	late := append([]string(nil), f.contPending["p-late"]...)
	f.contMu.Unlock()
	if len(lob) != 1 || lob[0] != "甲" {
		t.Fatalf("Niuma_Studio 席位应存进 LobbyKey 槽：%v", lob)
	}
	if len(late) != 1 || late[0] != "乙" {
		t.Fatalf("项目席位应存进项目槽：%v", late)
	}
}

// TestFleetDrainsStashedContinueWhenRoomStarts — 迟到的大厅调度器
// （桥后到）：start 一旦起房就消费存桩——归位完成的中断成员立刻收
// 到接续注入，不再等房主点卡。
func TestFleetDrainsStashedContinueWhenRoomStarts(t *testing.T) {
	dir := t.TempDir()
	projs, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projs.EnsureLobby(dir); err != nil {
		t.Fatal(err) // 大厅项目随建（Open 不自动播种，Attach 的起步名单靠它）
	}
	staff, err := staffing.Open(filepath.Join(dir, "staffing.json"))
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	agentStore.Upsert("甲", "工程师", "", "")
	if !agentStore.BindSession("甲", "s-drain-a") {
		t.Fatal("预绑会话失败")
	}
	// fleet 的 adopt 走 staffing 分支（StaffStore 一挂就短路 v1 名册）：
	// 编制行在，甲才随 Start 归位，存桩点名才有落点
	if _, err := staff.Join(chat.LobbyKey, "甲", "工程师", "s-drain-a"); err != nil {
		t.Fatal(err)
	}
	f := StartFleet(nil, agentStore, projs, staff, nil, Config{
		Workspace: dir, Model: "GLM-5.3", Reasoning: "high",
		PatrolEvery: -1, DailyAt: "off",
		InboxDir: t.TempDir(), // 车道落盘隔离——别写进真实的 ~/.niuma/inbox
	})
	f.ContinueInterrupted([]chat.InterruptedSeat{{Name: "甲"}})

	hub := chat.NewHub()
	gb := newGateBridge()
	if !f.Attach(gb, map[string]*chat.Hub{chat.LobbyKey: hub}) {
		t.Fatal("Attach 应成功（惰性 fleet 装桥）")
	}
	d := f.Lobby()
	if d == nil {
		t.Fatal("Niuma_Studio 调度器应已起")
	}
	t.Cleanup(d.Stop)
	waitFor(t, func() bool { return gb.sendCount("s-drain-a") == 1 }, "起房应消费存桩：甲的接续注入上路")
	if got := gb.sendTexts("s-drain-a")[0]; !strings.Contains(got, "自动接续") {
		t.Fatalf("接续线口径走样：%s", got)
	}
}
