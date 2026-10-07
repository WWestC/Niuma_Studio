package server

// 需求删除的 HTTP 腿（DELETE /p/{key}/reqs/{id}）：成功必须向项目房落
// 一帧 req{deleted}——带被删快照、from＝房主（本机面，调用者即房主），
// 与立案 created 同一道门（绿点喂帧）；台账 GET 随之少一条。拒绝路径：
// split（任务树之母）/closed（终局留档）400 带库的原因且房间不闻；跨
// 项目的 id 不许从别家门牌删（plan-submit 的归属规矩）；门牌不明 404、
// 方法不对 405、id 不存在 400。

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/projects"
	"github.com/WWestC/Niuma_Studio/requirements"
)

// reqDeletedFrames filters down to req{deleted} room broadcasts.
func reqDeletedFrames(frames []chat.Message) []chat.Message {
	var out []chat.Message
	for _, m := range frames {
		if m.Type == chat.MsgReq && m.Event == "deleted" && m.Req != nil {
			out = append(out, m)
		}
	}
	return out
}

// startReqServer boots the minimal deletion rig: lobby project, empty
// requirement store, no registry (project rooms fall back to the lobby
// hub — the broadcast lands where roomHub points).
func startReqServer(t *testing.T) (*Server, requirements.Store) {
	t.Helper()
	lobby := chat.NewHub()
	projStore, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := projStore.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	reqs := requirements.OpenMemory()
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			ProjectStore: projStore,
			Requirements: reqs,
		},
		LocalName: "房主"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, reqs
}

func deleteReqHTTP(t *testing.T, s *Server, key, id string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete,
		addrHTTP(s)+"/p/"+key+"/reqs/"+id, nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /p/%s/reqs/%s: %v", key, id, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func ledgerLen(t *testing.T, s *Server, key string) int {
	t.Helper()
	resp, err := http.Get(addrHTTP(s) + "/p/" + key + "/reqs")
	if err != nil {
		t.Fatalf("GET /reqs: %v", err)
	}
	defer resp.Body.Close()
	var rep struct {
		Reqs []requirements.Req `json:"reqs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode ledger: %v", err)
	}
	return len(rep.Reqs)
}

func TestReqHTTPDeleteBroadcastsDeleted(t *testing.T) {
	s, reqs := startReqServer(t)
	filed, err := reqs.Create("", chat.LobbyKey, "误录需求", "手滑立重了", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	obs := s.hub.AttachObserver(chat.LobbyKey, false, 0)

	resp := deleteReqHTTP(t, s, chat.LobbyKey, filed.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除应 200，得 %d", resp.StatusCode)
	}
	var rep struct {
		Project string           `json:"project"`
		Req     requirements.Req `json:"req"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if rep.Project != chat.LobbyKey || rep.Req.ID != filed.ID || rep.Req.Title != "误录需求" {
		t.Fatalf("回执应带被删快照：%+v", rep)
	}
	if got := ledgerLen(t, s, chat.LobbyKey); got != 0 {
		t.Fatalf("删除后台账应空，得 %d 条", got)
	}
	// 房间必须听见一帧 req{deleted}（被删快照＋房主落款）
	got := reqDeletedFrames(drainReqFrames(obs))
	if len(got) != 1 {
		t.Fatalf("应广播一帧 req deleted，得 %d", len(got))
	}
	if got[0].Req.ID != filed.ID || got[0].From != "房主" {
		t.Fatalf("广播快照/落款不符：%+v %q", got[0].Req, got[0].From)
	}
}

func TestReqHTTPDeleteRefusesSplit(t *testing.T) {
	s, reqs := startReqServer(t)
	filed, err := reqs.Create("", chat.LobbyKey, "已拆需求", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := reqs.MarkSplit("", chat.LobbyKey, filed.ID); err != nil {
		t.Fatalf("split: %v", err)
	}
	obs := s.hub.AttachObserver(chat.LobbyKey, false, 0)

	resp := deleteReqHTTP(t, s, chat.LobbyKey, filed.ID)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("split 需求删除应 400，得 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "仅 open") {
		t.Fatalf("400 应带库的原因，得 %q", string(body))
	}
	if got := ledgerLen(t, s, chat.LobbyKey); got != 1 {
		t.Fatalf("拒绝路径台账不动，得 %d 条", got)
	}
	if got := reqDeletedFrames(drainReqFrames(obs)); len(got) != 0 {
		t.Fatalf("拒绝删除房间不闻，得 %d 帧", len(got))
	}
}

func TestReqHTTPDeleteGuards(t *testing.T) {
	s, reqs := startReqServer(t)
	if _, err := s.opts.Stores.ProjectStore.Create(projects.Project{Key: "proj-r",
		Name: "别家项目", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.opts.Stores.ProjectStore.Activate("proj-r"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	filed, err := reqs.Create("", "proj-r", "别家需求", "", "小牛")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 跨项目：号段分家后别家门牌上根本没有这个号——查无即拒（动手前
	// 拦，不产生半删状态）
	resp := deleteReqHTTP(t, s, chat.LobbyKey, filed.ID)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("跨项目删除应 400，得 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "不存在") {
		t.Fatalf("跨项目 400 应如实报查无（含项目名），得 %q", string(body))
	}
	if _, ok := reqs.GetIn("", "proj-r", filed.ID); !ok {
		t.Fatal("跨项目拒绝路径不许真删")
	}

	// 门牌不明 404（/p/{key} 全家同一规矩）
	if resp := deleteReqHTTP(t, s, "nope", filed.ID); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知项目应 404，得 %d", resp.StatusCode)
	}
	// id 不存在 400 带原因
	if resp := deleteReqHTTP(t, s, chat.LobbyKey, "r_99"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("不存在的需求应 400，得 %d", resp.StatusCode)
	}
	// 单条脸只收 DELETE
	resp2, err := http.Get(addrHTTP(s) + "/p/" + chat.LobbyKey + "/reqs/r_01")
	if err != nil {
		t.Fatalf("GET /reqs/{id}: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("单条脸 GET 应 405，得 %d", resp2.StatusCode)
	}
}
