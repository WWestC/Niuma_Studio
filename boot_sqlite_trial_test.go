package main

// boot_sqlite_trial_test.go — 前置试用 B（Phase 1 第二批）：
// NIUMA_STORE=sqlite 下的真实产品流一轮。
//
// 载具说明（与「./build.sh dev 起真窗口」的偏差，实录已记录）：7777 被
// 操作者正在运行的真实工作室持有，ARB-1 守卫拒绝非交互启动接管、多开被
// 机器级禁止——第二实例不允许存在。故以 main() 的真实三阶段组合
// （openStores → bootDispatch → startServerFace，冒烟测试同款载具，
// 临时家＋临时端口）起两个连续「工作室进程」，产品流走真实面：建房与
// 需求走 HTTP 面（POST /projects、POST /p/{key}/reqs），提案与任务
// 流转走房主 mirror WS 通道（plan_submit / plan_accept / task_update /
// rank_set——与 niuma CLI 同一条拨号与信封形状）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/server"
	"github.com/WWestC/Niuma_Studio/util"
	"github.com/WWestC/Niuma_Studio/wire"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// trialLogBuf 收 boot/运行期日志：降级行的扫描面（log 自身串行写，
// bytes.Buffer 免锁安全）。
type trialLogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *trialLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *trialLogBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// bootTrialStudio 按 main() 的次序起一个「工作室进程」（临时端口、
// no-dispatch），返回可操作的面。
func bootTrialStudio(t *testing.T) (storeSet, *server.Server) {
	t.Helper()
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
	// 不注册 Cleanup 的 Close：用例自管生命周期（实例①中途关停再起②，
	// 双 Close 会 panic——server.Close 幂等性不在本用例的看守面）。
	return st, srv
}

// trialMirror 以房主凭证拨一条 mirror 通道（CLI dialTokenRoom 同形），
// 返回帧收发器。project 空＝大厅。
func trialMirror(t *testing.T, srv *server.Server, token, project string) (*websocket.Conn, chan wire.Message) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", srv.Port), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	replay := false
	hello := map[string]any{
		"type": "hello", "name": defaultName(), "role": "房主",
		"replay": replay, "token": token,
	}
	if project != "" {
		hello["project"] = project
	}
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var welcome wire.Message
	if err := wsjson.Read(ctx, conn, &welcome); err != nil || welcome.Type != "welcome" {
		t.Fatalf("welcome 形状不合：%v %+v", err, welcome)
	}
	// 帧泵：收到的帧进信道，await 按 type/event 挑。
	frames := make(chan wire.Message, 64)
	go func() {
		for {
			var m wire.Message
			if err := wsjson.Read(context.Background(), conn, &m); err != nil {
				close(frames)
				return
			}
			select {
			case frames <- m:
			default: // 慢消费即丢——试用只等回执，不排全量
			}
		}
	}()
	return conn, frames
}

// awaitFrame 等一条 type（可再叠 event）的帧，超时即败。
func awaitFrame(t *testing.T, frames chan wire.Message, typ, event string) wire.Message {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case m, ok := <-frames:
			if !ok {
				t.Fatal("mirror 连接在等回执时断开")
			}
			if m.Type == typ && (event == "" || m.Event == event) {
				return m
			}
		case <-deadline:
			t.Fatalf("等 %s/%s 回执超时", typ, event)
		}
	}
}

func sendFrame(t *testing.T, conn *websocket.Conn, frame map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, frame); err != nil {
		t.Fatalf("发帧失败：%v（%+v）", err, frame)
	}
}

func TestSQLiteTrialProductFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	util.MigrateLegacyPaths()

	lb := &trialLogBuf{}
	oldOutput := logSwapOutput(lb)
	defer log.SetOutput(oldOutput)

	// —— 实例 ①：起工作室 ——
	st1, srv1 := bootTrialStudio(t)
	owner := st1.ownerName
	// 房主凭证：openStores 落盘的 owner 凭证（mirror 拨号要带）。
	cred := chat.LoadSeatToken("", owner)
	if cred == "" {
		t.Fatal("房主凭证未落盘（openStores 应为 owner 铸凭）")
	}
	dbPath := filepath.Join(v2Root(), "studio.db")
	sizeAfterBoot := dbSize(t, dbPath)

	// —— 产品流 ①：建房（HTTP 立项面）＋开张（lifecycle 面） ——
	ws := t.TempDir() // 项目工作区（立项校验要求绝对路径）
	createProject(t, srv1, "book", "账本项目", ws)
	postJSON(t, srv1, "/p/book/lifecycle", map[string]any{"action": "activate"},
		func(code int, body string) {
			if code != 200 || !strings.Contains(body, `"active"`) {
				t.Fatalf("项目开张应成 active：code=%d body=%s", code, body)
			}
		})

	// —— 产品流 ②：req_create（HTTP 工单面，P4-d 裁定） ——
	postJSON(t, srv1, "/p/book/reqs", map[string]any{
		"title": "做一本账", "body": "记清楚每一笔",
	}, func(code int, body string) {
		if code != 200 || !strings.Contains(body, `"r_01"`) {
			t.Fatalf("req 建单应成 r_01：code=%d body=%s", code, body)
		}
	})

	// —— 产品流 ③④：plan_submit → plan_accept（房主 mirror WS 通道） ——
	connB, framesB := trialMirror(t, srv1, cred, "book")
	sendFrame(t, connB, map[string]any{
		"type": "plan_submit",
		"plan": map[string]any{
			"title": "账本拆解", "req": "r_01",
			"tasks": []map[string]any{
				{"title": "打地基"},
				{"title": "验收账面", "deps": []int{1}},
			},
		},
	})
	sub := awaitFrame(t, framesB, "plan", "submitted")
	if sub.Plan == nil || sub.Plan.ID != "p_01" || len(sub.Plan.Tasks) != 2 {
		t.Fatalf("提案应 p_01 两任务：%+v", sub.Plan)
	}
	getJSON(t, srv1, "/p/book/plan", func(code int, body string) {
		if code != 200 || !strings.Contains(body, "p_01") {
			t.Fatalf("槽内应有 p_01：code=%d body=%s", code, body)
		}
	})
	sendFrame(t, connB, map[string]any{
		"type": "plan_accept", "plan_id": "p_01", "project": "book",
	})
	acc := awaitFrame(t, framesB, "plan", "accepted")
	if !strings.Contains(acc.Text, "2 条任务入库") {
		t.Fatalf("接受回执应报 2 条入库：%s", acc.Text)
	}

	// —— 产品流 ⑤：任务状态流转（t_01 todo→doing→done） ——
	for _, st := range []string{"doing", "done"} {
		sendFrame(t, connB, map[string]any{
			"type": "task_update", "task_id": "t_01", "project": "book",
			"patch": map[string]any{"status": st, "note": "流转到 " + st},
		})
		up := awaitFrame(t, framesB, "task", "updated")
		if up.Task == nil || up.Task.Status != st {
			t.Fatalf("t_01 应流转到 %s：%+v", st, up.Task)
		}
	}

	// —— 产品流 ⑥：rank_set（大厅锚定的全局等级面） ——
	connL, framesL := trialMirror(t, srv1, cred, "")
	sendFrame(t, connL, map[string]any{"type": "rank_set", "name": "小猿", "rank": 3})
	rk := awaitFrame(t, framesL, "system", "")
	if !strings.Contains(rk.Text, "小猿") || !strings.Contains(rk.Text, "3") {
		t.Fatalf("rank_set 回执应报小猿调级：%s", rk.Text)
	}

	// —— 流内自检：三仓状态如实 ——
	if r, ok := st1.reqs.GetIn("", "book", "r_01"); !ok || r.Status != "split" {
		t.Fatalf("接受后 r_01 应 split：%+v %v", r, ok)
	}
	if p := st1.plans.Pending("", "book"); p != nil {
		t.Fatalf("接受后槽应空：%+v", p)
	}
	if task, ok := st1.engine.GetIn("book", "t_01"); !ok || task.Status != "done" || task.DoingTS == 0 || task.DoneTS == 0 {
		t.Fatalf("t_01 应 done 且工作钟落点：%+v %v", task, ok)
	}
	sizeAfterFlow := dbSize(t, dbPath)

	// JSON 架不新增写入：sqlite 下四仓（本批起含 tasks）的 JSON 目录
	// 与平铺等级文件从头到尾不该出现——任务/等级也住进了 studio.db。
	for _, dir := range []string{"requirements", "plan", "merge", "tasks"} {
		if _, err := os.Stat(filepath.Join(v2Root(), dir)); !os.IsNotExist(err) {
			t.Fatalf("sqlite 模式不应出现 JSON 架目录 %s：%v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".niuma_ranks.json")); !os.IsNotExist(err) {
		t.Fatalf("sqlite 模式不应写平铺等级文件：%v", err)
	}

	// —— 重启：实例 ① 关停，实例 ② 同一家重开 ——
	srv1.Close()
	st2, srv2 := bootTrialStudio(t)
	t.Cleanup(srv2.Close)

	// 需求与提案槽：HTTP 读面
	getJSON(t, srv2, "/p/book/reqs", func(code int, body string) {
		if code != 200 || !strings.Contains(body, `"r_01"`) || !strings.Contains(body, "split") {
			t.Fatalf("重启后 r_01 应 split：code=%d body=%s", code, body)
		}
	})
	getJSON(t, srv2, "/p/book/plan", func(code int, body string) {
		if code != 200 || !strings.Contains(body, `"plan":null`) {
			t.Fatalf("重启后槽应空：code=%d body=%s", code, body)
		}
	})
	// 任务与等级：引擎直读（重启后的新引擎）
	if task, ok := st2.engine.GetIn("book", "t_01"); !ok || task.Status != "done" || task.DoneTS == 0 {
		t.Fatalf("重启后 t_01 应保持 done：%+v %v", task, ok)
	}
	if task, ok := st2.engine.GetIn("book", "t_02"); !ok || task.Status != "todo" || len(task.Deps) != 1 || task.Deps[0] != "t_01" {
		t.Fatalf("重启后 t_02 应 todo 且依赖 t_01：%+v %v", task, ok)
	}
	if st2.engine.Rank("小猿") != 3 {
		t.Fatalf("重启后小猿应 Lv.3：%d", st2.engine.Rank("小猿"))
	}
	// 发号高水位：重启后 book 架下一张是 t_03（删号不重发的前置形状）
	if out := st2.engine.Create("book", owner, "重启后的新单", "", ""); out.Denied || out.Task.ID != "t_03" {
		t.Fatalf("重启后 book 架应发 t_03：%+v", out)
	}
	sizeAfterRestart := dbSize(t, dbPath)

	// 库文件增长合理性（记录进实录）
	t.Logf("studio.db 大小：开盘后 %d B → 产品流后 %d B → 重启写入后 %d B",
		sizeAfterBoot, sizeAfterFlow, sizeAfterRestart)
	if sizeAfterFlow <= sizeAfterBoot {
		t.Fatalf("产品流后库应增长：%d → %d", sizeAfterBoot, sizeAfterFlow)
	}

	// —— 日志降级行扫描 ——
	logScan(t, lb.String())
	t.Logf("试用全程日志（%d 行）：\n%s", strings.Count(lb.String(), "\n"), lb.String())
}

// dbSize 合并 db＋-wal＋-shm：WAL 模式下写先进 -wal，主文件要等
// checkpoint 才长——只量主文件会把「增长合理」误判成「没写进去」。
func dbSize(t *testing.T, path string) int64 {
	t.Helper()
	var total int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", p, err)
		}
	}
	return total
}

