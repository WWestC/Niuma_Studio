package main

// boot_smoke_test.go — boot 序列冒烟（ARCHITECTURE.md 的待办钉）：
// 按 main() 的真实次序走完 组合根四阶段 的前三段（openStores →
// bootDispatch(--no-dispatch) → startServerFace），临时端口起监听后
// HTTP 探针十一面：/app 工作台壳、/app/app.css 样式、/members 名册、
// /zcode 启动门（--no-dispatch 的 off 态）、/ 信息面、/owner/token
// 房主凭证线、/kb/people 名册聚合（Engine＋AgentStore＋LocalName 的
// 装配链）、/projects 项目面、/skills 技能库、/plugins.json 插件面
// （含出厂市场预装线）、/dispatch 断 404（--no-dispatch＝面缺席而不
// 是坏——off 态的正确形状）。要抓的是阶段次序与装配面的回归——任何
// 一段的签名或次序不变量被破坏，这里先红（此前只有三平台 CI 的构建
// 能兜底，跑起来的第一面无人看守）。runShell 不进冒烟（窗口/托盘是
// 进程形态，不是被测契约）。本测试无共享状态、全程临时家——`go test
// -race` 下同样成立，动 store/boot 并发面前的先这么跑一遍。

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/util"
)

func TestBootSmokeServesTheWorkbench(t *testing.T) {
	// 全部家路径（v2Root/openProjectStore/stableWorkspace/发现文件）落
	// 临时目录——测试不碰操作者的 ~/.niuma（dispatch 侧 TestMain 的
	// 双保险墙纪律，这里以 t.Setenv 达成同效）。
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱

	util.MigrateLegacyPaths()

	// main() 的有序组合，逐段同参（角色串照抄旗标缺省）。
	projStore := openProjectStore()
	lobbyWS := stableWorkspace()
	if _, err := projStore.EnsureLobby(lobbyWS); err != nil {
		t.Fatalf("lobby: %v", err)
	}
	registry := chat.NewRegistry(projStore, v2Root(), defaultName(), "人类玩家")
	if _, err := registry.AdoptActive(); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	hub, err := registry.Lobby()
	if err != nil {
		t.Fatalf("lobby hub: %v", err)
	}
	snapshotPath := registry.SnapshotPath(chat.LobbyKey)

	st := openStoresT(t, projStore, registry, hub, "人类玩家")
	var srv *server.Server
	dp := bootDispatch(hub, registry, projStore, lobbyWS, snapshotPath, st, true, &srv)
	sf := startServerFace(-1, false, true, hub, registry, projStore, snapshotPath, st, dp)
	srv = sf.srv
	if srv == nil {
		t.Fatal("startServerFace 未返回服务器")
	}
	t.Cleanup(func() { srv.Close() })
	base := "http://127.0.0.1:" + strconv.Itoa(srv.Port)

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, string(b)
	}

	// ① 工作台壳：embed 的 index.html 应带启动门与样式链接。
	if code, body := get("/app"); code != 200 || !strings.Contains(body, `id="boot"`) || !strings.Contains(body, "/app/app.css") {
		t.Fatalf("/app 形状不合：code=%d boot链接=%v css=%v", code,
			strings.Contains(body, `id="boot"`), strings.Contains(body, "/app/app.css"))
	}
	// ② 抽出的样式资产：与壳同一条 ETag 管线服务。
	if code, body := get("/app/app.css"); code != 200 || !strings.Contains(body, "--board") {
		t.Fatalf("/app/app.css 形状不合：code=%d", code)
	}
	// ③ 名册读面：JSON 名册（房主预座在场）。
	if code, body := get("/members"); code != 200 || !strings.Contains(body, defaultName()) {
		t.Fatalf("/members 形状不合：code=%d", code)
	}
	// ④ 启动门：--no-dispatch 的 off 态如实上报。
	if code, body := get("/zcode"); code != 200 || !strings.Contains(body, `"off"`) {
		t.Fatalf("/zcode 应报 off 态：code=%d", code)
	}
	// ⑤ 信息面：根路径的版本/地址 JSON（组合根的 version 注入）。
	if code, body := get("/"); code != 200 || !strings.Contains(body, version) {
		t.Fatalf("/ 信息面形状不合：code=%d 含版本=%v", code, strings.Contains(body, version))
	}
	// ⑥ 房主凭证线：回环 GET /owner/token 拿工作台要的那枚凭证——
	// Registry 与 OwnerCredential 的装配在 openStores 里落位（快照恢复
	// 前房主先入座、凭证先落盘）。
	if code, body := get("/owner/token"); code != 200 || !strings.Contains(body, `"token"`) {
		t.Fatalf("/owner/token 形状不合：code=%d", code)
	}
	// ⑦ 名册聚合：kb/people 是 Engine＋AgentStore＋LocalName 三方装配
	// 的读面，房主旗标要求 ownerName 正确穿到 Options.LocalName。
	if code, body := get("/kb/people"); code != 200 || !strings.Contains(body, defaultName()) {
		t.Fatalf("/kb/people 形状不合：code=%d 含房主=%v", code, strings.Contains(body, defaultName()))
	}
	// ⑧ 项目面：openProjectStore＋EnsureLobby 的大厅在列（键 "default"）。
	if code, body := get("/projects"); code != 200 || !strings.Contains(body, chat.LobbyKey) {
		t.Fatalf("/projects 形状不合：code=%d 含大厅键=%v", code, strings.Contains(body, chat.LobbyKey))
	}
	// ⑨ 技能库：capability.Open（含旧能力包迁移线）后空库也是合法 200。
	if code, _ := get("/skills"); code != 200 {
		t.Fatalf("/skills 应 200：code=%d", code)
	}
	// ⑩ 插件面：出厂市场预装线在 openStores 里跑过，索引面照常答。
	if code, body := get("/plugins.json"); code != 200 || !strings.Contains(body, `"api"`) {
		t.Fatalf("/plugins.json 形状不合：code=%d", code)
	}
	// ⑪ 调度管理面的 off 态：--no-dispatch 时 DispatchHandler 为 nil，
	// /dispatch 应整体缺席（404）——off 的正确形状是面不在，不是面坏了。
	if code, _ := get("/dispatch"); code != 404 {
		t.Fatalf("/dispatch 在 --no-dispatch 下应 404：code=%d", code)
	}
}
