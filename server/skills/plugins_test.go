package skills

// plugins.go 的 HTTP 面测试：索引形状（装载器的唯一进餐口）、资产伺服
// 与内容类型、路径穿越的三道闸（id 形状 / fs 路径 / 目录不逃逸）、索
// 引请求触发的衣橱重放（CLI 启停免重启的生效路径）。

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"github.com/WWestC/Niuma_Studio/staffing"
)

const demoManifest = `{
	"id": "a.demo", "name": "演示", "version": "0.1.0",
	"permissions": ["boards", "wardrobe"],
	"contributes": {
		"boards": [ {"id": "demo", "title": "演示板", "entry": "web/main.js"} ],
		"wardrobe": ["wardrobe.json"]
	}
}`

// hexFor derives a per-test hair hex: the staffing/pixart merge tables are
// process-global, so each case must roll its own color to stay order-proof.
func hexFor(t *testing.T) string {
	t.Helper()
	h := fnv.New32a()
	_, _ = h.Write([]byte(t.Name()))
	return fmt.Sprintf("#%06x", h.Sum32()&0xffffff)
}

func pluginFace(t *testing.T) *httptest.Server {
	t.Helper()
	hex := hexFor(t)
	root := t.TempDir()
	dir := filepath.Join(root, "a.demo")
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	wardrobe := fmt.Sprintf(`{
		"hair_colors": [ { "key": "aurora", "name": "极光薄荷", "hex": "%s", "weight": 3 } ]
	}`, hex)
	for name, body := range map[string]string{
		"plugin.json":   demoManifest,
		"wardrobe.json": wardrobe,
		"web/main.js":   "export function activate(niuma) { niuma.registerBoard({}); }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 逃逸靶子：插件根目录下的机密文件（不属于任何插件）
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Face{Stores: verbs.StoreSet{Plugins: plugins.Open(root, filepath.Join(t.TempDir(), "plugins.json"))}}
	mux := http.NewServeMux()
	mux.HandleFunc("/plugins.json", s.HandlePluginsIndex)
	mux.HandleFunc("/plugins/", s.HandlePluginAsset)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestPluginsIndexShape(t *testing.T) {
	ts := pluginFace(t)
	r, err := ts.Client().Get(ts.URL + "/plugins.json")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	var idx struct {
		API     string `json:"api"`
		Plugins []struct {
			ID       string `json:"id"`
			Enabled  bool   `json:"enabled"`
			Manifest *struct {
				Contributes struct {
					Boards []struct{ ID, Entry string } `json:"boards"`
				} `json:"contributes"`
			} `json:"manifest"`
		} `json:"plugins"`
	}
	b, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatalf("索引不是合法 JSON：%v\n%s", err, b)
	}
	if idx.API != plugins.APIVersion {
		t.Fatalf("api 版本 %q ≠ %q", idx.API, plugins.APIVersion)
	}
	if len(idx.Plugins) != 1 || idx.Plugins[0].ID != "a.demo" || !idx.Plugins[0].Enabled {
		t.Fatalf("插件条目走样：%s", b)
	}
	if bs := idx.Plugins[0].Manifest.Contributes.Boards; len(bs) != 1 || bs[0].ID != "demo" || bs[0].Entry != "web/main.js" {
		t.Fatalf("板块贡献走样：%s", b)
	}
}

func TestPluginAssetServed(t *testing.T) {
	ts := pluginFace(t)
	r, err := ts.Client().Get(ts.URL + "/plugins/a.demo/web/main.js")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf(".js 的内容类型该是 text/javascript，得到 %q（Windows 注册表之祸不许复发）", ct)
	}
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), "activate") {
		t.Fatalf("资产内容走样：%s", body)
	}
	// 不存在的文件与目录 404；裸 /plugins/ 与 /plugins.json 混不进来
	for _, p := range []string{"/plugins/a.demo/web/nope.js", "/plugins/a.demo/web", "/plugins/a.demo/", "/plugins/", "/plugins/nobody.x/web/main.js"} {
		r, err := ts.Client().Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("%s 该 404，得到 %d", p, r.StatusCode)
		}
	}
}

func TestPluginAssetTraversalBlocked(t *testing.T) {
	ts := pluginFace(t)
	client := ts.Client()
	// 形态各异的逃逸尝试：明文 ..、URL 编码的 %2e%2e/%2f、编码后的
	// 越权 id。跟随重定向后的终态必须 404，正文绝不带靶子内容。
	for _, p := range []string{
		"/plugins/a.demo/../secret.txt",
		"/plugins/a.demo/%2e%2e/secret.txt",
		"/plugins/a.demo/web/../../secret.txt",
		"/plugins/%2e%2e/secret.txt",
		"/plugins/..%2fsecret.txt",
		"/plugins/a.demo/%2e%2e%2f%2e%2e%2fsecret.txt",
	} {
		r, err := client.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("%s 终态该 404，得到 %d", p, r.StatusCode)
		}
		if strings.Contains(string(body), "TOP-SECRET") {
			t.Fatalf("%s 泄了靶子内容", p)
		}
	}
}

func TestPluginsIndexReappliesWardrobe(t *testing.T) {
	ts := pluginFace(t)
	hex := hexFor(t)
	if staffing.HairHexTaken("", hex) {
		t.Fatal("前置：本用例专属色尚未登记（hex 派生撞车？）")
	}
	r, err := ts.Client().Get(ts.URL + "/plugins.json")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !staffing.HairHexTaken("", hex) {
		t.Fatal("索引请求该触发衣橱重放（发色没进掷骰表）")
	}
}
