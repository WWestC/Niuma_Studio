package server

// The Web workbench static face (v2 P1): GET /app serves the workbench
// entry (index.html — hash-routed, so no SPA fallback exists and "/"
// keeps its info JSON untouched, 铁律 §3-1); /app/<path> serves the
// assets. The FS arrives via Options.WebFS: main embeds the repository
// web/ directory (go:embed, the kb/manual.md precedent), tests inject
// their own; nil leaves /app absent entirely.
//
// 企业应用的新鲜度纪律（本文件的第二使命）：静态资源必须带显式缓存
// 语义，绝不让原生窗口（WKWebView / WebView2）凭启发式缓存停在旧一
// 版上——rebuild 之后 UI 仍是旧面孔、开关状态对不上，一度是「更新不
// 及时」的根因。做法与 Chromium/VS Code 同款：
//   - 发布态（embed）：内容寻址强 ETag（sha256，装载时预计算——embed
//     不可变，每文件一次）+ Cache-Control: no-cache，命中即 304；
//     二进制一换，ETag 全变，窗口必然拿到新资源。
//   - 开发态（Options.WebDev）：os.DirFS 直读磁盘 + no-store +
//     /app/__reload SSE——改一行 JS 存盘，窗口自己重载，不再「改完
//     要重新编译、重启、硬刷新」三连。

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WWestC/Niuma_Studio/util"
)

func contentTypeOf(name string) string { return httputil.ContentTypeOf(name) }

// webFace is the workbench static face over one source FS. Build with
// newWebFace; the zero value is not usable.
type webFace struct {
	src fs.FS
	dev bool // disk mode: no-store + live-reload endpoint

	// onEntry rides every /app page load (index.html only) — the
	// server hangs its usage-ledger prewarm here so the drawer's first
	// open renders from a hot cache.
	onEntry func()

	// etags precomputes the strong validator per file (embed mode —
	// the FS is immutable, so once at mount); dev mode hashes nothing.
	etags map[string]string

	// dev watcher state: the disk directory (mirrored by src) and the
	// live-reload subscribers (one-shot: a change closes their channel,
	// which ends that SSE stream — the page reloads and re-subscribes).
	dir   string
	mu    sync.Mutex
	subs  map[chan struct{}]struct{}
	stamp string
}

// newWebFace builds the face. In dev mode src should be an os.DirFS
// over dir (main wires both) and dir must exist — the watcher walks it.
func newWebFace(src fs.FS, dev bool, dir string) *webFace {
	f := &webFace{src: src, dev: dev, dir: dir}
	if !dev {
		f.etags = map[string]string{}
		_ = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil // unreadable entry: served without a validator rather than not at all
			}
			b, err := fs.ReadFile(src, p)
			if err != nil {
				return nil
			}
			sum := sha256.Sum256(b)
			f.etags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
			return nil
		})
		return f
	}
	f.subs = map[chan struct{}]struct{}{}
	f.stamp = f.signature()
	go f.watchLoop()
	return f
}

// mount wires the routes onto the server mux.
func (f *webFace) mount(mux *http.ServeMux) {
	mux.HandleFunc("/app", f.handleEntry)
	mux.HandleFunc("/app/", f.handleAsset)
	if f.dev {
		mux.HandleFunc("/app/__reload", f.handleReload)
	}
}

// handleEntry serves the exact /app → index.html (anything else 404s —
// no SPA fallback, the constitution's §3-1).
func (f *webFace) handleEntry(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/app" {
		http.NotFound(w, r)
		return
	}
	if f.onEntry != nil {
		f.onEntry()
	}
	f.serveFile(w, r, "index.html")
}

// handleAsset serves /app/<path> from the source FS. fs.ValidPath is
// the traversal guard (Clean + reject anything that escapes).
func (f *webFace) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/app/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	name = path.Clean(name)
	if !fs.ValidPath(name) || name == "." {
		http.NotFound(w, r)
		return
	}
	f.serveFile(w, r, name)
}

