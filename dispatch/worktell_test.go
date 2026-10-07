package dispatch

// worktell_test.go — v2.10 同步收口的三根桩：「干活中」亮起的那一刻，
// 工作过程环里必有本回合的第一行（先落行、再亮灯）；不经调度器注入的
// 外部回合同样开行；回合进行中座位被自家一次性 CLI 顶走再重挂，working
// 位按调度器真相重烫——每一张看得到 chip 的脸读到的是同一份状态。

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
)

// workFrames taps the hub's broadcast for member_work frames.
func workFrames(d *Dispatcher) <-chan chat.Message {
	obs := d.hub.AttachObserver("", false, 0)
	out := make(chan chat.Message, 16)
	go func() {
		defer close(out)
		for msg := range obs.Receive() {
			if msg.Type == chat.MsgMemberWork {
				out <- msg
			}
		}
	}()
	return out
}

// waitFrame awaits the next member_work frame with the wanted on/off.
func waitFrame(t *testing.T, frames <-chan chat.Message, on bool) chat.Message {
	t.Helper()
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("observer 流提前关闭")
			}
			if f.Working == on {
				return f
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("等 member_work(%v) 帧超时", on)
		}
	}
}

// traceTextOf folds a member's ring into one blob for substring probes.
func traceTextOf(d *Dispatcher, name string) string {
	var b strings.Builder
	for _, e := range d.hub.TraceSnapshot(name)[name] {
		b.WriteString(e.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// TestLitChipOpensWithProcessRow: 出生首轮的武装翻转——member_work(true)
// 帧到达时，环里必须已有本回合的开场行（turnOpen 先于 setStatus）。
// 「亮着 chip 的空抽屉」在此结构性不可达。
func TestLitChipOpensWithProcessRow(t *testing.T) {
	st := leaseStage(t, time.Second)
	frames := workFrames(st.d)

	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	f := waitFrame(t, frames, true)
	if f.From != "小牛" || f.Since <= 0 {
		t.Fatalf("working 帧内容走样：%+v", f)
	}
	blob := traceTextOf(st.d, "小牛")
	if !strings.Contains(blob, "回合开始——注入投递中") {
		t.Fatalf("chip 亮起时抽屉里没有回合开场行：%q", blob)
	}
	if !strings.Contains(blob, "【导师】") && !strings.Contains(blob, "出生") && len(blob) == 0 {
		t.Fatal("前置异常：环为空")
	}

	// 回合结束：chip 熄灭，开场行仍在环里（过程是档案，不随灯灭消失）。
	st.d.mu.Lock()
	var sid string
	if m, ok := st.d.members["小牛"]; ok {
		sid = m.sessionID
	}
	st.d.mu.Unlock()
	if sid == "" {
		t.Fatal("成员不在调度中")
	}
	st.d.onEvent(turnCompletedEvent(sid, "到位"))
	waitFrame(t, frames, false)
	if !strings.Contains(traceTextOf(st.d, "小牛"), "回合开始——注入投递中") {
		t.Fatal("回合结束后开场行不该消失")
	}
}

// TestExternalRunOpensProcessRow: 会话自发/外部输入把闲成员点着
// （onState 的非注入翻转）——灯亮之前同样先落行，且行文说明这轮
// 不是调度器注入的。
func TestExternalRunOpensProcessRow(t *testing.T) {
	st := leaseStage(t, time.Second)
	frames := workFrames(st.d)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	waitFrame(t, frames, true)
	st.d.mu.Lock()
	var sid string
	if m, ok := st.d.members["小牛"]; ok {
		sid = m.sessionID
	}
	st.d.mu.Unlock()
	st.d.onEvent(turnCompletedEvent(sid, "到位")) // 归位闲态
	waitFrame(t, frames, false)

	st.d.onState(sid, "running", "") // 外部点火：不经 deliverQuoted 的武装
	if f := waitFrame(t, frames, true); f.From != "小牛" {
		t.Fatalf("外部回合的 working 帧名字走样：%+v", f)
	}
	if blob := traceTextOf(st.d, "小牛"); !strings.Contains(blob, "非调度器注入") {
		t.Fatalf("外部回合没有过程行：%q", blob)
	}
	// onState 的失败终态照旧走单门：chip 熄灭一次即可。
	st.d.onState(sid, "prompt_failed", "provider error: boom")
	waitFrame(t, frames, false)
}

// TestReseatKeepsWorkingTell: 回合进行中，成员自家一次性 CLI 顶座再
// 退场——重挂的新座位记录出生 Working=false，必须按调度器真相重烫，
// 否则 chip 在整段 Running 里熄灭而调度侧仍算 TA 在干活。
func TestReseatKeepsWorkingTell(t *testing.T) {
	st := leaseStage(t, 2*time.Second)
	frames := workFrames(st.d)
	if _, err := st.d.Birth("小牛", "排期编排", "", "", ""); err != nil {
		t.Fatalf("出生不应失败：%v", err)
	}
	waitFrame(t, frames, true) // 首轮保持 Running（stub 不发自发终态）
	tok := chat.LoadSeatToken(st.d.projectKey, "小牛")
	if tok == "" {
		t.Fatal("前置：凭证已落盘")
	}

	lease, _, ok := st.d.hub.JoinFree("小牛", "排期编排", false, tok)
	if !ok || lease == nil {
		t.Fatal("带凭证拨号未能顶回自己的座位")
	}
	st.d.hub.Leave(lease) // 一次性命令退场，调度器重挂

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		working := false
		for _, m := range st.d.hub.Members() {
			if m.Name == "小牛" {
				working = m.Working
			}
		}
		if holder, held := st.d.hub.SeatHolderToken("小牛"); held && holder == tok && working {
			return // 重挂完成且 working 位仍在——收口成立
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("座位重挂后 working 位丢失（重坐未按调度器真相重烫）")
}
