package server

// kbarchive_test.go — the v3 governance frames over the seatless
// owner face: archive/unarchive round-trips with the archived flag on
// the broadcast, the HTTP shelf (?archived=1 / ?q=), the host-only
// delete gate, the archive gate's non-owner denial, and the chronicle
// rollover integration (volume docs born archived, live volume small).
// Same dial/receipt discipline as kbdiff_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"

	"github.com/coder/websocket/wsjson"
)

func startKbGovStudio(t *testing.T) (*Server, *kb.DocsStore) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(filepath.Join(t.TempDir(), "kb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Write("design/r99", "定稿", "# 定稿\n正文", "小鹿"); err != nil {
		t.Fatal(err)
	}
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{Docs: docs}, LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, docs
}

func getDocs(t *testing.T, s *Server, query string) []kb.DocMeta {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + "/kb/docs" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var metas []kb.DocMeta
	if err := json.NewDecoder(resp.Body).Decode(&metas); err != nil {
		t.Fatal(err)
	}
	return metas
}

func hasKey(metas []kb.DocMeta, key string) bool {
	for _, m := range metas {
		if m.Key == key {
			return true
		}
	}
	return false
}

func TestKbArchiveFramesRoundTrip(t *testing.T) {
	s, docs := startKbGovStudio(t)
	preRev := 0
	if d, err := docs.Get("design/r99", 0); err != nil {
		t.Fatal(err)
	} else {
		preRev = d.Rev
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatless(t, s)
	receipt := func(tag string) chat.Message {
		t.Helper()
		var got chat.Message
		if err := wsjson.Read(ctx, conn, &got); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		return got
	}

	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgKbArchive, Key: "design/r99"}); err != nil {
		t.Fatal(err)
	}
	got := receipt("archive")
	if got.Event != "archived" || got.KbDoc == nil || !got.KbDoc.Archived || got.KbDoc.Rev != preRev {
		t.Fatalf("归档回执应带 archived 位且 rev 不动: %+v", got.KbDoc)
	}
	if hasKey(getDocs(t, s, ""), "design/r99") {
		t.Fatal("归档后默认列表不应出现")
	}
	if !hasKey(getDocs(t, s, "?archived=1"), "design/r99") {
		t.Fatal("归档架应列出")
	}
	if !hasKey(getDocs(t, s, "?archived=1&q=定稿"), "design/r99") {
		t.Fatal("归档架搜索应命中")
	}
	if hasKey(getDocs(t, s, "?q=定稿"), "design/r99") {
		t.Fatal("在编搜索不应漏进归档")
	}

	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgKbUnarchive, Key: "design/r99"}); err != nil {
		t.Fatal(err)
	}
	if got := receipt("unarchive"); got.Event != "unarchived" || got.KbDoc == nil || got.KbDoc.Archived {
		t.Fatalf("恢复回执应清除 archived 位: %+v", got.KbDoc)
	}
	if !hasKey(getDocs(t, s, ""), "design/r99") {
		t.Fatal("恢复后默认列表应回来")
	}
}

func TestKbDeleteHostOnly(t *testing.T) {
	s, docs := startKbGovStudio(t)
	if _, err := docs.Write("design/r98", "旧稿", "# 旧稿", "小鹿"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialOwnerSeatless(t, s)
	if err := wsjson.Write(ctx, conn, chat.Message{Type: chat.MsgKbDelete, Key: "design/r98"}); err != nil {
		t.Fatal(err)
	}
	var got chat.Message
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatal(err)
	}
	if got.Event != "deleted" || got.Key != "design/r98" || got.KbDoc != nil {
		t.Fatalf("删除回执应只带 key 无 doc: event=%s key=%s", got.Event, got.Key)
	}
	if _, err := docs.Get("design/r98", 0); err == nil {
		t.Fatal("删除后文档应不可读")
	}

	// the gate: anyone but the host gets denied before the store is
	// touched (the frame's own host check, no seat needed)
	frame := s.kbDeleteFrame(s.hub, "小狐", "design/r99")
	if frame.Event != "denied" || !strings.Contains(frame.Text, "房主") {
		t.Fatalf("非房主删除应被拒: %+v", frame)
	}
	if _, err := docs.Get("design/r99", 0); err != nil {
		t.Fatal("被拒的删除不得动文档")
	}
}

func TestKbArchiveGateDeniesNonOwner(t *testing.T) {
	s, _ := startKbGovStudio(t)
	if _, err := s.kbArchiveAs("小狐", "design/r99", true); err == nil {
		t.Fatal("无引擎房间非 owner 非房主应被拒")
	}
	if _, err := s.kbArchiveAs("房主", "design/r99", true); err != nil {
		t.Fatalf("房主应可归档: %v", err)
	}
}

func TestChronicleRolloverIntegration(t *testing.T) {
	st := newChronicleStore(t)
	c := NewChronicle(st)
	// 200 × ~180B ≈ 36KB > DocVolMax(32KB)：至少分出一卷
	for i := 0; i < 200; i++ {
		c.OnTaskDone("", fmt.Sprintf("t_roll_%d", i), strings.Repeat("事", 55), "小猿")
	}
	vol, err := st.Get(chronicleKey+"-vol-001", 0)
	if err != nil {
		t.Fatalf("应自动分出第一卷: %v", err)
	}
	if !vol.Archived {
		t.Fatal("历史卷应天生归档（不进默认列表）")
	}
	live, _ := st.Get(chronicleKey, 0)
	if len(live.Body) > kb.DocVolMax {
		t.Fatalf("活卷应恒小: %d", len(live.Body))
	}
	volLines := 0
	for _, l := range strings.Split(vol.Body, "\n") {
		if strings.HasPrefix(l, "202") {
			volLines++
		}
	}
	total := len(readChronicle(t, st)) + volLines
	if total != 200 {
		t.Fatalf("分卷不得丢行: 活卷+卷一 共 %d 行, want 200", total)
	}
	if hasKey(st.ListOpt(kb.ListOptions{Prefix: chronicleKey + "-vol-"}), chronicleKey+"-vol-001") {
		t.Fatal("卷不应进在编列表")
	}
	if !hasKey(st.ListOpt(kb.ListOptions{Archived: true, Prefix: chronicleKey + "-vol-"}), chronicleKey+"-vol-001") {
		t.Fatal("卷应在归档架按前缀可查")
	}
}
