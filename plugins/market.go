package plugins

// market.go — M2 市场的装货面：registry 索引拉取（源仓库是纯静态
// 托管——GitHub raw/Pages 即可开张）、插件 zip 下载校验、解包落装。
// zip 走「解到临时目录 → 复用 Install 的 manifest 校验与复制」一条
// 路，磁盘真相只有一个入口。zip-slip 是硬防线：包内路径不干净整个
// 包拒收。

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// MarketEntry is one plugin in a registry index（niuma-market 仓库的
// index.json 一行）。
type MarketEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Desc    string `json:"desc,omitempty"`
	Author  string `json:"author,omitempty"`
	URL     string `json:"url"`    // zip 下载址（https；明文 http 仅限回环源）
	SHA256  string `json:"sha256"` // 完整性钉——网络装货必需（HTTP 面强制非空）
}

// MarketIndex is a registry's whole shelf.
type MarketIndex struct {
	API     string        `json:"api"`
	Plugins []MarketEntry `json:"plugins"`
}

// marketHTTP — the fetch path's own clients: DNS through the pure-Go
// resolver (PreferGo). On darwin the cgo/getaddrinfo path can hang for
// some hosts (raw.githubusercontent.com on flaky v6/DNS setups — the
// 2026-10-02 debugging: shell curl fine, app's fetch dead 15s), while
// the Go resolver walks; the market must not depend on the machine
// resolver's mood. Everything else in the app stays on the default
// transport (it only ever talks to 127.0.0.1).
var marketTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:  8 * time.Second,
		Resolver: &net.Resolver{PreferGo: true},
	}).DialContext,
}

var (
	marketIndexClient = &http.Client{Timeout: 15 * time.Second, Transport: marketTransport}
	marketZipClient   = &http.Client{Timeout: 60 * time.Second, Transport: marketTransport}
)

// marketGet is one GET with one retry — registry hosting (GitHub raw)
// is intermittently flaky on some networks; a single 800ms-backoff
// second try saves most one-shot failures.
func marketGet(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	var resp *http.Response
	var err error
	for try := 0; try < 2; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(800 * time.Millisecond):
			}
		}
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err = client.Do(req)
		if err == nil {
			return resp, nil
		}
	}
	return nil, err
}

// marketURLAllowed is the fetch scheme gate（安全核查修复）：https 到
// 哪都行，明文 http 只放行回环字面量（本机测试 registry）。非回环的
// 明文源等于把插件内容（装进工作台的可执行 JS）交给链路上的任何人
// 改写——sha 钉管内容替换，明文管链路本身，两道都得有。回环放行是
// 给 httptest 起的本地源留的活路，也顺带覆盖 file:// 之外的本地玩法。
func marketURLAllowed(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New(i18n.S("源地址需是合法 URL"))
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && hostIsLoopbackLiteral(u.Host) {
		return nil
	}
	return errors.New(i18n.S("源地址需是 https——明文 http 仅限本机回环源"))
}

