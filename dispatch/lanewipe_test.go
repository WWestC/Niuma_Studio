package dispatch

// lanewipe_test.go — WipeProjectLanes 的文件面契约：只清指名项目的
// 分键；同文件别项目的车道原样；清空即删文件；损坏文件跳过不动；
// 无 inbox 目录安静空手而归；幂等（再清一次 0 份）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeInboxFile(t *testing.T, dir, name string, projects map[string]inboxLanes) {
	t.Helper()
	b, err := json.Marshal(projects)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func readInboxFile(t *testing.T, dir, name string) map[string]inboxLanes {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var file map[string]inboxLanes
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return file
}

func TestWipeProjectLanes(t *testing.T) {
	dir := t.TempDir()
	lanes := inboxLanes{Queue: []string{"未达旧消息"}}
	// 小明两个项目都有车道；小红只有被清的项目；小刚只在别的项目。
	writeInboxFile(t, dir, "小明", map[string]inboxLanes{"proj-a": lanes, "proj-b": lanes})
	writeInboxFile(t, dir, "小红", map[string]inboxLanes{"proj-a": lanes})
	writeInboxFile(t, dir, "小刚", map[string]inboxLanes{"proj-c": lanes})
	if err := os.WriteFile(filepath.Join(dir, "损坏.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write 损坏: %v", err)
	}

	n, err := WipeProjectLanes(dir, "proj-a")
	if err != nil {
		t.Fatalf("WipeProjectLanes: %v", err)
	}
	if n != 2 {
		t.Fatalf("应触及小明/小红两份文件，得 %d", n)
	}
	// 小明的 proj-a 分键消失、proj-b 车道内容原样
	file := readInboxFile(t, dir, "小明")
	if len(file) != 1 {
		t.Fatalf("小明应只剩 proj-b 一个分键，得 %v", file)
	}
	if kept, ok := file["proj-b"]; !ok || len(kept.Queue) != 1 || kept.Queue[0] != "未达旧消息" {
		t.Fatalf("proj-b 的车道内容不许动，得 %+v", kept)
	}
	// 小红清空即删文件
	if _, err := os.Stat(filepath.Join(dir, "小红.json")); !os.IsNotExist(err) {
		t.Fatal("清空的车道文件应删除")
	}
	// 小刚与损坏文件不许被牵连
	if file := readInboxFile(t, dir, "小刚"); len(file) != 1 {
		t.Fatalf("小刚不许动，得 %v", file)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "损坏.json")); err != nil || string(b) != "not json" {
		t.Fatal("损坏文件应跳过不动")
	}

	// 幂等：再清一次 0 份无错
	if n, err := WipeProjectLanes(dir, "proj-a"); err != nil || n != 0 {
		t.Fatalf("幂等再清应 0 份无错，得 %d %v", n, err)
	}
	// 无 inbox 目录：安静空手而归
	if n, err := WipeProjectLanes(filepath.Join(dir, "不存在的目录"), "proj-a"); err != nil || n != 0 {
		t.Fatalf("缺目录应 0 份无错，得 %d %v", n, err)
	}
}
