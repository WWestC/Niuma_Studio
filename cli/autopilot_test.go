package cli

// autopilot_test.go — niuma autopilot complete 的拨号面（recruitBirth
// 同款 httptest WS 台架）：动词拨进项目房、落 autopilot_done{note}、
// 私有 autopilot{completed} 回执零退；denied 回执非零退且理由上 stderr。

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// apDoneStub stands a room that handshakes one dialer, captures the
// autopilot_done frame, and answers with the given private reply.
func apDoneStub(t *testing.T, reply chat.Message) (*httptest.Server, *string, *string) {
	t.Helper()
	var frameType, note string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		var hello chat.Message
		if err := wsjson.Read(ctx, c, &hello); err != nil {
			return
		}
		if err := wsjson.Write(ctx, c, chat.Message{Type: chat.MsgWelcome,
			Project: hello.Project, You: &chat.Member{Name: hello.Name}}); err != nil {
			return
		}
		var in chat.Message
		if err := wsjson.Read(ctx, c, &in); err != nil {
			return
		}
		frameType, note = in.Type, in.Text
		ans := reply
		ans.From = hello.Name
		_ = wsjson.Write(ctx, c, ans)
		var bye chat.Message // taskBye 收尾；读到即静默放行
		_ = wsjson.Read(ctx, c, &bye)
	}))
	t.Cleanup(srv.Close)
	return srv, &frameType, &note
}

// TestAutopilotCompleteRoundTrip: 拨进 book 房、帧型 autopilot_done、
// note 原样落帧、completed 回执零退。
func TestAutopilotCompleteRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())        // 隔离 loadSeatToken 的真机座位令牌
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE
	srv, frameType, note := apDoneStub(t, chat.Message{
		Type: chat.MsgAutopilot, Event: "completed",
		Text: "收摊完成：自动补货与自动推进均已关闭（房内已播报系统行）"})
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	rc := runAutopilot([]string{"complete", "--project", "book", "--name", "小苗",
		"--note", "三章全部过审", "--port", strconv.Itoa(port)})
	if rc != 0 {
		t.Fatalf("autopilot complete rc = %d, want 0", rc)
	}
	if *frameType != chat.MsgAutopilotDone {
		t.Fatalf("帧型 = %q, want %q", *frameType, chat.MsgAutopilotDone)
	}
	if *note != "三章全部过审" {
		t.Fatalf("note = %q, want 三章全部过审", *note)
	}
}

// TestAutopilotCompleteDeniedFails: denied 回执非零退（编排者门拒绝，
// 理由由 stderr 承载）。
func TestAutopilotCompleteDeniedFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	srv, frameType, _ := apDoneStub(t, chat.Message{
		Type: chat.MsgAutopilot, Event: "denied",
		Text: "收摊只能由本房在岗编排者（岗位「排期编排」）发起"})
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	rc := runAutopilot([]string{"complete", "--project", "book", "--name", "小猿",
		"--port", strconv.Itoa(port)})
	if rc != 1 {
		t.Fatalf("denied 应非零退（rc = %d）", rc)
	}
	if *frameType != chat.MsgAutopilotDone {
		t.Fatalf("denied 路径同样应落帧（got %q）", *frameType)
	}
}
