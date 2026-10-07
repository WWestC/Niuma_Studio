package zcode

// failclose_test.go — answerServerRequest 的 fail-close 默认直测（安全
// 核查修复补钉）：这是整个代理权限模型最承重的几行——OnReverse 缺席或
// 放空时，未知/未应答的服务端请求必须得到 deny/decline/-32601，绝不
// 回空结果（空结果会被读成同意）。

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// answerOnce wires a pipe-backed Client, drives one answerServerRequest
// and returns the JSON line it wrote to the wire.
func answerOnce(t *testing.T, method string, params string, onRev func(string, map[string]any) (any, error)) string {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { stdinW.Close() })
	c := &Client{stdin: stdinW, opts: Options{OnReverse: onRev}}
	go func() {
		c.answerServerRequest(json.RawMessage(`"srv-7"`), method, json.RawMessage(params))
		stdinW.Close()
	}()
	ch := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(stdinR).ReadBytes('\n')
		if err == nil {
			ch <- string(line)
		}
		close(ch)
	}()
	select {
	case line := <-ch:
		if line == "" {
			t.Fatalf("%s：fail-close 默认必须写回应答，不得静默", method)
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatalf("%s：应答未在 5s 内上 wire", method)
		return ""
	}
}

func TestAnswerServerRequestFailCloseDefaults(t *testing.T) {
	cases := []struct {
		method  string
		wantSub string // 应答里必须出现的片段
	}{
		{"interaction/requestPermission", `"decision":"deny"`},
		{"interaction/requestUserInput", `"action":"decline"`},
		{"workspace/whateverUnknown", `-32601`},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			line := answerOnce(t, tc.method, `{}`, nil)
			for _, sub := range []string{tc.wantSub, `"srv-7"`} {
				if !strings.Contains(line, sub) {
					t.Fatalf("应答应含 %s，实际：%s", sub, strings.TrimSpace(line))
				}
			}
			// deny/decline 之外不得出现同意形状（空 result 就是同意）。
			if strings.Contains(line, `"decision":"allow"`) || strings.Contains(line, `"action":"accept"`) {
				t.Fatalf("默认应答不得放行：%s", line)
			}
		})
	}
}

func TestAnswerServerRequestRuntimePrefsAreFailSafe(t *testing.T) {
	line := answerOnce(t, "session/requestRuntimePreferences", `{}`, nil)
	for _, sub := range []string{`"nativeSearchEnhancementsEnabled":false`, `"memoryEnabled":false`} {
		if !strings.Contains(line, sub) {
			t.Fatalf("运行时偏好必须是 fail-safe 关闭集，应含 %s：%s", sub, line)
		}
	}
}

func TestAnswerServerRequestOnReverseVerdictWins(t *testing.T) {
	// OnReverse 给了裁决就原样上 wire——包括显式拒绝（err 路径走 -32601）。
	line := answerOnce(t, "interaction/requestPermission", `{}`,
		func(string, map[string]any) (any, error) {
			return map[string]any{"decision": "allow", "reason": "测试放行"}, nil
		})
	if !strings.Contains(line, `"decision":"allow"`) {
		t.Fatalf("OnReverse 的裁决该透传：%s", line)
	}
	line = answerOnce(t, "interaction/requestPermission", `{}`,
		func(string, map[string]any) (any, error) {
			return nil, io.ErrClosedPipe
		})
	if !strings.Contains(line, `-32601`) {
		t.Fatalf("OnReverse 的错误该变 -32601：%s", line)
	}
}