// serveFile is the one read-and-respond path for entry and assets:
// validator/freshness headers first, If-None-Match → 304, else body
// (gzipped on the wire when the client accepts and the type shrinks).
func (f *webFace) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	body, err := fs.ReadFile(f.src, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := contentTypeOf(name)
	h := w.Header()
	h.Set("Content-Type", ct)
	if f.dev {
		h.Set("Cache-Control", "no-store") // disk mode: the next save IS the next version
	} else {
		h.Set("Cache-Control", "no-cache") // revalidate every time; 304 when unchanged
		if et, ok := f.etags[name]; ok {
			h.Set("ETag", et)
			if etagMatch(r.Header.Get("If-None-Match"), et) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	if gzippable(ct) && acceptsGzip(r) && len(body) >= gzipMinSize {
		// 线上压缩：text 族 3-5×（index.html 240KB→~64KB），局域网窗口与
		// 冷启动都吃这一刀。在线压（embed 面不落 gzip 副本）；ETag 仍是未
		// 压缩体的强校验子——If-None-Match 照发、304 无体返回，revalidate
		// 语义原样，Vary 把协商维度记账给代理。
		h.Set("Content-Encoding", "gzip")
		h.Set("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(body)
		_ = gz.Close()
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// gzippable says whether a content type actually shrinks on the wire:
// the text family and json/svg do 3-5×; png/woff2 are already compressed
// (double-wrapping burns CPU for nothing) and octet-streams are opaque.
func gzippable(ct string) bool {
	return strings.HasPrefix(ct, "text/") ||
		strings.HasPrefix(ct, "application/json") ||
		ct == "image/svg+xml"
}

// etagMatch reports whether the request's If-None-Match carries our
// validator (the header may list several; W/ weak forms never match —
// ours are strong).
func etagMatch(in, etag string) bool {
	if in == "" {
		return false
	}
	for _, part := range strings.Split(in, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
}

// ---------- dev live-reload (the 开发态 half) ----------

// handleReload is the dev-only SSE stream: the page subscribes at boot;
// the watcher closes its channel on the next asset change, the stream
// ends, and app.js reloads the window. Production never mounts it (a
// 404 the page takes as "not dev" and moves on).
func (f *webFace) handleReload(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok || !f.dev {
		http.NotFound(w, r)
		return
	}
	ch := make(chan struct{})
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.subs, ch)
		f.mu.Unlock()
	}()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": reload stream open\n\n")
	fl.Flush()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			_, _ = io.WriteString(w, "data: reload\n\n")
			fl.Flush()
			return
		case <-tick.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

// watchLoop polls the dev directory (no cgo/fsnotify dependency — a
// 700ms walk over one source tree is nothing on a dev machine) and
// wakes every subscriber when the asset signature moves. Runs for the
// process lifetime in dev mode only; each pass rides a panic fence —
// a dead watcher is a dev window that silently stops hot-reloading.
func (f *webFace) watchLoop() {
	tick := time.NewTicker(700 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		util.Guard("server: web watcher", func() {
			sig := f.signature()
			if sig == f.stamp {
				return
			}
			f.stamp = sig
			f.mu.Lock()
			subs := make([]chan struct{}, 0, len(f.subs))
			for ch := range f.subs {
				subs = append(subs, ch)
			}
			f.subs = map[chan struct{}]struct{}{}
			f.mu.Unlock()
			for _, ch := range subs {
				close(ch)
			}
		})
	}
}

// reloadExts is what a change to actually matters for the window —
// saving a .go file next door must not reload the UI.
var reloadExts = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".html": true,
	".json": true, ".webmanifest": true, ".svg": true, ".png": true,
	".ico": true, ".woff2": true,
}

// signature folds the watched tree into one comparable string: every
// interesting file's path + size + mtime. A vanished tree stops the
// noise (empty signature) instead of erroring the loop.
func (f *webFace) signature() string {
	if f.dir == "" {
		return ""
	}
	var b strings.Builder
	_ = filepath.WalkDir(f.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // unreadable/missing: contribute nothing, keep serving what we have
		}
		if !reloadExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		b.WriteString(p)
		b.WriteByte('\x00')
		b.WriteString(info.ModTime().String())
		b.WriteByte('\x00')
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte('\x00')
		return nil
	})
	return b.String()
}

// WebDevRoot resolves the dev asset root: dir when it holds an
// index.html (NIUMA_DEV=1 launched from the repo), else nil — main
// falls back to the embed with a loud log. The bool is "dev mode on".
func WebDevRoot(dir string) fs.FS {
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return nil
	}
	return os.DirFS(dir)
}

// DevAbsentWarn is the one-line log main emits when dev mode asked for
// a disk root that isn't there (wrong cwd) — loud, not fatal.
func DevAbsentWarn(dir string) {
	log.Printf("NIUMA_DEV: %s 下没有 web/index.html —— 回退内置资源（请从仓库根目录启动）", dir)
}