// logSwapOutput swaps the standard logger's output to w and returns the
// previous output (the restore value).
func logSwapOutput(w io.Writer) io.Writer {
	old := log.Writer()
	log.SetOutput(w)
	return old
}

func createProject(t *testing.T, srv *server.Server, key, name, workspace string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"key": key, "name": name, "workspace": workspace, "by": defaultName(),
	})
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/projects", srv.Port),
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("立项失败：%v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("立项应 200：%d %s", resp.StatusCode, b)
	}
}

func postJSON(t *testing.T, srv *server.Server, path string, payload map[string]any, check func(int, string)) {
	t.Helper()
	body, _ := json.Marshal(payload)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", srv.Port, path),
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	check(resp.StatusCode, string(b))
}

func getJSON(t *testing.T, srv *server.Server, path string, check func(int, string)) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", srv.Port, path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	check(resp.StatusCode, string(b))
}

// logScan 断言试用日志无未预期降级行（关键词面＝各 store 的降级口径）。
func logScan(t *testing.T, logText string) {
	t.Helper()
	bad := []string{
		"以内存", "starting empty", "请检查数据库", "未生效", "未落库",
		"corrupt", "回落", "重置台账", "导入中断",
	}
	for _, kw := range bad {
		if strings.Contains(logText, kw) {
			t.Fatalf("试用日志出现降级行（%s）：\n%s", kw, logText)
		}
	}
}
