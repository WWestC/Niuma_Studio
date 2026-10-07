package cli

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestRoomCommandsRefuseWithoutName pins the 「3StripFishde」 ghost-seat
// fix: every command that dials the room must refuse — exit 2, before
// any port discovery, probe or dial — when --name is missing. The old
// hostname-derived default joined as a stranger and left a grace ghost
// in the roster (documented rule: 凡 CLI 操作必带 --name). No server is
// running in these tests: reaching the refusal (and not a discovery
// error, exit 1) proves the guard fires first, with zero side effects.
func TestRoomCommandsRefuseWithoutName(t *testing.T) {
	cases := []struct {
		name string
		run  func() int
	}{
		{"say", func() int { return runSay([]string{"hello"}) }},
		{"report", func() int { return runReport([]string{"progress"}) }},
		{"listen", func() int { return runListen(nil) }},
		{"wait", func() int { return runWait(nil) }},
		{"task create", func() int {
			return runTaskCreate([]string{"--title", "重建索引"})
		}},
		{"task update", func() int {
			return runTaskUpdate([]string{"t_01", "--status", "doing"})
		}},
		{"task confirm", func() int {
			return runTaskConfirmDecline("task_confirm", []string{"t_01"})
		}},
		{"kb write", func() int {
			return runKbWrite([]string{"roles/hr", "--body", "手册"})
		}},
		{"kb append", func() int {
			return runKbAppend([]string{"ops/room-log", "小明 已上岗"})
		}},
		{"kb restore", func() int {
			return runKbRestore([]string{"roles/hr", "--rev", "3"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := tc.run()
			if rc != 2 {
				t.Errorf("%s without --name: rc = %d, want 2 (refusal before any dial)", tc.name, rc)
			}
		})
	}
}

// TestFailNoNameMessage keeps the refusal actionable: it must name the
// missing flag and state that nothing was dialed.
func TestFailNoNameMessage(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	rc := runSay([]string{"hello"})
	os.Stderr = old
	_ = w.Close()
	var buf strings.Builder
	b, _ := io.ReadAll(r)
	_, _ = buf.Write(b)
	msg := buf.String()
	if rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	if !strings.Contains(msg, "--name") || !strings.Contains(msg, "未拨号") {
		t.Errorf("refusal message not actionable: %q", msg)
	}
}
