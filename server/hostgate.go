package server

// hostgate.go — 浏览器侧请求的门闩（安全核查修复）：本应用的信任根是
// "回环即房主"，但它挡不住浏览器替恶意网页发起的请求——DNS rebinding
// 下 Origin==Host==攻击者域名，恶意页与 127.0.0.1:7777 同源，可读全部
// JSON 面（聊天史、/fs/ls、/mcps 明文、/owner/token）；无 PNA 的浏览器
// 里 no-cors POST 还能直打 /reset、/rebuild、装插件（确认令牌是静态串，
// 防误用不防对手）。WS 升级虽有 coder/websocket 默认同源校验，rebinding
// 下同样放行。
//
// 三道各自独立的检查（任一不过即裸 404，不确认存在性，与 /view、
// /owner/token 同款体例）：
//  1. Host 必须是回环字面量（127.0.0.1/::1/localhost）——rebinding 的
//     Host 是攻击者域名，当场死；
//  2. 带 Origin 的请求（浏览器对跨源 POST 一律带）必须与请求 Host 同源
//     ——no-cors 直打的跨站 POST 当场死；本地非浏览器客户端不带 Origin，
//     不受影响；
//  3. 带 Sec-Fetch-Site 的现代浏览器必须自称 same-origin/same-site/none。
//
// 这不是把应用变成多用户安全的服务器——监听仍是仅回环、管理面仍无鉴权；
// 它只是把"本机进程可信"的模型里混进来的浏览器流量摘出去。分享页经
// SSH 隧道访问时 Host 仍是 localhost，不受影响。

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// gateBrowserBorne wraps the whole mux with the anti-rebinding /
// anti-cross-site gate above. Fail-close on anything it can't parse.
func gateBrowserBorne(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostIsLoopbackLiteral(r.Host) {
			http.NotFound(w, r)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !originMatchesHost(o, r.Host) {
			http.NotFound(w, r)
			return
		}
		switch sfs := r.Header.Get("Sec-Fetch-Site"); sfs {
		case "", "same-origin", "same-site", "none":
		default: // "cross-site"（及任何陌生取值）——fail-close
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostIsLoopbackLiteral reports whether a Host header (host or
// host:port) names the loopback machine by literal. Hostnames that
// merely RESOLVE to 127.0.0.1 don't count — that resolution is exactly
// what rebinding hijacks.
func hostIsLoopbackLiteral(host string) bool {
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	h = strings.Trim(h, "[]") // [::1]:7777 → ::1
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// originMatchesHost reports whether an Origin header value is the same
// origin as the request's own Host (scheme aside: the app serves http
// only, but a proxied https front shouldn't die on its scheme).
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false // opaque origins (null, chrome-extension…) — not ours
	}
	return strings.EqualFold(u.Host, host)
}
