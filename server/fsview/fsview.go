package fsview

// Package fsview is the project file browser's server face (类 VSCode
// 资源管理器), split from the shell's fsview.go/fsbrowse.go — the
// shell mounts, this package decides. path 一律是「相对项目 workspace
// 的斜杠路径」，空串＝根；围界由 fsResolve＋WithinDir 双保险钉死
// （前端永远拿不到也不需要绝对路径）。

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/server/fswatch"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// Face is the file-browsing face's whole world: the project registry
// (draft/archived 也可读——立项前看一眼工作区、归档后翻旧账都是正当读).
type Face struct {
	Projects *projects.Store
}

// fsTextMaxBytes is the text-preview cap: bigger files serve their first
// 2MB with truncated=true (VSCode 同款「大文件只给开头」的分寸——完整内
// 容在终端里)。
const fsTextMaxBytes = 2 << 20

// fsBinarySniffLen is how much of the head we scan for a NUL byte before
// calling a file binary (the classic file(1) heuristic — text formats
// never carry one).
const fsBinarySniffLen = 8192

// fsSearchCap fences the search walk's hit list; fsSearchSkip are the
// directory names the walk refuses to enter (VCS internals and the
// dependency mega-folders — neither is ever the answer to「我的文件在哪」,
// and both are where a walk burns its budget). The list lives in
// fswatch (the workspace watcher drinks the same one — 双面一张黑名单，
// 免 twin drift).
const (
	fsSearchCap  = 200
	fsSearchWalk = 20000 // 目录数步行帽：病态大树及时止损
)

var fsSearchSkip = fswatch.SkipDirs

// fsResolve pins rel under the project workspace root and returns the
// absolute path. Any rel — absolute-looking, dotdot-laden, backslash-
// smuggled — lands inside root or the call refuses.
func fsResolve(root, rel string) (string, bool) {
	full := filepath.Join(root, filepath.Clean("/"+strings.ReplaceAll(rel, "\\", "/")))
	if !httputil.WithinDir(root, full) {
		return "", false
	}
	return full, true
}

// fsWorkspace resolves the key's workspace root ("" = no store / no
// project / empty workspace).
func (f *Face) workspace(key string) string {
	if f.Projects == nil {
		return ""
	}
	if p, ok := f.Projects.Get(key); ok {
		return p.Workspace
	}
	return ""
}

// handleProjectFsTree serves one directory's children: dirs first (case-
// insensitive within each band), each row {name, dir, size}. Symlinked
// directories stat through and count as dirs (fs/ls 同款——链接目标也是
// 可下的目的地)；.git 隐去。
func (f *Face) Tree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	root := f.workspace(r.PathValue("key"))
	if root == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("项目登记表不可用，无法界定工作区"))
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	full, ok := fsResolve(root, rel)
	if !ok {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("path 越界：只能浏览项目工作区内的路径"))
		return
	}
	fi, err := os.Stat(full)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("目录不存在：%s", rel))
		return
	}
	if !fi.IsDir() {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("不是目录：%s", rel))
		return
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("读目录失败：%s", rel))
		return
	}
	type row struct {
		Name string `json:"name"`
		Dir  bool   `json:"dir"`
		Size int64  `json:"size,omitempty"`
	}
	dirs, files := []row{}, []row{}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue // VSCode 资源管理器默认排除同款
		}
		isDir := e.IsDir()
		size := int64(0)
		if e.Type()&os.ModeSymlink != 0 {
			// 软链按目标算（fs/ls 先例）：目标是目录就是可下的目的地，
			// 断链退化为零尺寸文件行。
			if sfi, serr := os.Stat(filepath.Join(full, e.Name())); serr == nil {
				isDir = sfi.IsDir()
				if !isDir {
					size = sfi.Size()
				}
			}
		} else if !isDir {
			if info, ierr := e.Info(); ierr == nil {
				size = info.Size()
			}
		}
		if isDir {
			dirs = append(dirs, row{Name: e.Name(), Dir: true})
		} else {
			files = append(files, row{Name: e.Name(), Size: size})
		}
	}
	less := func(list []row) func(i, j int) bool {
		return func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) }
	}
	sort.Slice(dirs, less(dirs))
	sort.Slice(files, less(files))
	out := append(dirs, files...)
	parent := ""
	if rel != "" {
		if p := strings.TrimPrefix(filepath.ToSlash(filepath.Dir(filepath.Clean("/"+rel))), "/"); p != "" {
			parent = p
		}
	}
	httputil.WriteJSONZip(w, r, map[string]any{
		"project": r.PathValue("key"), "path": rel, "parent": parent,
		"workspace": root, "entries": out,
	})
}

