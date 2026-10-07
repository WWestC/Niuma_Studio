package server

// /p/{key}/history's two batch shapes (v2 P3-c + v2.8.1 tail): forward
// paging (since/next_since, oldest-first) and the tail read the cold
// open hydrates from. The tail form exists because oldest-first alone
// pinned a fresh hydration to the file's FIRST page — a room past one
// batch reopened with its newest conversation invisible (重启后"丢"了
// 最近的对话), and any live frame then jumped the client cursor past
// the hidden gap so no reconnect could ever backfill it. These tests
// pin both directions and the symptom itself.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
)

// startHistoryStudio boots a server whose registry persists under a
// temp root and pre-writes the room's history file with n say frames
// (seq 1..n, speaker alternating 甲/乙 so the q filter has teeth).
func startHistoryStudio(t *testing.T, key string, n int) *Server {
	t.Helper()
	root := t.TempDir()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	registry := chat.NewRegistry(projStore, root, "", "")
	if _, err := projStore.Create(projects.Project{Key: key, Name: "项目甲",
		Workspace: t.TempDir() + "/" + key}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := projStore.Activate(key); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	var buf strings.Builder
	// fresh stamps on purpose: the boot retention sweep (default 7 days,
	// v2.8.2) prunes stale-looking history files — a fixture from 2023
	// would race it and legitimately vanish
	fresh := time.Now().Unix()
	for i := 1; i <= n; i++ {
		from := "甲"
		if i%2 == 0 {
			from = "乙"
		}
		fmt.Fprintf(&buf, `{"type":"say","from":%q,"text":"line %d","ts":%d,"seq":%d}`+"\n", from, i, fresh, i)
	}
	if err := os.MkdirAll(filepath.Join(root, "history"), 0o755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "history", key+".jsonl"), []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Registry: registry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

type historyPage struct {
	Project   string         `json:"project"`
	Count     int            `json:"count"`
	Messages  []chat.Message `json:"messages"`
	HasMore   bool           `json:"has_more"`
	NextSince int64          `json:"next_since"`
}

func getHistoryPage(t *testing.T, url string) (historyPage, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Logf("GET %s -> %d: %s", url, resp.StatusCode, body)
		return historyPage{}, resp.StatusCode
	}
	var page historyPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return page, http.StatusOK
}

func seqsOf(page historyPage) []int64 {
	out := make([]int64, 0, len(page.Messages))
	for _, m := range page.Messages {
		out = append(out, m.Seq)
	}
	return out
}

func eqSeqs(t *testing.T, page historyPage, want ...int64) {
	t.Helper()
	got := seqsOf(page)
	if len(got) != len(want) {
		t.Fatalf("batch seqs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("batch seqs = %v, want %v", got, want)
		}
	}
}

// TestProjectHistoryTailReadsFromTheEnd: tail=1 keeps the NEWEST limit
// matches, oldest-first within the batch, has_more false (nothing sits
// beyond the newest batch — a forward-chasing client stops here) and
// next_since at the newest seq.
func TestProjectHistoryTailReadsFromTheEnd(t *testing.T) {
	s := startHistoryStudio(t, "proj-a", 5)
	base := addrHTTP(s) + "/p/proj-a/history"

	page, code := getHistoryPage(t, base+"?tail=1&limit=3")
	if code != http.StatusOK {
		t.Fatalf("tail: status %d", code)
	}
	eqSeqs(t, page, 3, 4, 5)
	if page.Count != 3 || page.HasMore || page.NextSince != 5 {
		t.Fatalf("tail meta = count %d has_more %v next_since %d, want 3/false/5",
			page.Count, page.HasMore, page.NextSince)
	}

	// without limit the default batch applies to the tail too
	page, _ = getHistoryPage(t, base+"?tail=1")
	eqSeqs(t, page, 1, 2, 3, 4, 5)

	// q composes with tail: the newest 乙 matches only
	page, _ = getHistoryPage(t, base+"?tail=1&q=%E4%B9%99&limit=2")
	eqSeqs(t, page, 2, 4)
}

// TestProjectHistoryTailSymptom: the reported bug itself — a history
// past one default batch reopens showing its HEAD, hiding the newest
// conversation. tail=1 must return exactly the newest 100.
func TestProjectHistoryTailSymptom(t *testing.T) {
	s := startHistoryStudio(t, "proj-a", 250)
	page, code := getHistoryPage(t, addrHTTP(s)+"/p/proj-a/history?tail=1")
	if code != http.StatusOK {
		t.Fatalf("tail: status %d", code)
	}
	if page.Count != 100 {
		t.Fatalf("tail count = %d, want the default 100", page.Count)
	}
	eqSeqs(t, page, append(make([]int64, 0, 100), seqRange(151, 250)...)...)
	if page.HasMore || page.NextSince != 250 {
		t.Fatalf("tail meta has_more %v next_since %d, want false/250", page.HasMore, page.NextSince)
	}
}

func seqRange(from, to int) []int64 {
	out := make([]int64, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, int64(i))
	}
	return out
}

// TestProjectHistoryForwardPagingUnchanged: the pre-tail contract keeps
// its shape — oldest-first batches, has_more/next_since for the chase.
func TestProjectHistoryForwardPagingUnchanged(t *testing.T) {
	s := startHistoryStudio(t, "proj-a", 5)
	base := addrHTTP(s) + "/p/proj-a/history"

	page, code := getHistoryPage(t, base)
	if code != http.StatusOK {
		t.Fatalf("plain: status %d", code)
	}
	eqSeqs(t, page, 1, 2, 3, 4, 5)
	if page.HasMore || page.NextSince != 5 {
		t.Fatalf("plain meta has_more %v next_since %d, want false/5", page.HasMore, page.NextSince)
	}

	page, _ = getHistoryPage(t, base+"?limit=2")
	eqSeqs(t, page, 1, 2)
	if !page.HasMore || page.NextSince != 2 {
		t.Fatalf("page1 meta has_more %v next_since %d, want true/2", page.HasMore, page.NextSince)
	}

	page, _ = getHistoryPage(t, base+"?since=2&limit=2")
	eqSeqs(t, page, 3, 4)
	if !page.HasMore || page.NextSince != 4 {
		t.Fatalf("page2 meta has_more %v next_since %d, want true/4", page.HasMore, page.NextSince)
	}
}

// TestProjectHistoryTailRejectsSince: tail reads from the end — a
// since cursor alongside it is a client bug, refused loudly.
func TestProjectHistoryTailRejectsSince(t *testing.T) {
	s := startHistoryStudio(t, "proj-a", 5)
	_, code := getHistoryPage(t, addrHTTP(s)+"/p/proj-a/history?tail=1&since=3")
	if code != http.StatusBadRequest {
		t.Fatalf("tail+since status = %d, want 400", code)
	}
}

// TestProjectHistoryMDExportUnchanged: the human export keeps its
// whole-filtered-log shape regardless of the new parameter.
func TestProjectHistoryMDExportUnchanged(t *testing.T) {
	s := startHistoryStudio(t, "proj-a", 5)
	resp, err := http.Get(addrHTTP(s) + "/p/proj-a/history?format=md")
	if err != nil {
		t.Fatalf("GET md: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if got := strings.Count(string(body), "**甲**：line") + strings.Count(string(body), "**乙**：line"); got != 5 {
		t.Fatalf("md export renders %d say lines, want 5:\n%s", got, body)
	}
}
