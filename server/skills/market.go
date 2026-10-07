package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/plugins"
	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// handleMarketIndex proxies one registry index fetch.
func (fc *Face) HandleMarketIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	source := r.URL.Query().Get("url")
	if source == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("缺少 ?url= 参数（市场源地址）"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	idx, err := plugins.FetchMarketIndex(ctx, source)
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeMarketJSON(w, idx)
}

type marketInstallBody struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path"`
	Force  bool   `json:"force"`
}

// handleMarketInstall is the one-click: {url, sha256?} for a registry
// zip or {path} for a local directory.
func (fc *Face) HandleMarketInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body marketInstallBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	var (
		id  string
		err error
	)
	switch {
	case body.URL != "":
		// 网络装货必须带 sha256 钉（安全核查修复）：无钉的 URL 包
		// 内容无凭无据，装进工作台的是可执行 JS——宁可让用户去索引
		// 里拿带钉的条目，不放无钉的包过门。本地 path 装货不经网络，
		// 不在此列。
		if body.SHA256 == "" {
			httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("网络装货需带 sha256 完整性钉（索引条目里的 sha256 字段）"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		var zipData []byte
		zipData, err = plugins.FetchZip(ctx, body.URL)
		if err == nil {
			id, err = fc.Stores.Plugins.InstallFromZip(zipData, body.SHA256, body.Force)
		}
	case body.Path != "":
		id, err = fc.Stores.Plugins.Install(body.Path, body.Force)
	default:
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("载荷需带 url 或 path（二选一）"))
		return
	}
	if err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fc.ReconcilePlugins()
	writeMarketJSON(w, map[string]any{"ok": true, "id": id, "text": i18n.Sf("已安装 %s——刷新工作室窗口生效", id)})
}

// handleMarketEnable flips one plugin's enable state; the wardrobe and
// capability contributions reconcile in the same request.
func (fc *Face) HandleMarketEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
		On bool   `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.ID == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("载荷缺失或 ID 为空"))
		return
	}
	if err := fc.Stores.Plugins.SetEnabled(body.ID, body.On); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fc.ReconcilePlugins()
	if body.On {
		writeMarketJSON(w, map[string]any{"ok": true, "text": i18n.Sf("已启用 %s", body.ID)})
	} else {
		writeMarketJSON(w, map[string]any{"ok": true, "text": i18n.Sf("已停用 %s", body.ID)})
	}
}

// handleMarketRemove uninstalls (contributions withdraw in-request).
func (fc *Face) HandleMarketRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.ID == "" {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.S("载荷缺失或 ID 为空"))
		return
	}
	if err := fc.Stores.Plugins.Remove(body.ID); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	fc.ReconcilePlugins()
	writeMarketJSON(w, map[string]any{"ok": true, "text": i18n.Sf("已卸载 %s", body.ID)})
}

// reconcilePlugins re-applies the whole contribution surface after a
// market write — enable/disable/uninstall land immediately, no host
// restart, no waiting for the next /plugins.json.
func (fc *Face) ReconcilePlugins() {
	ps := fc.Stores.Plugins.Load()
	fc.Stores.Plugins.ApplyWardrobe(ps)
}

func writeMarketJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
