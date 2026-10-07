// Package httputil is the server family's shared HTTP write face: the
// JSON helpers every face package (and the shell) answers with. It
// exists so the domain faces split out of the shell without importing
// it — writeJSON/writeJSONErr/writeJSONZip/withinDir moved here
// verbatim from the shell's http.go and trace.go; the shell keeps
// unexported wrappers so its ~200 call sites are untouched.
package httputil

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// WriteJSON writes v as the face's JSON answer.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// WriteJSONErr is WriteJSON's error twin: an HTTP status with a JSON
// {"error": "..."} body, so read faces can fail with a reason the web
// panels render instead of a bare status line. The reason rides through
// i18n.S — static Chinese reasons are dictionary keys, so the whole
// error family flips language with the UI setting without per-call
// edits (composed reasons migrate at their call sites with Sf).
func WriteJSONErr(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": i18n.S(reason)})
}

// GzipMinSize is the wire-compression floor: below it gzip's dictionary
// and trailer eat the bytes it saves — small answers ride plain.
const GzipMinSize = 1024

// AcceptsGzip reports whether the client advertised gzip in
// Accept-Encoding (WKWebView 和浏览器都带；不带的客户端拿明文).
func AcceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "gzip" {
			return true
		}
	}
	return false
}

// WriteJSONZip is WriteJSON's heavy-read-face twin: byte-identical JSON
// (Encoder 的行尾换行保留), gzipped when the client accepts it and the
// body clears GzipMinSize — /kb/tasks 一拍 190KB、驾驶舱 20s 一撞，
// 压完 ~30KB，局域网开面板的速度就是这一刀省出来的。在线压缩（这些
// 面每分钟至多几发，sync.Pool 不值当）；gzip 路径不设 Content-Length
// （压后尺寸写完才知道，chunked 走起），304/ETag 语义不涉足。
func WriteJSONZip(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		WriteJSONErr(w, http.StatusInternalServerError, i18n.Sf("序列化失败：%s", err.Error()))
		return
	}
	body = append(body, '\n') // json.Encoder 的行尾换行，明文/密文同一形状
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if !AcceptsGzip(r) || len(body) < GzipMinSize {
		_, _ = w.Write(body)
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Set("Vary", "Accept-Encoding")
	gz := gzip.NewWriter(w)
	_, _ = gz.Write(body)
	_ = gz.Close()
}

// WithinDir reports whether path stays inside root (the shared
// containment check the file-reading faces pin every user path with).
func WithinDir(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ImageExts is the image-extension whitelist (lowercase, with dot) —
// the shared mime vocabulary of the file-serving faces (trace's image
// leg and the file browser's kind=image envelope).
var ImageExts = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp", ".svg": "image/svg+xml",
}

// contentTypeOf picks a deterministic media type (the OS mime registry
// must never decide — Windows' registry once served .js as text/plain
// and the module loader refused it). charset rides text types.
// ContentTypeOf maps a file name onto its HTTP Content-Type
func ContentTypeOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".webmanifest":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// isLoopbackAddr reports whether the dial came from this machine —
// the app's one trust root (WS listeners and admin endpoints alike).
// LoopbackAddr reports whether the remote address is loopback (the
// detail faces' cleartext rule: masked everywhere, cleartext on the
// machine).
func LoopbackAddr(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// readJSONBody is the small shared reader (the autopilot face's shape).
// ReadJSONBody decodes one request body as JSON with a size cap.
func ReadJSONBody(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 安全核查修复：有界读取
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("%s", i18n.S("载荷缺失"))
	}
	return json.Unmarshal(data, v)
}

// requestFromLoopback reports whether the connection itself came from
// this machine. An unparseable peer (empty RemoteAddr, a weird proxy)
// is NOT loopback — fail-close asks it for the token.
// ReqFromLoopback reports whether the connection itself came from
// this machine (fail-close on unparseable peers).
func ReqFromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// bearerToken extracts an Authorization: Bearer <token> value ("" when
// absent or malformed).
// BearerToken extracts an Authorization: Bearer <token> value.
func BearerToken(r *http.Request) string {
	const prefix = "Bearer "
	a := r.Header.Get("Authorization")
	if len(a) > len(prefix) && strings.EqualFold(a[:len(prefix)], prefix) {
		return strings.TrimSpace(a[len(prefix):])
	}
	return ""
}
