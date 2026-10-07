package roomops

// The keeper's watch gate + advisor exemption (v2.4): only 必须 posts
// and 自动补员=是 rows are watched — a plain custom row (先定编后招牛马,
// 自动补员=否) sits quiet at 编制 1 / 在岗 0 instead of nagging every
// cooldown; an autofill custom row still alarms when its refill cannot
// land; and the 小助手 establishment row names no seat at all (the
// assistant session lives on the bridge), so it must never alarm.

import (
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
)

func TestKeeperWatchGateAndAdvisor(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	// 三类行同表：受看护的自建岗（自动补员=是，无候选人可召回 → 报警）；
	// 安静的规划岗（自动补员=否，未招牛马）；小助手（必须系统岗，无座位）。
	table := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 | 必须 |\n" +
		"|---|---|---|---|---|---|---|\n" +
		"| be | 后端 | 后端 | 1 | 是 |  | 否 |\n" +
		"| ops | 运维 | 运维 | 1 | 否 |  | 否 |\n" +
		"| assistant | 小助手 | 使用顾问 | 1 | 否 |  | 是 |\n"
	if _, err := docs.Write("ops/establishment", "编制表", table, "房主"); err != nil {
		t.Fatalf("预置表：%v", err)
	}

	tick := make(chan time.Time)
	k := StartKeepers(KeeperConfig{LobbyHub: lobby, Docs: docs, Tick: tick})
	if k == nil {
		t.Fatal("看护应可启动")
	}
	defer k.Stop()

	// 三岗都「缺了超过宽限期」：预填 firstSeen 为一小时前，一轮即出结论
	old := time.Now().Add(-time.Hour)
	for _, key := range []string{"be", "ops", "assistant"} {
		k.firstSeen["default\x00"+key] = old
	}

	tick <- time.Now()
	if !k.done() {
		t.Fatal("一轮对账应在时限内完成")
	}

	var joined string
	for _, m := range lobby.History() {
		joined += m.Text + "\n"
	}
	if !strings.Contains(joined, "后端") {
		t.Fatalf("自动补员=是的缺岗应照常报警（召回不成才说），历史：%s", joined)
	}
	if strings.Contains(joined, "运维") {
		t.Fatalf("自建岗（非必须、自动补员=否）不应缺岗报警——先定编后招牛马是常态，历史：%s", joined)
	}
	if strings.Contains(joined, "小助手") || strings.Contains(joined, "使用顾问") {
		t.Fatalf("小助手行（顾问，无座位）不应触发缺岗报警，历史：%s", joined)
	}
}
