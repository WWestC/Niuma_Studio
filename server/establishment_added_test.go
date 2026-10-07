package server

// 加编的招聘台两腿（v2.4 编制表面板的补缺）：行写成功后，范围房间落
// 一条 recorded [编制] 提示行（对话里的可见痕迹——面板写本是房主私有
// 的知识），并异步敲 Options.EstablishmentAdded；敲失败再落一条如实的
// 房主侧提示。钩子只在「加」时响：add、以及编制数上调的 update（扩编
// 也是加编）；减编/改名/删行一概不响。

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/kb"
)

type estAddEvent struct {
	project string
	row     kb.EstablishmentRow
	prev    int
	by      string
}

// startEstAddStudio boots a studio whose row face taps a channel-fed
// EstablishmentAdded hook, plus one observer on the lobby hub to read
// the recorded room lines.
func startEstAddStudio(t *testing.T, hookErr error) (*Server, *chat.Client, chan estAddEvent) {
	t.Helper()
	lobby := chat.NewHub()
	lobby.Join("房主", "boss", false)
	docs, err := kb.OpenDocs(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDocs: %v", err)
	}
	legacy := "# 编制表\n\n" +
		"| 岗位 key | 名称 | 身份 | 编制 | 自动补员 | 手册 |\n" +
		"|---|---|---|---|---|---|\n" +
		"| developer | 开发 | 前端开发 | 1 | 否 | roles/developer |\n"
	if _, err := docs.Write("ops/establishment", "编制表", legacy, "房主"); err != nil {
		t.Fatalf("预置表：%v", err)
	}
	events := make(chan estAddEvent, 4)
	s, err := Start(lobby, Options{Endpoint: EndpointConfig{
		PreferredPort: -1,
	},
		Stores: StoreSet{
			Docs: docs,
		},
		LocalName: "房主",
		Fleet: FleetFuncs{EstablishmentAddedFn: func(project string, row kb.EstablishmentRow, prev int, by string) error {
			events <- estAddEvent{project, row, prev, by}
			return hookErr
		}}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(s.Close)
	return s, lobby.AttachObserver(chat.LobbyKey, false, 0), events
}

// drainEstFrames collects the observer's frames for one settling beat.
func drainEstFrames(c *chat.Client) []chat.Message {
	var got []chat.Message
	deadline := time.After(250 * time.Millisecond)
	for {
		select {
		case m := <-c.Receive():
			got = append(got, m)
		case <-deadline:
			return got
		}
	}
}

func TestEstablishmentRowAddTapsHiringLeg(t *testing.T) {
	s, obs, events := startEstAddStudio(t, nil)
	_, rev := getEstRows(t, s)

	// add：钩子带全行字段（prev=0），房间落一条 recorded [编制] 行
	status, body := estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "add", "expect_rev": rev,
		"row": map[string]any{"key": "qa", "name": "测试", "role": "测试工程师",
			"headcount": 2, "auto_fill": true, "manual": "roles/qa"},
	})
	if status != http.StatusOK {
		t.Fatalf("加岗应 200，得 %d：%v", status, body)
	}
	select {
	case ev := <-events:
		if ev.project != chat.LobbyKey || ev.prev != 0 || ev.by != "房主" {
			t.Fatalf("add 钩子参数不对：%+v", ev)
		}
		if ev.row.Key != "qa" || ev.row.Role != "测试工程师" || ev.row.Headcount != 2 || !ev.row.AutoFill {
			t.Fatalf("add 钩子行漂移：%+v", ev.row)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("add 后 EstablishmentAdded 未被触发")
	}
	assertEstHint(t, drainEstFrames(obs), "qa", "编制 2", "已提醒 HR（人事）")

	// 扩编 update（1 → 3）：prev=1，提示行走差额口径
	_, rev = getEstRows(t, s)
	status, body = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "update", "key": "developer", "expect_rev": rev,
		"row": map[string]any{"key": "developer", "name": "开发", "role": "前端开发",
			"headcount": 3, "auto_fill": false, "manual": "roles/developer"},
	})
	if status != http.StatusOK {
		t.Fatalf("扩编应 200，得 %d：%v", status, body)
	}
	select {
	case ev := <-events:
		if ev.prev != 1 || ev.row.Key != "developer" || ev.row.Headcount != 3 {
			t.Fatalf("扩编钩子参数不对：%+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("扩编后 EstablishmentAdded 未被触发")
	}
	assertEstHint(t, drainEstFrames(obs), "developer", "编制 1 → 3", "已提醒 HR（人事）")

	// 减编 update 与 delete：不是加编，一概不响
	_, rev = getEstRows(t, s)
	status, _ = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "update", "key": "developer", "expect_rev": rev,
		"row": map[string]any{"key": "developer", "name": "开发", "role": "前端开发",
			"headcount": 1, "auto_fill": false, "manual": "roles/developer"},
	})
	if status != http.StatusOK {
		t.Fatalf("减编应 200，得 %d", status)
	}
	_, rev = getEstRows(t, s)
	if status, _ = estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "delete", "key": "qa", "expect_rev": rev}); status != http.StatusOK {
		t.Fatalf("删岗应 200，得 %d", status)
	}
	drainEstFrames(obs)
	select {
	case ev := <-events:
		t.Fatalf("减编/删岗不应触发加编钩子，却来了：%+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestEstablishmentRowAddHookFailureLeavesFallbackNote(t *testing.T) {
	s, obs, _ := startEstAddStudio(t, errors.New("boomed"))
	_, rev := getEstRows(t, s)
	status, _ := estRowPOST(t, s, "/kb/establishment/row", map[string]any{
		"op": "add", "expect_rev": rev,
		"row": map[string]any{"key": "qa", "name": "测试", "role": "测试工程师", "headcount": 1},
	})
	if status != http.StatusOK {
		t.Fatalf("钩子失败不应回滚写入，得 %d", status)
	}
	// 先见 recorded 提示行，再见如实的未送达提示（异步，宽容等待）
	var sawHint, sawFallback bool
	deadline := time.After(2 * time.Second)
	for !(sawHint && sawFallback) {
		select {
		case m := <-obs.Receive():
			if m.Type != chat.MsgSystem {
				continue
			}
			if contains(m.Text, "[编制]", "qa", "已提醒 HR") {
				sawHint = true
			}
			if contains(m.Text, "加编提醒未送达 HR", "@HR") {
				sawFallback = true
			}
		case <-deadline:
			t.Fatalf("应见提示行与未送达提示：hint=%v fallback=%v", sawHint, sawFallback)
		}
	}
}

// assertEstHint pins one recorded system line carrying every want.
func assertEstHint(t *testing.T, frames []chat.Message, wants ...string) {
	t.Helper()
	for _, m := range frames {
		if m.Type == chat.MsgSystem && contains(m.Text, wants...) {
			return
		}
	}
	t.Fatalf("房间应落一条带 %v 的 [编制] 系统行，得：%v", wants, frames)
}

func contains(s string, wants ...string) bool {
	for _, w := range wants {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
