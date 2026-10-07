package server

// lang.go — 界面语言（中/英）的读写面：GET/POST /lang。真源
// ~/.niuma/lang.json（i18n.SaveLang/LoadLang，reveal.json 同款纪律），
// 进程内镜像住 i18n 包（atomic，S/Sf 热读）。前端只在手动选过语言时
// 推送（web/ui/i18n.js 的 setLang → postLang；手动窗启动重推自愈，跟
// 随系统的自动判定不写——外来英文环境页面捋不走）——单用户本地应用，
// 最后写入者胜即可。无注册根（内存态测试、裸内嵌）整个面 404，
// 与 /retention 同一纪律：没有落盘义务就不冒读写姿态。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// langPath is the setting's slot under the v2 root.
func (s *Server) langPath() string {
	return filepath.Join(s.opts.Registry.Root(), "lang.json")
}

func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	if s.opts.Registry == nil || s.opts.Registry.Root() == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"lang": i18n.Lang()})
	case http.MethodPost:
		var body struct {
			Lang *string `json:"lang"`
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
		if err == nil && len(data) > 0 {
			err = json.Unmarshal(data, &body)
		} else if err == nil {
			err = errors.New(i18n.S("载荷缺失"))
		}
		if err != nil {
			writeJSONErr(w, http.StatusBadRequest, i18n.Sf("载荷解析失败: %s", err.Error()))
			return
		}
		if body.Lang == nil || (*body.Lang != "zh" && *body.Lang != "en") {
			writeJSONErr(w, http.StatusBadRequest, i18n.S(`载荷需要 lang（"zh" 或 "en"）`))
			return
		}
		if err := i18n.SaveLang(s.langPath(), *body.Lang); err != nil {
			writeJSONErr(w, http.StatusInternalServerError, i18n.Sf("设置写入失败: %s", err.Error()))
			return
		}
		i18n.SetLang(*body.Lang) // 落盘与换镜一口气——下一条 S/Sf 已按新语言出
		writeJSON(w, map[string]any{"lang": i18n.Lang()})
	default:
		http.Error(w, "GET/POST only", http.StatusMethodNotAllowed)
	}
}
