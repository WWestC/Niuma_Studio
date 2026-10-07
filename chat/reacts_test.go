package chat

// The emoji-reaction ledger's own tests (飞书式表情回应, reacts.go):
// broadcast + dedupe + toggle, the honesty rules (member lines only,
// self-react allowed — an opinion is not an answer, emoji validation,
// palette cap, floor), and the persistence that keeps chips alive
// across restarts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reactorsOf snapshots one seq's reactor set for one emoji.
func reactorsOf(t *testing.T, h *Hub, seq int64, emoji string) []string {
	t.Helper()
	_, rows := h.ReactReceipts()
	for _, row := range rows {
		if row.Seq != seq {
			continue
		}
		for _, set := range row.Reactions {
			if set.Emoji == emoji {
				return set.Names
			}
		}
	}
	return nil
}

func TestMarkReactBroadcastsTogglesAndDedupes(t *testing.T) {
	h := NewHub()
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "方案 B 定稿")
	seq := lastSaySeq(t, h)
	li, _ := h.Join("李四", "", false)

	if !h.MarkReact("李四", seq, "👍", true) {
		t.Fatal("首枚回应该记上")
	}
	frame := recvUntil(t, li, MsgReact, 2*time.Second)
	if frame.From != "李四" || frame.Seq != seq || frame.Emoji != "👍" || !frame.On {
		t.Fatalf("react 帧形状不对: %+v", frame)
	}
	if got := reactorsOf(t, h, seq, "👍"); len(got) != 1 || got[0] != "李四" {
		t.Fatalf("快照不对: %v", got)
	}

	// 同人同表情重复添加安静；未知 seq、坏表情安静——回应不许说谎
	if h.MarkReact("李四", seq, "👍", true) {
		t.Fatal("重复添加不该再记")
	}
	if h.MarkReact("李四", seq+424242, "👍", true) {
		t.Fatal("未知 seq 不该记账")
	}
	if h.MarkReact("李四", seq, "带\n换行", true) {
		t.Fatal("带控制字符的表情不该记账")
	}
	if h.MarkReact("李四", seq, strings.Repeat("👍", 9), true) {
		t.Fatal("超长表情序列不该记账")
	}
	select {
	case m := <-li.Receive():
		t.Fatalf("不应再广播 react 帧: %+v", m)
	case <-time.After(150 * time.Millisecond):
	}

	// 撤回：broadcast 翻 on=false，账面清干净；再撤安静
	if !h.MarkReact("李四", seq, "👍", false) {
		t.Fatal("撤回该是新鲜事实")
	}
	frame = recvUntil(t, li, MsgReact, 2*time.Second)
	if frame.On {
		t.Fatalf("撤回帧该带 on=false: %+v", frame)
	}
	if got := reactorsOf(t, h, seq, "👍"); got != nil {
		t.Fatalf("撤回后账面该净: %v", got)
	}
	if h.MarkReact("李四", seq, "👍", false) {
		t.Fatal("撤回不存在的事实不该广播")
	}
}

func TestReactLedgerHonestyRules(t *testing.T) {
	h := NewHub()
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "回自己的也行——回应是观点不是回话")
	seq := lastSaySeq(t, h)

	// 与收到回执分家：回自己的消息合法
	if !h.MarkReact("房主", seq, "🎉", true) {
		t.Fatal("回自己的消息该可以回应")
	}
	if got := reactorsOf(t, h, seq, "🎉"); len(got) != 1 || got[0] != "房主" {
		t.Fatalf("自回该记账: %v", got)
	}

	// 系统行是房间自己的声音，挣不到回应
	h.SystemRecorded("系统留痕行")
	if h.MarkReact("李四", h.LastSeq(), "👍", true) {
		t.Fatal("系统行不该挣得回应")
	}

	// report 也是成员的话——汇报同样可以被回应；调色盘上限内多表情共存
	h.Report(sp, "周报：数据已齐")
	rep := h.LastSeq()
	h.MarkReact("李四", rep, "👍", true)
	h.MarkReact("李四", rep, "🔥", true)
	h.MarkReact("王五", rep, "👍", true)
	if got := reactorsOf(t, h, rep, "👍"); len(got) != 2 {
		t.Fatalf("同表情多人该并存: %v", got)
	}
	if got := reactorsOf(t, h, rep, "🔥"); len(got) != 1 {
		t.Fatalf("第二枚表情该记账: %v", got)
	}

	// 每条消息的调色盘有上限：满了之后再添新表情安静（旧表情不受影响）
	for i := 0; i < reactEmojiCap+3; i++ {
		h.MarkReact("赵六", rep, string(rune(0x1F400+i))+"️⃣", true)
	}
	_, rows := h.ReactReceipts()
	for _, row := range rows {
		if row.Seq == rep && len(row.Reactions) > reactEmojiCap {
			t.Fatalf("调色盘超上限: %+v", row.Reactions)
		}
	}
}

func TestReactLedgerPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.jsonl")
	h1 := NewHub()
	h1.SetHistoryPath(path)
	sp, _ := h1.Join("房主", "", false)
	h1.Say(sp, "第一条")
	m1 := lastSaySeq(t, h1)
	h1.Say(sp, "第二条")
	m2 := lastSaySeq(t, h1)
	h1.MarkReact("李四", m1, "👍", true)
	h1.MarkReact("王五", m1, "👍", true)
	h1.MarkReact("王五", m2, "🔥", true)
	h1.FlushLedgers() // 落盘已异步：开新房前排干

	h2 := NewHub()
	h2.SetHistoryPath(path)
	since, rows := h2.ReactReceipts()
	if since != m1 {
		t.Fatalf("floor 未随账本持久化: since=%d want=%d", since, m1)
	}
	if len(rows) != 2 {
		t.Fatalf("重启丢了回应: %+v", rows)
	}
	if got := reactorsOf(t, h2, m1, "👍"); len(got) != 2 || got[0] != "李四" || got[1] != "王五" {
		t.Fatalf("回应集不合: %v", got)
	}
	if got := reactorsOf(t, h2, m2, "🔥"); len(got) != 1 {
		t.Fatalf("第二行回应丢失: %v", got)
	}

	// 撤回也持久化：净掉的枚重启后不复活
	h2.MarkReact("李四", m1, "👍", false)
	h2.FlushLedgers() // 同上：排干再开 h3
	h3 := NewHub()
	h3.SetHistoryPath(path)
	if got := reactorsOf(t, h3, m1, "👍"); len(got) != 1 || got[0] != "王五" {
		t.Fatalf("撤回后重启复活了: %v", got)
	}
}

func TestReactLedgerFloorsAtRestoredHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.jsonl")
	old, err := json.Marshal(Message{Type: MsgSay, From: "老王", Text: "旧消息", Seq: 1000, TS: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(old, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.SetHistoryPath(path)
	// 屏障挂 t.Cleanup：flusher 排干即静止，不再与 TempDir 清理赛跑
	t.Cleanup(func() { h.FlushLedgers() })
	// 功能上线前的历史消息：floor 之下，回应也不记账（宁缺毋谎）
	if h.MarkReact("李四", 1000, "👍", true) {
		t.Fatal("floor 下的 seq 不该记账")
	}
	sp, _ := h.Join("房主", "", false)
	h.Say(sp, "新消息")
	seq := lastSaySeq(t, h)
	h.MarkReact("李四", seq, "👍", true)
	if got := reactorsOf(t, h, seq, "👍"); len(got) != 1 {
		t.Fatalf("floor 上的 seq 该记账: %v", got)
	}
}
