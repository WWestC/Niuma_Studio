package dispatch

// whisper_test.go — 私信车道面（manual v0.10 M4 占位落地）：在编成员
// 收到带【私信】包装的注入（同一车道排队纪律）；陌生名 loud refusal
// （server 侧把这句变成写信面的拒收行，不装发送成功）。

import (
	"strings"
	"testing"
)

func TestWhisperDeliversToManagedMember(t *testing.T) {
	d, wb, _ := patrolStage(t)
	if err := d.attach("小猿", "开发", "s-wh-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.attach("小狐", "开发", "s-wh-2"); err != nil {
		t.Fatal(err)
	}
	if err := d.Whisper("小狐", "这件事单独说"); err != nil {
		t.Fatal(err)
	}
	sent := wb.sentText()
	if !strings.Contains(sent, "【私信】") || !strings.Contains(sent, "这件事单独说") {
		t.Fatalf("注入应带私信包装与原文: %q", sent)
	}
	if !strings.Contains(sent, "不进房间消息流") {
		t.Fatalf("包装应言明私密性（回复口径提示）: %q", sent)
	}
}

func TestWhisperRefusesUnknownName(t *testing.T) {
	d, _, _ := patrolStage(t)
	err := d.Whisper("路人甲", "喂")
	if err == nil || !strings.Contains(err.Error(), "不在调度中") {
		t.Fatalf("陌生名应 loud refusal: %v", err)
	}
}
