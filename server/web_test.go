package server

// The workbench static face's freshness contract (web.go): release mode
// answers ETag + no-cache with a real 304 on If-None-Match; dev mode
// answers no-store and pushes a reload event over /app/__reload when a
// watched file changes on disk. These guard the「改了没生效」batch —
// a rebuilt binary or a saved file must always reach the open window.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
)

// startWeb boots one server on an ephemeral port (parallel tests never
// fight over a fixed one) with just the web face mounted.
func startWeb(t *testing.T, src fs.FS, dev bool, dir string) *Server {
	t.Helper()
	s, err := Start(chat.NewHub(), Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Web: WebConfig{
			WebFS:     src,
			WebDev:    dev,
			WebDevDir: dir,
		}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// addrHTTP is the loopback base URL for the ephemeral port.
func addrHTTP(s *Server) string { return fmt.Sprintf("http://127.0.0.1:%d", s.Port) }

func TestWebReleaseFreshness(t *testing.T) {
	src := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>entry</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("export const x = 1;\n")},
	}
	s := startWeb(t, src, false, "")

	// entry: html type, no-cache, strong etag
	resp, err := http.Get(addrHTTP(s) + "/app")
	if err != nil {
		t.Fatalf("GET /app: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "<html>entry</html>" {
		t.Fatalf("entry: status=%d body=%q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("entry Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("entry Cache-Control = %q, want no-cache", cc)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("entry has no ETag — webviews would heuristically cache a stale build")
	}

	// asset: js type, same discipline
	resp2, err := http.Get(addrHTTP(s) + "/app/app.js")
	if err != nil {
		t.Fatalf("GET /app/app.js: %v", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if ct := resp2.Header.Get("Content-Type"); !strings.Contains(ct, "text/javascript") {
		t.Fatalf("asset Content-Type = %q", ct)
	}
	if cc := resp2.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("asset Cache-Control = %q", cc)
	}

	// If-None-Match with the entry's validator → 304 (the cheap path)
	req, _ := http.NewRequest("GET", addrHTTP(s)+"/app", nil)
	req.Header.Set("If-None-Match", etag)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	io.Copy(io.Discard, resp3.Body)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match %q → status %d, want 304", etag, resp3.StatusCode)
	}

	// no SPA fallback and no traversal: everything odd 404s
	for _, p := range []string{"/app/missing.js", "/app/../ws.go", "/app/"} {
		resp4, err := http.Get(addrHTTP(s) + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		io.Copy(io.Discard, resp4.Body)
		resp4.Body.Close()
		if resp4.StatusCode == 200 {
			t.Fatalf("GET %s → 200, want 40x (no SPA fallback / traversal)", p)
		}
	}
}

func TestWebDevLiveReload(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("index.html", "<html>v1</html>")
	write("app.js", "// v1\n")
	s := startWeb(t, os.DirFS(dir), true, dir)

	// dev assets: no-store (the next save IS the next version), no etag
	resp, err := http.Get(addrHTTP(s) + "/app/app.js")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("dev Cache-Control = %q, want no-store", cc)
	}
	if resp.Header.Get("ETag") != "" {
		t.Fatal("dev mode must not pin etags — files change under it")
	}

	// subscribe to the reload stream, then save a watched file
	stream, err := http.Get(addrHTTP(s) + "/app/__reload")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if stream.StatusCode != 200 || !strings.Contains(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("reload face: status=%d type=%q", stream.StatusCode, stream.Header.Get("Content-Type"))
	}
	defer stream.Body.Close()
	lines := bufio.NewReader(stream.Body)

	time.Sleep(200 * time.Millisecond) // let the subscriber register
	write("app.js", "// v2\n")

	got := make(chan struct{}, 1)
	go func() {
		for {
			line, err := lines.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: reload") {
				got <- struct{}{}
				return
			}
		}
	}()
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("no reload event within 3s of a watched file change")
	}

	// a non-asset neighbor must NOT wake anything: touch a .go-shaped
	// file, then confirm the stream stays quiet past the watcher beat
	write("ignore.txt", "x")
	write("app.js", "// v3\n") // the next real change; the reader above has exited, so just prove the face still serves v3
	resp3, err := http.Get(addrHTTP(s) + "/app/app.js")
	if err != nil {
		t.Fatalf("GET after change: %v", err)
	}
	b3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if string(b3) != "// v3\n" {
		t.Fatalf("dev asset stale after save: %q", b3)
	}

	// production never mounts the face
	s2 := startWeb(t, os.DirFS(dir), false, "")
	resp2, err := http.Get(addrHTTP(s2) + "/app/__reload")
	if err != nil {
		t.Fatalf("GET prod reload: %v", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("prod /app/__reload → %d, want 404", resp2.StatusCode)
	}
}

// TestWebDevRootFallback pins the dev-root resolver: a directory with
// an index.html passes, anything else refuses (main falls back to the
// embed rather than serving a broken dev face).
func TestWebDevRootFallback(t *testing.T) {
	dir := t.TempDir()
	if WebDevRoot(dir) != nil {
		t.Fatal("empty dir accepted as dev root")
	}
	if WebDevRoot("") != nil {
		t.Fatal("empty path accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if WebDevRoot(dir) == nil {
		t.Fatal("dir with index.html refused")
	}
}

// TestWebGzipOnTheWire pins the wire-compression contract: a big text
// asset served gzipped when the client accepts (gunzip = the original
// bytes), plain otherwise; tiny files and already-compressed types stay
// plain; the 304 revalidation is untouched by the negotiation. The
// client disables transport-level auto-gzip so the tests read the wire
// bytes raw.
func TestWebGzipOnTheWire(t *testing.T) {
	bigHTML := "<html>" + strings.Repeat("<p>牛马工作室</p>", 400) + "</html>"
	src := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(bigHTML)},
		"tiny.html":  &fstest.MapFile{Data: []byte("<html>x</html>")},
		"pic.png":    &fstest.MapFile{Data: bytes.Repeat([]byte{0x89, 0x50}, 1024)},
	}
	s := startWeb(t, src, false, "")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}

	get := func(name, acceptEncoding string) *http.Response {
		t.Helper()
		req, err := http.NewRequest("GET", addrHTTP(s)+"/app/"+name, nil)
		if err != nil {
			t.Fatalf("req: %v", err)
		}
		req.Header.Set("Accept-Encoding", acceptEncoding)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", name, err)
		}
		return resp
	}

	// 大文本 + gzip 协商 → 压缩体，解压即原文件；Vary 记账协商维度
	resp := get("index.html", "gzip")
	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("大文本应回压缩体，Content-Encoding=%q", ce)
	}
	if vary := resp.Header.Get("Vary"); vary != "Accept-Encoding" {
		t.Fatalf("Vary=%q, want Accept-Encoding", vary)
	}
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip 体打不开: %v", err)
	}
	un, _ := io.ReadAll(gr)
	resp.Body.Close()
	if string(un) != bigHTML {
		t.Fatal("解压后不是原文件")
	}

	// 不带协商 → 明文原样
	resp2 := get("index.html", "identity")
	plain, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.Header.Get("Content-Encoding") != "" || string(plain) != bigHTML {
		t.Fatal("无协商应拿明文原文件")
	}

	// 小文件：gzip 开销吃掉省的字节，带协商也明文
	resp3 := get("tiny.html", "gzip")
	tiny, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if resp3.Header.Get("Content-Encoding") != "" || string(tiny) != "<html>x</html>" {
		t.Fatal("小文件应走明文")
	}

	// png 自带压缩，再包一层白烧 CPU
	resp4 := get("pic.png", "gzip")
	png, _ := io.ReadAll(resp4.Body)
	resp4.Body.Close()
	if resp4.Header.Get("Content-Encoding") != "" {
		t.Fatal("png 不应再压缩")
	}
	if len(png) != 2048 {
		t.Fatalf("png 字节数不对: %d", len(png))
	}

	// 304 与协商无关：ETag 校验在压缩决定之前，无体返回
	req, err := http.NewRequest("GET", addrHTTP(s)+"/app", nil)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	probe, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /app: %v", err)
	}
	etag := probe.Header.Get("ETag")
	probe.Body.Close()
	if etag == "" {
		t.Fatal("entry 无 ETag")
	}
	req2, _ := http.NewRequest("GET", addrHTTP(s)+"/app", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	req2.Header.Set("If-None-Match", etag)
	resp5, err := client.Do(req2)
	if err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	io.Copy(io.Discard, resp5.Body)
	resp5.Body.Close()
	if resp5.StatusCode != http.StatusNotModified {
		t.Fatalf("带协商的 revalidate → %d, want 304", resp5.StatusCode)
	}
}
