package server

// trace.go — 工作过程的读面（v2.8）：
//
//   - GET /p/{key}/trace[?name=成员] — hub 环形缓冲的快照（各成员的
//     think/draft/tool/model/turn/error 条目，旧在前）＋名册上的干活
//     中戳。抽屉冷窗开箱拉这一份，直播增量走 "trace" 帧。
//   - GET /p/{key}/trace/file?path= — 工作区图片的窄端点：工具入参里
//     引用的本地图片（美术设计读 mock 图、验收看渲染产物）用 <img>
//     直出。只放行图片扩展名、只放行该房工作区内的文件、带尺寸帽——
//     回环即房主的信任模型下仍然把面收窄（/fs/ls 只列目录名，这里给
//     的是字节）。

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// traceFileMaxBytes is the image serving cap (8MB — a screenshot-grade
// PNG fits; a mislabeled monster does not).
const traceFileMaxBytes = 8 << 20

// handleProjectTrace serves the room's 工作过程 snapshot:
//
//	{"project": key, "members": [
//	   {"from": 名字, "working": bool, "entries": [TraceEntry…]},
//	]}
//
// ?name= narrows to one member (the open panel's refresh path).
// Unknown / draft / archived projects are 404 — the registry refuses
// them (the /p face's rule). Members render in roster order, ringless
// members included as empty rows (the panel lists who has a trail at
// all).
func (s *Server) handleProjectTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Registry == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	hub, err := s.opts.Registry.Hub(key)
	if err != nil {
		writeJSONErr(w, http.StatusNotFound, err.Error())
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	snapshot := hub.TraceSnapshot(name)
	working := map[string]bool{}
	for _, m := range hub.Members() {
		working[m.Name] = m.Working
	}
	rows := []map[string]any{}
	for _, m := range hub.Members() {
		if name != "" && m.Name != name {
			continue
		}
		entries := snapshot[m.Name]
		if entries == nil {
			entries = []chat.TraceEntry{}
		}
		rows = append(rows, map[string]any{
			"from": m.Name, "working": working[m.Name], "entries": entries,
		})
	}
	// 归档/离席成员的存量轨迹照出（名字不在名册上也有 ring）：换房
	// 整流后点旧名字仍能看最后一程。
	if name != "" && len(rows) == 0 {
		if entries, ok := snapshot[name]; ok {
			rows = append(rows, map[string]any{
				"from": name, "working": false, "entries": entries,
			})
		}
	}
	writeJSON(w, map[string]any{"project": key, "members": rows})
}

// handleProjectTraceFile serves one image under the room's workspace
// (GET /p/{key}/trace/file?path=). Refusals are 400 with the why:
// outside the workspace, a non-image extension, missing, or oversized.
func (s *Server) handleProjectTraceFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	root := ""
	if s.opts.Stores.ProjectStore != nil {
		if p, ok := s.opts.Stores.ProjectStore.Get(r.PathValue("key")); ok && p.Workspace != "" {
			root = p.Workspace
		}
	}
	if root == "" {
		writeJSONErr(w, http.StatusBadRequest, i18n.S("项目登记表不可用，无法界定工作区"))
		return
	}
	path := filepath.Clean(strings.TrimSpace(r.URL.Query().Get("path")))
	if path == "" {
		writeJSONErr(w, http.StatusBadRequest, i18n.S("path 必填"))
		return
	}
	mime, ok := httputil.ImageExts[strings.ToLower(filepath.Ext(path))]
	if !ok {
		writeJSONErr(w, http.StatusBadRequest, i18n.S("只放行图片文件（png/jpg/gif/webp/bmp/svg）"))
		return
	}
	// 放行根是工作区 ∪ 该项目的成员工作树（v2.7.1 分支隔离——隔离成
	// 员引用的本地图片在自己树上，不在主工作区里）。
	roots := append([]string{root}, s.gitFace().MemberTreeDirs(r.PathValue("key"))...)
	inside := false
	for _, rt := range roots {
		if withinDir(rt, path) {
			inside = true
			break
		}
	}
	if !inside {
		writeJSONErr(w, http.StatusBadRequest, i18n.Sf("文件不在该办公室的工作区内：%s", path))
		return
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		writeJSONErr(w, http.StatusBadRequest, i18n.Sf("文件不存在：%s", path))
		return
	}
	if fi.Size() > traceFileMaxBytes {
		writeJSONErr(w, http.StatusBadRequest, i18n.Sf("文件超过 %dMB 帽", traceFileMaxBytes>>20))
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

// withinDir reports whether path sits inside root (prefix match on
// cleaned absolute paths, either separator style tolerated).
func withinDir(root, path string) bool { return httputil.WithinDir(root, path) }
