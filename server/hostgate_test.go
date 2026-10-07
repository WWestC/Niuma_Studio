package server

// hostgate_test.go — 浏览器侧门闩的钉子（安全核查修复）：rebinding 形状
// （Host=攻击者域名）死、跨站 Origin 直打 POST 死、Sec-Fetch-Site 自称
// cross-site 死；本地客户端的形状（回环 Host、不带 Origin）原样过。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gateTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(gateBrowserBorne(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})))
	t.Cleanup(ts.Close)
	return ts
}

func gateGet(t *testing.T, ts *httptest.Server, host, origin, sfs string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/reset", strings.NewReader(`{"confirm":"RESET"}`))
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if sfs != "" {
		req.Header.Set("Sec-Fetch-Site", sfs)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestHostGateBlocksRebindingAndCrossSite(t *testing.T) {
	ts := gateTestServer(t)

	cases := []struct {
		name       string
		host       string
		origin     string
		sfs        string
		wantStatus int
	}{
		// rebinding 形状：Host 是攻击者域名（浏览器把 attacker.com 解析到
		// 127.0.0.1），即便 Origin 与之"同源"也死。
		{"rebinding host", "attacker.com:7777", "http://attacker.com:7777", "same-origin", http.StatusNotFound},
		{"rebinding host no headers", "attacker.com", "", "", http.StatusNotFound},
		// 跨站直打：Host 是回环（浏览器允许向回环 IP 发 no-cors POST），
		// 但 Origin 暴露了发起方。
		{"cross-site origin post", "", "http://evil.example", "", http.StatusNotFound},
		{"cross-site fetch-site", "", "", "cross-site", http.StatusNotFound},
		{"opaque origin", "", "null", "", http.StatusNotFound},
		// 本地客户端与正常工作台页面的形状。
		{"plain local client", "", "", "", http.StatusOK},
		{"localhost host", "localhost", "", "", http.StatusOK},
		{"ipv6 loopback host", "[::1]:7777", "", "", http.StatusOK},
		{"same-origin browser", "", ts.URL, "same-origin", http.StatusOK},
		{"user-typed navigation", "", "", "none", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gateGet(t, ts, tc.host, tc.origin, tc.sfs); got != tc.wantStatus {
				t.Fatalf("status = %d, want %d", got, tc.wantStatus)
			}
		})
	}
}