// handleProjectFsFile serves one file's preview envelope: kind is text
// (utf-8 且头 8K 无 NUL——content 随行，超 2MB 截断标 truncated)、image
// （按扩展名判定——字节走 /p/{key}/trace/file）或 binary（不带货）。
func (f *Face) File(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	root := f.workspace(r.PathValue("key"))
	if root == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("项目登记表不可用，无法界定工作区"))
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("path 必填（相对工作区的文件路径）"))
		return
	}
	full, ok := fsResolve(root, rel)
	if !ok {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("path 越界：只能读取项目工作区内的文件"))
		return
	}
	fi, err := os.Stat(full)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("文件不存在：%s", rel))
		return
	}
	if fi.IsDir() {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("是目录，不是文件：%s", rel))
		return
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if _, isImg := httputil.ImageExts[ext]; isImg {
		httputil.WriteJSON(w, map[string]any{
			"project": r.PathValue("key"), "path": rel, "name": filepath.Base(rel),
			"ext": ext, "size": fi.Size(), "kind": "image",
		})
		return
	}
	b, err := os.ReadFile(full)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("读文件失败：%s", rel))
		return
	}
	head := b
	if len(head) > fsBinarySniffLen {
		head = head[:fsBinarySniffLen]
	}
	truncated := false
	if len(b) > fsTextMaxBytes {
		b = b[:fsTextMaxBytes]
		truncated = true
	}
	if strings.IndexByte(string(head), 0) >= 0 || !utf8.Valid(b) {
		httputil.WriteJSON(w, map[string]any{
			"project": r.PathValue("key"), "path": rel, "name": filepath.Base(rel),
			"ext": ext, "size": fi.Size(), "kind": "binary",
		})
		return
	}
	httputil.WriteJSONZip(w, r, map[string]any{
		"project": r.PathValue("key"), "path": rel, "name": filepath.Base(rel),
		"ext": ext, "size": fi.Size(), "kind": "text", "truncated": truncated,
		"content": string(b),
	})
}

// handleProjectFsSearch walks the workspace for filename substrings
// (case-insensitive, path included in the haystack — "chat/hub" finds
// web/chat/hub.go). Hits are slash-rel paths, walk order; the walk
// refuses .git/node_modules and stops at fsSearchCap hits or
// fsSearchWalk directories, whichever comes first (truncated=true says
// the list is a prefix, not the whole truth).
func (f *Face) Search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	root := f.workspace(r.PathValue("key"))
	if root == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("项目登记表不可用，无法界定工作区"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("q 必填（文件名子串）"))
		return
	}
	needle := strings.ToLower(q)
	hits := []string{}
	truncated := false
	walked := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 断链/无权子树跳过——检索是尽力而为
		}
		if d.IsDir() {
			if walked++; walked > fsSearchWalk {
				truncated = true
				return filepath.SkipAll
			}
			if fsSearchSkip[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(strings.ToLower(filepath.ToSlash(p)), needle) {
			hits = append(hits, filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(p, root), string(filepath.Separator))))
			if len(hits) >= fsSearchCap {
				truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	httputil.WriteJSONZip(w, r, map[string]any{
		"project": r.PathValue("key"), "q": q, "hits": hits, "truncated": truncated,
	})
}
