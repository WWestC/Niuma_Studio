package cli

// kbgov_test.go — the v3 governance commands over the owner's
// seatless face: archive/unarchive/delete frames round-trip (same
// dial discipline as owner_oneshot_test), and kb export writes the
// markdown tree (active + archived) over the HTTP face.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket/wsjson"
)

func TestKbGovFramesRoundTrip(t *testing.T) {
	_, docs, url := startOwnerStudio(t)
	if _, err := docs.Write("design/r77", "测试稿", "# 测试稿\n正文", "小鹿"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, me, err := dialTask(ctx, url, "房主", "kb archive")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	send := func(frame chat.Message) string {
		t.Helper()
		if err := wsjson.Write(ctx, conn, frame); err != nil {
			t.Fatal(err)
		}
		for {
			var m chat.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == chat.MsgKb && m.From == me.Name {
				return m.Event
			}
		}
	}

	if ev := send(chat.Message{Type: chat.MsgKbArchive, Key: "design/r77"}); ev != "archived" {
		t.Fatalf("archive 应回执 archived, got %s", ev)
	}
	if len(docs.List()) != 0 {
		t.Fatal("归档后默认列表应为空")
	}
	if ev := send(chat.Message{Type: chat.MsgKbUnarchive, Key: "design/r77"}); ev != "unarchived" {
		t.Fatalf("unarchive 应回执 unarchived, got %s", ev)
	}
	if d, err := docs.Get("design/r77", 0); err != nil || d.Archived {
		t.Fatal("出档后应在编")
	}
	if ev := send(chat.Message{Type: chat.MsgKbDelete, Key: "design/r77"}); ev != "deleted" {
		t.Fatalf("delete 应回执 deleted, got %s", ev)
	}
	if _, err := docs.Get("design/r77", 0); err == nil {
		t.Fatal("删除后应不可读")
	}
}

func TestKbExportTree(t *testing.T) {
	s, docs, _ := startOwnerStudio(t)
	if _, err := docs.Write("design/e1", "导出甲", "# 甲\n", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Write("roles/e2", "导出乙", "# 乙\n", "y"); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Archive("roles/e2", "房主"); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "exp")
	if code := runKbExport([]string{"--port", strconv.Itoa(s.Port), out}); code != 0 {
		t.Fatalf("kb export 退出码 %d", code)
	}
	for _, rel := range []string{"design/e1.md", "roles/e2.md"} {
		b, err := os.ReadFile(filepath.Join(out, rel))
		if err != nil {
			t.Fatalf("导出树缺 %s: %v", rel, err)
		}
		if len(b) == 0 || !strings.HasPrefix(string(b), "#") {
			t.Fatalf("导出内容异常 %s: %q", rel, b)
		}
	}
}
