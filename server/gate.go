package server

// gate.go — 信任模型的可部署层（根治「离开回环即重写」的三分之
// 一）：本应用的信任根是"回环即房主"，hostgate 只摘浏览器侧流量，
// 监听面一直钉死 127.0.0.1——想把这间工作室端给别人用（LAN 同事、
// SSH 反向转发到手机……）过去是"全部裸露 + 一条告警日志"。
//
// 现在的分界是：
//   - 回环请求：行为逐字节照旧（gateBrowserBorne 三查 + 零摩擦），
//     本机单用户的日常一根手指都不用多动；
//   - 非回环请求：必须携带工作室令牌（Authorization: Bearer <t>，
//     或 WS/直链的 ?token=），常量时间比对，错了回裸 404——与
//     hostgate 同款"不确认端点存在"体例；持令牌的远端请求直达
//     mux（令牌即边界，Origin/Host 检查对远端不再适用——它们的
//     活儿由令牌承担）；
//   - 非回环绑定而无令牌：server.Start 直接拒绝起服务（fail-close，
//     原地取代过去那条只告警不设防的哨兵日志）。
//
// 令牌来源：NIUMA_GATE_TOKEN；远程绑定而未提供时自动铸一枚写入
// ~/.niuma_gate_token（0600）并打进日志。这不是多用户——仍然一间
// 工作室、一个房主、一张令牌；是"把工作室安全地端出去"从重写级
// 降为环境变量级。访客只读面（VisitorGate）与一日回放分享页
// （dayReplayGate）的独立 token 语义不变，且现在整体坐在令牌门
// 之内。

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"github.com/WWestC/Niuma_Studio/server/httputil"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// GateTokenPath returns the auto-minted host token's conventional
// location: ~/.niuma_gate_token.
func GateTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".niuma_gate_token"), nil
}

// MintGateToken mints a fresh 128-bit host token (hex).
func MintGateToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// SaveGateToken persists the token for the operator's reference (0600
// — credential mode, same as the seat tokens).
func SaveGateToken(path, token string) error {
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

// newTokenGate arms the mux's token boundary. localGate is the full
// historical stack (gateBrowserBorne over the mux) for loopback
// traffic; authorized remote traffic serves the mux directly; anything
// else from beyond the loopback is a bare 404.
func newTokenGate(token string, localGate, mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestFromLoopback(r) {
			localGate.ServeHTTP(w, r)
			return
		}
		present := bearerToken(r)
		if present == "" {
			present = r.URL.Query().Get("token") // WS dials and plain links
		}
		if present == "" || subtle.ConstantTimeCompare([]byte(present), []byte(token)) != 1 {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func requestFromLoopback(r *http.Request) bool { return httputil.ReqFromLoopback(r) }

// gateRefusal is the fail-close sentinel server.Start runs right after
// binding: a listener beyond the loopback without an armed token is a
// refusal, not a warning — the unauthenticated management face must
// never face a network by accident. A non-TCP or unparseable listener
// address counts as beyond the loopback (fail-close).
func gateRefusal(ln net.Listener, token string) error {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr.IP == nil || !addr.IP.IsLoopback() {
		if token == "" {
			return fmt.Errorf("监听 %v 离开回环但未配置访问令牌——管理端点不做匿名鉴权，拒绝裸奔；请设置 NIUMA_GATE_TOKEN（或让它自动铸造），或保持回环绑定", ln.Addr())
		}
		log.Printf("安全面：服务监听在非回环地址 %v——令牌门已武装（回环访问照旧零摩擦，远端须携带 Bearer 令牌或 ?token=）", ln.Addr())
	}
	return nil
}

func bearerToken(r *http.Request) string { return httputil.BearerToken(r) }
