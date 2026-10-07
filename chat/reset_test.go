package chat

// reset_test.go — ResetDurable 的库存契约：历史文件与三台账文件
// （reads/acks/reacts）连同终端目录一并删除、内存环与 departed/
// ghosts 清空；seq 单调不回卷（新帧编号越过旧人群）；三台账 floor
// 抬到旧 seq 之后——旧回执静默丢弃、不复活任何行。Forget 是清退流程
// 的点名版：只清指定名字的离场快照与幽灵。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetDurableWipesFilesStateAndFloors(t *testing.T) {
	dir := t.TempDir()
	h := NewHub()
	h.SetOwner("房主")
	h.SetHistoryPath(filepath.Join(dir, "room.jsonl"))
	h.SetTermDir(filepath.Join(dir, "term"))

	h.Join("小明", "后端", false)
	b, _ := h.Join("小红", "前端", false)
	msg, ok := h.Say(b, "艾特小明的一句话")
	if !ok {
		t.Fatal("前置失败：Say 被拒")
	}
	h.MarkRead("小明", msg.Seq)
	h.MarkAck("小明", msg.Seq) // 自己的行不记回执——用别人的行
	h.MarkReact("小明", msg.Seq, "👍", true)
	h.AppendTerm([]TraceEntry{{Kind: "in", From: "小明", Text: "旧输入"}})
	seqBefore := h.LastSeq()
	h.FlushHistory() // 历史与三账本都是异步落盘：断言读盘前先排干
	h.FlushLedgers()

	for _, p := range []string{
		filepath.Join(dir, "room.jsonl"),
		filepath.Join(dir, "room.reads.json"),
		filepath.Join(dir, "room.acks.json"),
		filepath.Join(dir, "room.reacts.json"),
		filepath.Join(dir, "term"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("前置失败： %s 应已落盘 (%v)", p, err)
		}
	}

	if err := h.ResetDurable(); err != nil {
		t.Fatalf("ResetDurable: %v", err)
	}
	for _, p := range []string{
		filepath.Join(dir, "room.jsonl"),
		filepath.Join(dir, "room.reads.json"),
		filepath.Join(dir, "room.acks.json"),
		filepath.Join(dir, "room.reacts.json"),
		filepath.Join(dir, "term"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s 重置后应消失（stat err=%v）", p, err)
		}
	}
	if got := h.History(); len(got) != 0 {
		t.Fatalf("内存环应清空，剩 %d 帧", len(got))
	}
	if _, rows := h.ReadReceipts(); len(rows) != 0 {
		t.Fatalf("已读台账应清空，剩 %d 行", len(rows))
	}
	if _, rows := h.AckReceipts(); len(rows) != 0 {
		t.Fatalf("收到台账应清空，剩 %d 行", len(rows))
	}
	if _, rows := h.ReactReceipts(); len(rows) != 0 {
		t.Fatalf("表情台账应清空，剩 %d 行", len(rows))
	}
	if got := h.TermSnapshot("小明", 10); len(got) != 0 {
		t.Fatalf("终端转录应清空，剩 %d 行", len(got))
	}
	// 旧 seq 的回执落在 floor 之下：静默丢弃，不复活任何行、不 panic
	h.MarkRead("小明", msg.Seq)
	h.MarkAck("小明", msg.Seq)
	h.MarkReact("小明", msg.Seq, "👍", true)
	if _, rows := h.ReadReceipts(); len(rows) != 0 {
		t.Fatalf("floor 之下的旧回执不得复活行，得 %d 行", len(rows))
	}
	// seq 不回卷：新帧的编号越过旧人群
	h.SystemRecorded("重置后的第一帧")
	if after := h.LastSeq(); after <= seqBefore {
		t.Fatalf("重置不回卷 seq：前 %d 后 %d", seqBefore, after)
	}
	if got := h.History(); len(got) != 1 || got[0].Text != "重置后的第一帧" {
		t.Fatalf("重置后历史应从回执开卷，得 %+v", got)
	}
}

func TestForgetDropsDepartedAndGhosts(t *testing.T) {
	h := NewHub()
	h.SetOwner("房主")
	seat, _ := h.Join("小明", "后端", false)
	h.Join("小红", "前端", false)
	if _, ok := h.Kick("小明"); !ok { // kick → departed 快照（无宽限幽灵）
		t.Fatal("前置失败：kick 小明")
	}
	if _, ok := h.departed["小明"]; !ok {
		t.Fatal("前置失败：kick 应留离场快照")
	}
	h.Forget([]string{"小明"})
	if _, ok := h.departed["小明"]; ok {
		t.Fatal("Forget 应点名清掉离场快照")
	}
	if !rosterHas2(h, "小红") {
		t.Fatal("Forget 不许误伤在座成员")
	}
	// 正常离席走宽限幽灵：Forget 同样点名清掉
	h.Leave(seat) // 小明已不在座——改用小红验证 Leave 路径
	seat2, _ := h.Join("小刚", "测试", false)
	h.Leave(seat2)
	if _, ok := h.ghosts["小刚"]; !ok {
		t.Fatal("前置失败：正常离席应进宽限幽灵")
	}
	h.Forget([]string{"小刚"})
	if _, ok := h.ghosts["小刚"]; ok {
		t.Fatal("Forget 应点名清掉宽限幽灵")
	}
}

func rosterHas2(h *Hub, name string) bool {
	for _, m := range h.Members() {
		if m.Name == name {
			return true
		}
	}
	return false
}
