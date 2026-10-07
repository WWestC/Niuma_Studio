package server

// media.go — 输入图片的 HTTP 面（聊天发图）：
//
//   - POST /media?name=原文件名 — 裸字节进 body（Content-Type 报 MIME，
//     缺失/通用值时按文件名后缀兜底），落进媒体仓库，回
//     {"image":{id,…},"url":"/media/{id}"}。前端选图/贴图即传，say 帧
//     只带引用——上传与发言两步分开，一条消息重发不必重传字节。
//   - GET /media/{id} — 仓库取回字节直出（<img> 的 src）。内容寻址
//     （id 一次性生成、永不改指），配强 ETag + no-cache 的回执协商，
//     与 /app 静态面同一套新鲜度纪律。
//
// 写面是 POST 而非 WS 帧：字节流走 HTTP 比 JSON 线帧合适（一张截图
// base64 进 WS 会让两侧各备一份膨胀的中转缓冲），且 /rebuild、
// /establishment/row 等新写面已有 HTTP 先例。回环即房主的信任模型
// 下不设房主校验；白名单与尺寸帽在 media.Store 里收口。

import (
	"io"
	"net/http"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/media"
)

// handleMediaUpload stores one uploaded image (POST /media). The body
// IS the bytes; ?name= rides the original filename (display + the
// extension fallback when Content-Type is missing or generic).
func (s *Server) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.Stores.MediaStore == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, i18n.S("媒体仓库未配置（输入图片不可用）"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxBytes+(1<<20)) // 尺寸帽＋一点协议余量
	data, err := io.ReadAll(r.Body)
	if err != nil { // MaxBytesReader's own too-large verdict
		writeJSONErr(w, http.StatusRequestEntityTooLarge, i18n.S("上传超过尺寸帽（8MB）"))
		return
	}
	img, err := s.opts.Stores.MediaStore.Save(data, r.URL.Query().Get("name"), r.Header.Get("Content-Type"))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"image": img, "url": "/media/" + img.ID})
}

// handleMediaGet serves one stored image's bytes (GET /media/{id}).
// Refusals are plain 404s — a dropped warehouse entry renders as a
// broken thumb, never a failed room.
func (s *Server) handleMediaGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	path, mime, _, ok := s.opts.Stores.MediaStore.Lookup(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := `"` + r.PathValue("id") + `"` // content-addressed: the id never re-points
	h := w.Header()
	h.Set("Content-Type", mime)
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeFile(w, r, path) // ServeContent under the hood: length + range come free
}
