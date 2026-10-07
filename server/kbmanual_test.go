package server

// kbmanual_test.go — /kb/manual 三个面的契约：无参逐字节回整本（旧行为
// 不动）；?index=1 回带序号的节目录；?q=词 只回命中节（首行是命中说
// 明）且远小于全文；未命中回目录＋提示。便宜入口的验收口径就是「便宜」：
// 命中一节的响应必须比整本小一个数量级。

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
)

func startKBManualServer(t *testing.T) *Server {
	t.Helper()
	hub := chat.NewHub()
	hub.Join("房主", "boss", false)
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func getBody(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, string(b)
}

func TestKBManualBareFaceIsFullBook(t *testing.T) {
	s := startKBManualServer(t)
	code, body := getBody(t, s, "/kb/manual")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body != kb.Manual() {
		t.Fatal("无参 /kb/manual 必须逐字节回整本（旧行为不动）")
	}
}

func TestKBManualIndexFace(t *testing.T) {
	s := startKBManualServer(t)
	code, body := getBody(t, s, "/kb/manual?index=1")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, "手册目录") || !strings.Contains(body, "1. ") {
		t.Fatalf("目录面缺骨架:\n%s", head(body, 200))
	}
	// 目录必须远小于全文（这是便宜入口存在的意义）
	if len(body) >= len(kb.Manual())/4 {
		t.Fatalf("目录面不该有全文的四分之一大: %d vs %d", len(body), len(kb.Manual()))
	}
}

func TestKBManualQueryFace(t *testing.T) {
	s := startKBManualServer(t)
	code, body := getBody(t, s, "/kb/manual?q="+url.QueryEscape("访客通道"))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, "命中 ") || !strings.Contains(body, "手册检索「访客通道」") || !strings.Contains(body, "## 访客通道") {
		t.Fatalf("命中说明或节内容缺失:\n%s", head(body, 200))
	}
	if len(body) >= len(kb.Manual()) {
		t.Fatal("单节命中不该比整本还大")
	}
	// 未命中：回目录＋提示，200（换词重试的引路，不是错误）
	code, body = getBody(t, s, "/kb/manual?q="+url.QueryEscape("不存在的词xyz"))
	if code != http.StatusOK || !strings.Contains(body, "未命中") || !strings.Contains(body, "手册目录") {
		t.Fatalf("未命中该回目录: code=%d\n%s", code, head(body, 200))
	}
}

func head(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
