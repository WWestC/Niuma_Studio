package skills

// market.go 的 HTTP 面测试：索引代理、URL 一键安装（本地 registry 起
// httptest 供源）、启停与卸载的即时生效。

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/server/verbs"
)

// startRegistry serves a one-plugin market: index.json + the zip.
func startRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	for name, body := range map[string]string{
		"plugin.json": `{"id":"a.shelf","name":"货架件","version":"0.1.0","contributes":{"boards":[{"id":"shelf","title":"货架","entry":"web/x.js"}]}}`,
		"web/x.js":    "export function activate(){}",
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api":"1","plugins":[{"id":"a.shelf","name":"货架件","version":"0.1.0","url":"` + srvURL(r) + `/pkg.zip"}]}`))
	})
	mux.HandleFunc("/pkg.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes.Bytes())
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// srvURL derives the registry's own base from the incoming request, so
// the index works whatever host/port the test server picked.
func srvURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func marketFace(t *testing.T) (*httptest.Server, *plugins.Store, string) {
	t.Helper()
	root := t.TempDir()
	store := plugins.Open(root, filepath.Join(t.TempDir(), "plugins.json"))
	s := &Face{Stores: verbs.StoreSet{Plugins: store}}
	mux := http.NewServeMux()
	mux.HandleFunc("/plugins.json", s.HandlePluginsIndex)
	mux.HandleFunc("/plugins/", s.HandlePluginAsset)
	mux.HandleFunc("/market/index", s.HandleMarketIndex)
	mux.HandleFunc("/market/install", s.HandleMarketInstall)
	mux.HandleFunc("/market/enable", s.HandleMarketEnable)
	mux.HandleFunc("/market/remove", s.HandleMarketRemove)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, store, root
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestMarketIndexProxyAndInstall(t *testing.T) {
	reg := startRegistry(t)
	ts, store, root := marketFace(t)

	// 索引代理
	resp, err := http.Get(ts.URL + "/market/index?url=" + reg.URL + "/index.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("index 代理 %d", resp.StatusCode)
	}
	var idx struct {
		Plugins []struct{ ID, URL string } `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil || len(idx.Plugins) != 1 || idx.Plugins[0].ID != "a.shelf" {
		t.Fatalf("索引走样：%v %v", err, idx)
	}

	// 一键安装：URL zip（带 sha 钉——网络装货无钉不放行，先取包算钉）
	zipResp, err := http.Get(reg.URL + "/pkg.zip")
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := io.ReadAll(zipResp.Body)
	zipResp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(zipBytes)
	resp2 := postJSON(t, ts.URL+"/market/install", map[string]any{
		"url":    reg.URL + "/pkg.zip",
		"sha256": hex.EncodeToString(sum[:]),
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("install %d", resp2.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "a.shelf", "plugin.json")); err != nil {
		t.Fatalf("一键安装没落盘：%v", err)
	}
	if ps := store.Load(); len(ps) != 1 || !ps[0].Enabled {
		t.Fatalf("装完该可见且启用：%+v", ps)
	}

	// 坏源/坏地址：400 级错误，不炸
	for _, body := range []map[string]any{
		{"url": reg.URL + "/nope.zip", "sha256": hex.EncodeToString(sum[:])},
		{"path": "/definitely/not/here"},
	} {
		r := postJSON(t, ts.URL+"/market/install", body)
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatalf("坏安装该 400，得到 %d", r.StatusCode)
		}
	}

	// 安全核查修复的钉子：无钉网络装货 400；明文非回环源 400。
	r3 := postJSON(t, ts.URL+"/market/install", map[string]any{"url": reg.URL + "/pkg.zip"})
	r3.Body.Close()
	if r3.StatusCode != 400 {
		t.Fatalf("无钉网络装货该 400，得到 %d", r3.StatusCode)
	}
	r4 := postJSON(t, ts.URL+"/market/install", map[string]any{"url": "http://example.com/pkg.zip", "sha256": hex.EncodeToString(sum[:])})
	r4.Body.Close()
	if r4.StatusCode != 400 {
		t.Fatalf("明文非回环源该 400，得到 %d", r4.StatusCode)
	}
}

func TestMarketEnableRemoveImmediate(t *testing.T) {
	ts, store, root := marketFace(t)
	// 直接目录装一个
	writeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(writeDir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writeDir, "plugin.json"), []byte(`{"id":"a.shelf","name":"货架件","version":"0.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Install(writeDir, false); err != nil {
		t.Fatal(err)
	}

	// 停用：同一请求内生效
	r := postJSON(t, ts.URL+"/market/enable", map[string]any{"id": "a.shelf", "on": false})
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("enable %d", r.StatusCode)
	}
	if ps := store.Load(); len(ps) != 1 || ps[0].Enabled {
		t.Fatalf("停用该即时生效：%+v", ps)
	}

	// 卸载：目录消失＋墓碑（预装不再复活）
	r2 := postJSON(t, ts.URL+"/market/remove", map[string]any{"id": "a.shelf"})
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("remove %d", r2.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "a.shelf")); !os.IsNotExist(err) {
		t.Fatal("卸载后目录该消失")
	}
	if store.ShouldSeed("a.shelf") {
		t.Fatal("卸载墓碑该挡住预装复活")
	}

	// 非法体：400
	for _, body := range []map[string]any{{}, {"id": ""}} {
		r := postJSON(t, ts.URL+"/market/remove", body)
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatalf("坏体该 400，得到 %d", r.StatusCode)
		}
	}
	_ = strings.TrimSpace("")
}
