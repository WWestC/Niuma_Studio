package fsview

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// handleFsList serves GET /fs/ls?path=: the named directory's
// subdirectories plus the parent path ("" at the volume root — no
// further up). An empty path means the user's home — the picker's
// landing spot. Unreadable children are skipped silently; a refusal is
// a 400 whose body is the why (bad path, not a directory).
func (f *Face) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("取用户主目录失败：%s", err.Error()))
			return
		}
		path = home
	}
	path = filepath.Clean(path)
	fi, err := os.Stat(path)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("目录不存在：%s", err.Error()))
		return
	}
	if !fi.IsDir() {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("不是目录：%s", path))
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("读目录失败：%s", err.Error()))
		return
	}
	dirs := []string{}
	for _, e := range entries {
		isDir := e.IsDir()
		// DirEntry.IsDir is false for a symlink — stat it through, a
		// linked directory is still a pickable destination.
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if sfi, serr := os.Stat(filepath.Join(path, e.Name())); serr == nil {
				isDir = sfi.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	parent := ""
	if p := filepath.Dir(path); p != path {
		parent = p // filepath.Dir(root) == root — at the top there is no up
	}
	home, _ := os.UserHomeDir()
	httputil.WriteJSON(w, map[string]any{"path": path, "parent": parent, "dirs": dirs, "home": home})
}
