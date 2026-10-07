package skills

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// handlePluginsIndex answers the loader's one question — which plugins
// exist, which are enabled, what do they contribute — and re-applies
// wardrobe contributions so the roll pools track the enabled set.
func (fc *Face) HandlePluginsIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	ps := fc.Stores.Plugins.Load()
	fc.Stores.Plugins.ApplyWardrobe(ps)
	type index struct {
		API     string            `json:"api"`
		Plugins []*plugins.Plugin `json:"plugins"`
	}
	body, err := json.Marshal(index{API: plugins.APIVersion, Plugins: ps})
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("插件索引序列化失败: %s", err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// handlePluginAsset serves /plugins/<id>/<rest> from the plugin root.
// Three guards stack: the id must be a well-formed publisher.name (no
// path material at all), the rest must survive fs.ValidPath after Clean
// (no "..", no escape), and the resolved path must stay under the
// plugin's own directory (belt over braces — Join already rejected the
// escapes, this catches a future refactor).
func (fc *Face) HandlePluginAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	root := fc.Stores.Plugins.Root()
	rest := strings.TrimPrefix(r.URL.Path, "/plugins/")
	id, file, ok := strings.Cut(rest, "/")
	if !ok || file == "" || root == "" {
		http.NotFound(w, r)
		return
	}
	if !plugins.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	file = path.Clean(file)
	if file == "." || containsDotDot(file) {
		http.NotFound(w, r)
		return
	}
	dir := filepath.Join(root, id)
	full := filepath.Join(dir, filepath.FromSlash(file))
	if prefix, _ := filepath.Rel(dir, full); prefix == "" || strings.HasPrefix(prefix, "..") {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", httputil.ContentTypeOf(file))
	h.Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}

// containsDotDot spots any .. path element (path.Clean already folds
// interior ones; leading/trailing separators could still smuggle one).
func containsDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
