package chat

// registry_purge_test.go — Registry.PurgeFiles（项目删除面的房间档案
// 清除）的契约：非在册 key 的全套持久文件（history jsonl ＋三台账
// 兄弟文件＋座位快照＋终端目录）一并删除；仍在册（已实例化）的房间
// 拒绝——活房间的清除走重置面；未落盘的文件按不存在静默放过（draft
// 从未开张也要能删）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

func purgeRegistry(t *testing.T, root string) *Registry {
	t.Helper()
	reg := NewRegistry(nil, root, "", "")
	return reg
}

func TestPurgeFilesWipesDurableInventory(t *testing.T) {
	root := t.TempDir()
	reg := purgeRegistry(t, root)
	key := "gone"
	// 伪造一个已归档房间留下的全套档案（不经 Hub：删除面面对的正是
	// 非实例化状态）；台账文件名走 SetHistoryPath 的派生约定
	// （TrimSuffix(".jsonl") + ".reads.json" …）
	history := filepath.Join(root, "history", key+".jsonl")
	if err := os.WriteFile(history, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "history", key+".reads.json"),
		filepath.Join(root, "history", key+".acks.json"),
		filepath.Join(root, "history", key+".reacts.json"),
	} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "room", key+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "terminal", key, "小明"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := reg.PurgeFiles(key); err != nil {
		t.Fatalf("PurgeFiles: %v", err)
	}
	for _, p := range []string{
		history,
		filepath.Join(root, "history", key+".reads.json"),
		filepath.Join(root, "history", key+".acks.json"),
		filepath.Join(root, "history", key+".reacts.json"),
		filepath.Join(root, "room", key+".json"),
		filepath.Join(root, "terminal", key),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s 删除后应消失（stat err=%v）", p, err)
		}
	}
}

func TestPurgeFilesSilentOnNeverOpened(t *testing.T) {
	reg := purgeRegistry(t, t.TempDir())
	if err := reg.PurgeFiles("never-opened"); err != nil {
		t.Fatalf("从未开张的项目（无任何文件）应静默成功，得 %v", err)
	}
}

func TestPurgeFilesRefusesLiveRoom(t *testing.T) {
	dir := t.TempDir()
	store, err := projects.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(projects.Project{Key: "alpha", Name: "甲", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate("alpha"); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(store, filepath.Join(dir, "root"), "房主", "老板")
	if _, err := reg.Hub("alpha"); err != nil { // active 项目 → 实例化
		t.Fatalf("前置失败：Hub(alpha): %v", err)
	}
	if err := reg.PurgeFiles("alpha"); err == nil {
		t.Fatal("仍在册的活房间应拒绝清文件")
	}
}
