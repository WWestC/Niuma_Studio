package recruit

// RecallMember pulls an existing member back online (v0.10 seat-M4,
// t_85): the saved prompt becomes a fresh GUI session via the drive
// engine, then the seat is confirmed by polling /members. Shared by
// the CLI (recall --name) and the room's watch keeper (auto-fill) —
// the manual pull and the automatic refill ride the exact same path.
//
// The red line holds: no headless spawns, ever — recall is the
// re-run of the hiring pipeline for a member that already exists.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// RecallMember runs the pull-back for one member: config check →
// prompt fetch → drive injection → seat confirmation.
func RecallMember(port int, name string) error {
	// 1. the member must have a saved config — active or archived
	// alike (recalling an archived member is a rehire, seat-M1's
	// upsert-clears-archive makes it whole).
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/agents/%s/prompt", port, name))
	if err != nil {
		return fmt.Errorf("办公室不可达: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s 未建档（无保存的接入配置）——先 recruit --name %s 建档", name, name)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /agents/%s/prompt: status %d", name, resp.StatusCode)
	}
	b := make([]byte, 0, 8192)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			break
		}
		if len(b) > 64*1024 {
			return fmt.Errorf("提示词超长（>64KB），疑似配置损坏")
		}
	}

	// 2. the brief rides a temp file (first line = the title/verify
	// key, same contract as recruit). The name folds through
	// SafeTempLeaf（安全核查修复：名字进路径先消毒）; the file is
	// removed when this function returns — the failure fallback fetches
	// fresh content over curl, so nothing needs it to linger on disk.
	briefPath := filepath.Join(os.TempDir(), SafeTempLeaf("dh_recall_", name, ".txt"))
	if err := os.WriteFile(briefPath, b, 0o600); err != nil {
		return fmt.Errorf("写提示词文件: %w", err)
	}
	defer func() { _ = os.Remove(briefPath) }()

	// 3. GUI injection through the drive engine (ARB-2 exit codes).
	if rc := RunDrive([]string{"--brief", briefPath, "--attempts", "3"}); rc != 0 {
		return fmt.Errorf("会话注入失败（rc=%d；无 ZCode 窗口/无辅助功能权限时 drive 不可用，可手动开位：curl -s http://127.0.0.1:%d/agents/%s/prompt 粘贴到新会话）", rc, port, name)
	}

	// 4. seat confirmation: poll /members until the name holds a seat.
	deadline := time.Now().Add(90 * time.Second)
	for {
		ok, err := memberOnline(port, name)
		if err != nil {
			return fmt.Errorf("在座确认失败: %w", err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s 90 秒内未在座（会话可能未跑 guard）", name)
		}
		time.Sleep(2 * time.Second)
	}
}

// memberOnline reports whether name currently holds a seat (live or
// grace — the roster view).
func memberOnline(port int, name string) (bool, error) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/members", port))
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var members []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		return false, err
	}
	for _, m := range members {
		if m.Name == name {
			return true, nil
		}
	}
	return false, nil
}
