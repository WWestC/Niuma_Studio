package dispatch

// 加编的招聘台唤醒：编制表面板的加行/扩编是房主私有的写，房间只落
// [编制] 提示行——Dispatcher.EstablishmentAdded 把【加编通知】点名投给
// 在岗 HR（人事）：全行字段＋落表范围＋按手册推进的指引；非 HR 成员
// 一封不收；没有在岗 HR 时如实回 false（服务端转成房主侧提示，绝不
// 装成功）。

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/WWestC/Niuma_Studio/agents"
	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// hrBridge 在 ackBridge 之上把注入按会话记名——加编通知只该进 HR 的
// 会话，记名才能断言「别人一封不收」。
type hrBridge struct {
	ackBridge
	mu    sync.Mutex
	pairs []struct{ sid, text string }
}

func (f *hrBridge) Send(ctx context.Context, sessionID, content, inputID string, deny []string) (*zcode.SendAck, error) {
	f.mu.Lock()
	f.pairs = append(f.pairs, struct{ sid, text string }{sessionID, content})
	f.mu.Unlock()
	return f.ackBridge.Send(ctx, sessionID, content, inputID, deny)
}

func TestEstablishmentAddedWakesSeatedHR(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	fb := &hrBridge{}
	d := Start(hub, store, fb, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-ea-eng"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("小马", agents.HRRole, "s-ea-hr"); err != nil {
		t.Fatal(err)
	}

	row := kb.EstablishmentRow{Key: "be", Name: "后端", Role: "后端",
		Headcount: 2, AutoFill: true, Manual: "roles/be"}
	if !d.EstablishmentAdded("proj-a", row, 0, "房主") {
		t.Fatal("在岗 HR 应收到加编通知")
	}
	waitFor(t, func() bool { return len(fb.pairs) == 1 }, "应恰好注入一封（只有 HR）")
	if got := fb.pairs[0].sid; got != "s-ea-hr" {
		t.Fatalf("通知应进 HR 会话，进了 %s", got)
	}
	line := fb.pairs[0].text
	for _, want := range []string{
		"【加编通知", "项目 proj-a 的编制表",
		"后端（key be，身份「后端」）", "编制 2", "自动补员 是", "roles/be",
		"已确认的岗位行", "dispatch birth",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("注入缺 %q：\n%s", want, line)
		}
	}

	// 扩编：prev>0 走差额口径，落表范围回到大厅
	fb.pairs = nil
	if !d.EstablishmentAdded(chat.LobbyKey, row, 1, "房主") {
		t.Fatal("扩编同样应送达 HR")
	}
	waitFor(t, func() bool { return len(fb.pairs) == 1 }, "扩编应注入一封")
	for _, want := range []string{"编制 1 → 2", "需补 1", "Niuma_Studio 编制表"} {
		if !strings.Contains(fb.pairs[0].text, want) {
			t.Errorf("扩编注入缺 %q：\n%s", want, fb.pairs[0].text)
		}
	}
}

func TestEstablishmentAddedWithoutHRAnswersFalse(t *testing.T) {
	hub := chat.NewHub()
	store, err := agents.Open("")
	if err != nil {
		t.Fatal(err)
	}
	d := Start(hub, store, &ackBridge{}, Config{
		Workspace:   t.TempDir(),
		InboxDir:    t.TempDir(),
		PatrolEvery: -1,
		DailyAt:     "off",
	})
	t.Cleanup(d.Stop)
	if err := d.attach("张三", "工程师", "s-ea-eng2"); err != nil {
		t.Fatal(err)
	}
	if d.EstablishmentAdded(chat.LobbyKey, kb.EstablishmentRow{Key: "x"}, 0, "房主") {
		t.Fatal("无在岗 HR 应回 false，不装成功")
	}
}
