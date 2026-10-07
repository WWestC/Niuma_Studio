package server

// meetingctl_test.go — 会议钟动词的服务端闸门：meeting_end /
// meeting_extend 只归本场主持（召集评审的编排者），非主持、空闲房间、
// 钩子缺失、调度器拒执各自拿到响亮的私信拒执；主持的控制原样路由到
// Options.MeetingCtl（main 接到 fleet，fleet 送到议程协程）。

import (
	"errors"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/meeting"
)

// ctlStage is one server over an in-memory meeting store, with the
// hook recording calls.
type ctlStage struct {
	s     *Server
	hub   *chat.Hub
	calls []ctlCall
}
type ctlCall struct {
	project string
	actor   string
	end     bool
	minutes int
}

func newCtlStage(t *testing.T, hookErr error) *ctlStage {
	t.Helper()
	hub := chat.NewHub()
	meets := meeting.OpenMemory()
	st := &ctlStage{hub: hub}
	s, err := Start(hub, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			Meetings: meets,
		},
		LocalName: "房主",
		Fleet: FleetFuncs{MeetingCtlFn: func(project, actor string, end bool, minutes int) (string, error) {
			st.calls = append(st.calls, ctlCall{project, actor, end, minutes})
			if hookErr != nil {
				return "", hookErr
			}
			return "（回执）", nil
		}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	st.s = s
	return st
}

func TestMeetingCtlChairGate(t *testing.T) {
	st := newCtlStage(t, nil)
	if _, err := st.s.opts.Stores.Meetings.Begin(chat.LobbyKey, "r_01", "需求", "小牛", []string{"老品"}); err != nil {
		t.Fatal(err)
	}

	// 主持：路由到钩子，参数原样
	if msg := st.s.plansFace().MeetingCtlAs(st.hub, nil, "小牛", true, 0); msg.Event == "denied" {
		t.Fatalf("主持的 end 不应被拒：%s", msg.Text)
	}
	if msg := st.s.plansFace().MeetingCtlAs(st.hub, nil, "小牛", false, 15); msg.Event == "denied" {
		t.Fatalf("主持的 extend 不应被拒：%s", msg.Text)
	}
	if len(st.calls) != 2 {
		t.Fatalf("钩子应被调两次，得到 %d 次", len(st.calls))
	}
	if st.calls[0].project != chat.LobbyKey || st.calls[0].actor != "小牛" || !st.calls[0].end {
		t.Fatalf("end 参数不对：%+v", st.calls[0])
	}
	if st.calls[1].minutes != 15 || st.calls[1].end {
		t.Fatalf("extend 参数不对：%+v", st.calls[1])
	}

	// 非主持（参会人、房主之外的路人）：响亮拒执，钩子不再被调
	n := len(st.calls)
	for _, who := range []string{"老品", "路人"} {
		msg := st.s.plansFace().MeetingCtlAs(st.hub, nil, who, false, 10)
		if msg.Event != "denied" || msg.Type != chat.MsgMeeting {
			t.Fatalf("%s 的控制应被私信拒执，得到 %+v", who, msg)
		}
		if msg.Text == "" {
			t.Fatalf("拒执应带理由")
		}
	}
	if len(st.calls) != n {
		t.Fatalf("非主持不应触达钩子，得到 %d 次", len(st.calls)-n)
	}
}

func TestMeetingCtlNoMeetingAndHookFailures(t *testing.T) {
	// 空闲房间：没有可控制的会议
	st := newCtlStage(t, nil)
	if msg := st.s.plansFace().MeetingCtlAs(st.hub, nil, "小牛", true, 0); msg.Event != "denied" {
		t.Fatalf("空闲房间应被拒，得到 %+v", msg)
	}

	// 调度器拒执（fleet 回的错误）原样变成私信拒执
	st2 := newCtlStage(t, errors.New("议程正在换挡（窗口切换中），稍候重试"))
	if _, err := st2.s.opts.Stores.Meetings.Begin(chat.LobbyKey, "r_01", "需求", "小牛", nil); err != nil {
		t.Fatal(err)
	}
	msg := st2.s.plansFace().MeetingCtlAs(st2.hub, nil, "小牛", false, 10)
	if msg.Event != "denied" || msg.Text != "议程正在换挡（窗口切换中），稍候重试" {
		t.Fatalf("钩子错误应转成拒执原文，得到 %+v", msg)
	}

	// 钩子未接线：动词直接拒
	hub := chat.NewHub()
	meets := meeting.OpenMemory()
	s, err := Start(hub, Options{Endpoint: EndpointConfig{PreferredPort: -1}, Stores: StoreSet{Meetings: meets}, LocalName: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := meets.Begin(chat.LobbyKey, "r_01", "需求", "小牛", nil); err != nil {
		t.Fatal(err)
	}
	if msg := s.plansFace().MeetingCtlAs(hub, nil, "小牛", true, 0); msg.Event != "denied" {
		t.Fatalf("无钩子应被拒，得到 %+v", msg)
	}
}
