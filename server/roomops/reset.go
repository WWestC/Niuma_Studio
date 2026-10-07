package roomops

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/WWestC/Niuma_Studio/i18n"

	"github.com/WWestC/Niuma_Studio/server/httputil"
)

// resetConfirmToken is the protocol-level double confirmation — the
// settings card makes the human type 重置, the wire carries RESET.
const resetConfirmToken = "RESET"

// handleReset serves POST /reset: body {"confirm":"RESET"} signals the
// reset choreography and answers immediately (the process relaunches
// moments later; the response only needs to reach the confirmation
// dialog so it can close with a receipt).
func (rc *Face) HandleReset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/reset" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if rc.ResetCh == nil {
		http.NotFound(w, r) // embeds/tests that don't wire the choreography
		return
	}
	var req struct{ Confirm string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		httputil.WriteJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err))
		return
	}
	if req.Confirm != resetConfirmToken {
		httputil.WriteJSONErr(w, http.StatusBadRequest,
			i18n.S("确认口令不符——重置工作室需要在请求体携带 \"confirm\":\"RESET\"（前端确认对话框代填）"))
		return
	}
	select {
	case rc.ResetCh <- struct{}{}:
	default:
		// 已有一枚信号在处理（或正在退出）：绝不排第二次队
		httputil.WriteJSONErr(w, http.StatusConflict, i18n.S("工作室已在收摊/重置中——无需重复发起"))
		return
	}
	rc.Hub.SystemRecorded(i18n.S("工作室重置已确认：正在归档成员侧栏、擦除全部数据，随后将以全新状态重启"))
	httputil.WriteJSON(w, map[string]any{
		"ok":         true,
		"restarting": true,
		"text":       i18n.S("重置已开始——归档侧栏、擦除数据、拉起全新工作室"),
	})
}
