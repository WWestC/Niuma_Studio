package server

// The kb write broadcast's red/green rows (黑板红绿对比): a successful
// kb_write / kb_append over the seatless owner face must arrive with
// diff rows the chat card can paint — the write's actual change, not
// just the event word — while a denial stays a plain reason (no rows
// to paint). Same dial/shape discipline as mirror_kb_test.go: the
// seatless connection reads its own write-back receipt next.

import (
	"context"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"

	"github.com/coder/websocket/wsjson"
)

func TestKbBroadcastCarriesDiff(t *testing.T) {
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{Docs: docs}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatless(t, s)

	// receipt reads into a FRESH struct every call: wsjson decodes into
	// the value it's handed, so a reused one keeps stale fields from the
	// previous frame (a denied reply carries no diff key — the rows of
	// the receipt before it would linger and lie).
	receipt := func(tag string) chat.Message {
		t.Helper()
		var got chat.Message
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		return got
	}

	// rev1: a new doc — every line is an add row.
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgKbWrite, KbDoc: &chat.KbDoc{Key: "roles/diff", Title: "手册", Body: "第一版"}}); err != nil {
		t.Fatalf("send kb_write: %v", err)
	}
	if got := receipt("rev1"); got.Type != chat.MsgKb || got.Event != "written" ||
		len(got.Diff) != 1 || got.Diff[0].Kind != "+" || got.Diff[0].Text != "第一版" {
		t.Fatalf("新建写入应广播 +第一版 增行，got type=%s event=%s diff=%+v", got.Type, got.Event, got.Diff)
	}

	// rev2: a rewrite — one del row before one add row (git hunk order).
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgKbWrite, KbDoc: &chat.KbDoc{Key: "roles/diff", Body: "第二版"}}); err != nil {
		t.Fatalf("send kb_write(2): %v", err)
	}
	if got := receipt("rev2"); len(got.Diff) != 2 || got.Diff[0].Kind != "-" || got.Diff[0].Text != "第一版" ||
		got.Diff[1].Kind != "+" || got.Diff[1].Text != "第二版" {
		t.Fatalf("改写应广播 -第一版 +第二版，got %+v", got.Diff)
	}

	// rev3: an append — exactly the appended line as an add row.
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgKbAppend, Key: "roles/diff", Text: "追加行"}); err != nil {
		t.Fatalf("send kb_append: %v", err)
	}
	if got := receipt("rev3"); got.Event != "appended" ||
		len(got.Diff) != 1 || got.Diff[0].Kind != "+" || got.Diff[0].Text != "追加行" {
		t.Fatalf("追加应广播 +追加行，got event=%s diff=%+v", got.Event, got.Diff)
	}

	// a denial stays a plain reason — no rows to paint.
	if err := wsjson.Write(ctx, conn, chat.Message{
		Type: chat.MsgKbAppend, Key: "ops/none", Text: "x"}); err != nil {
		t.Fatalf("send kb_append(none): %v", err)
	}
	if got := receipt("denied"); got.Event != "denied" || len(got.Diff) != 0 {
		t.Fatalf("拒绝帧只带理由不带 diff，got event=%s diff=%+v", got.Event, got.Diff)
	}
}