// hostIsLoopbackLiteral mirrors server.hostgate 的同名判定（plugins
// 反向被 server 依赖，复制这一小段比引包干净）。
func hostIsLoopbackLiteral(hostport string) bool {
	h := hostport
	if hp, _, err := net.SplitHostPort(hostport); err == nil {
		h = hp
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// FetchMarketIndex pulls one registry index. The host proxies because
// plugin JS cannot cross-origin fetch — the market board asks us.
func FetchMarketIndex(ctx context.Context, url string) (*MarketIndex, error) {
	if err := marketURLAllowed(url); err != nil {
		return nil, err
	}
	resp, err := marketGet(ctx, marketIndexClient, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(i18n.Sf("源响应 %d", resp.StatusCode))
	}
	var idx MarketIndex
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&idx); err != nil {
		return nil, errors.New(i18n.Sf("源不是合法的索引 JSON：%v", err))
	}
	return &idx, nil
}

// FetchZip downloads one plugin zip (size-capped; a plugin is data＋JS,
// 16MB 已经很宽裕).
func FetchZip(ctx context.Context, url string) ([]byte, error) {
	if err := marketURLAllowed(url); err != nil {
		return nil, err
	}
	resp, err := marketGet(ctx, marketZipClient, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(i18n.Sf("包下载响应 %d", resp.StatusCode))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// InstallFromZip verifies (optional pin), unpacks to a temp dir and
// installs through the plain Install path. sha empty = no pin.
func (s *Store) InstallFromZip(data []byte, sha string, force bool) (string, error) {
	if s.root == "" {
		return "", errors.New(i18n.S("插件目录不可用"))
	}
	if sha != "" {
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), sha) {
			return "", errors.New(i18n.S("sha256 校验不符——源与包不一致，拒绝安装"))
		}
	}
	tmp, err := os.MkdirTemp("", "niuma-plugin-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := unzipTree(data, tmp); err != nil {
		return "", err
	}
	// 文件夹打包的 zip 常把一切裹在一层目录里：根上没有 plugin.json
	// 而唯一的子目录里有 → 下钻一层（装货面体谅打包习惯，不改包内容）
	root := tmp
	if _, err := os.Stat(filepath.Join(root, "plugin.json")); err != nil {
		ents, rerr := os.ReadDir(root)
		if rerr == nil && len(ents) == 1 && ents[0].IsDir() {
			inner := filepath.Join(root, ents[0].Name(), "plugin.json")
			if _, ierr := os.Stat(inner); ierr == nil {
				root = filepath.Join(root, ents[0].Name())
			}
		}
	}
	return s.Install(root, force)
}

// unzipTree extracts with hard rails: no absolute/.. paths (zip-slip),
// ≤500 files, ≤32MB total — a plugin is data + JS, anything bigger is
// not a plugin. The path check runs BEFORE any mkdir (directory
// entries escape too — 安全核查修复：目录条目先建后查的旧序放走了
// 越根建目录), and the size caps are enforced on ACTUAL bytes
// decompressed, not just the header's declared sizes — a zip can lie
// in its UncompressedSize64 and a liar must not write past the caps
// before anyone notices.
var (
	unzipPerFileCap = int64(16 << 20)
	unzipTotalCap   = int64(32 << 20)
)

func unzipTree(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.New(i18n.Sf("不是合法 zip：%v", err))
	}
	files, total := 0, int64(0)
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) || strings.Contains(f.Name, "\\") {
			return errors.New(i18n.Sf("zip 内路径不合法：%q（zip-slip 防线）", f.Name))
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(filepath.Join(dst, f.Name), 0o755); err != nil {
				return err
			}
			continue
		}
		files++
		if files > 500 {
			return errors.New(i18n.S("zip 文件数超限（>500）"))
		}
		if f.UncompressedSize64 > uint64(unzipPerFileCap) {
			return errors.New(i18n.Sf("zip 内单文件超限：%s", f.Name))
		}
		total += int64(f.UncompressedSize64)
		if total > unzipTotalCap {
			return errors.New(i18n.S("zip 解包总量超限（>32MB）"))
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		target := filepath.Join(dst, f.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			in.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			in.Close()
			return err
		}
		// 声明的大小不作数：实测解压字节数上封顶，谎报头的解压炸弹
		// 写满上限即拒（LimitReader 多读 1 字节用来分辨"恰好到顶"与"超顶"）。
		n, copyErr := io.Copy(out, io.LimitReader(in, unzipPerFileCap+1))
		if copyErr == nil && n > unzipPerFileCap {
			copyErr = errors.New(i18n.Sf("zip 内单文件实际大小超限：%s（声明 %d）", f.Name, f.UncompressedSize64))
		}
		if copyErr == nil {
			total += n - int64(f.UncompressedSize64) // 实测对齐：谎报的差额也计入总量
			if total > unzipTotalCap {
				copyErr = errors.New(i18n.S("zip 解压实际总量超限（>32MB）"))
			}
		}
		in.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
