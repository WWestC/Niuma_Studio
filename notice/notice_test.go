package notice_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/notice"
	"github.com/WWestC/Niuma_Studio/storetest"
)

func TestSetGetClearRoundTrip(t *testing.T) {
	open, _ := storetest.NoticeSQLiteBacking(t)
	s := open("")
	if _, ok := s.Get("default"); ok {
		t.Fatal("空库读到公告")
	}
	n, err := s.SetExpect("default", "下周冲刺：全员 996", "老板", -1)
	if err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if n.Rev != 1 {
		t.Fatalf("首发 rev 应为 1，得到 %d", n.Rev)
	}
	got, ok := s.Get("default")
	if !ok || got.Content != "下周冲刺：全员 996" || got.By != "老板" || got.Rev != 1 {
		t.Fatalf("回读不符：%+v ok=%v", got, ok)
	}
	n2, err := s.SetExpect("default", "改：下周冲刺取消", "老板", 1)
	if err != nil {
		t.Fatalf("条件更新失败：%v", err)
	}
	if n2.Rev != 2 {
		t.Fatalf("更新 rev 应为 2，得到 %d", n2.Rev)
	}
	if err := s.Clear("default", "老板", 2); err != nil {
		t.Fatalf("撤下失败：%v", err)
	}
	if _, ok := s.Get("default"); ok {
		t.Fatal("撤下后仍读到公告")
	}
}

func TestExpectRevConflicts(t *testing.T) {
	open, _ := storetest.NoticeSQLiteBacking(t)
	s := open("")
	_, err := s.SetExpect("proj-x", "初版", "老板", 3)
	if err == nil {
		t.Fatal("不存在却期望 rev 3，应拒绝")
	}
	var rc *notice.RevConflictError
	if !errors.As(err, &rc) || rc.CurrentRev != 0 {
		t.Fatalf("应回 *notice.RevConflictError 且 CurrentRev=0：%v", err)
	}
	if _, err := s.SetExpect("proj-x", "初版", "老板", -1); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if _, err := s.SetExpect("proj-x", "重复发布", "老板", 0); err == nil {
		t.Fatal("已存在却期望 rev 0（必须不存在），应拒绝")
	}
	// 期望当前 rev 的条件更新合法（rev 1 → 2）
	if _, err := s.SetExpect("proj-x", "第二版", "老板", 1); err != nil {
		t.Fatalf("期望当前 rev 的更新应成功：%v", err)
	}
	if _, err := s.SetExpect("proj-x", "过期版本", "老板", 1); err == nil {
		t.Fatal("期望过期 rev 1（当前已是 2），应拒绝")
	}
	rc = nil
	_, err = s.SetExpect("proj-x", "过期版本", "老板", 1)
	if !errors.As(err, &rc) || rc.CurrentRev != 2 || rc.UpdatedBy != "老板" {
		t.Fatalf("冲突错误应带当前 rev 与发布人：%v", err)
	}
	if err := s.Clear("proj-x", "老板", 1); err == nil {
		t.Fatal("撤下带过期 rev 应拒绝")
	}
	// 拒绝路径不得改动盘面
	got, _ := s.Get("proj-x")
	if got.Rev != 2 || got.Content != "第二版" {
		t.Fatalf("被拒后公告被改动：%+v", got)
	}
}

func TestContentValidation(t *testing.T) {
	open, _ := storetest.NoticeSQLiteBacking(t)
	s := open("")
	if _, err := s.SetExpect("default", "  \n ", "老板", -1); err == nil {
		t.Fatal("空白公告应拒绝")
	}
	long := make([]rune, notice.MaxContentRunes+1)
	for i := range long {
		long[i] = '字'
	}
	if _, err := s.SetExpect("default", string(long), "老板", -1); err == nil {
		t.Fatal("超长公告应拒绝")
	}
	if _, err := s.SetExpect("Bad Key!", "x", "老板", -1); err == nil {
		t.Fatal("非法 key 应拒绝")
	}
}

func TestClearAbsentIsNoop(t *testing.T) {
	open, _ := storetest.NoticeSQLiteBacking(t)
	s := open("")
	if err := s.Clear("default", "老板", -1); err != nil {
		t.Fatalf("撤下不存在的公告应为幂等成功：%v", err)
	}
}

func TestPersistenceAcrossStores(t *testing.T) {
	open, reopenB := storetest.NoticeSQLiteBacking(t)
	s1 := open("")
	if _, err := s1.SetExpect("proj-y", "落盘检查", "老板", -1); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	s2 := reopenB("")(t)
	got, ok := s2.Get("proj-y")
	if !ok || got.Content != "落盘检查" || got.Rev != 1 {
		t.Fatalf("重开库应读到已落盘公告：%+v ok=%v", got, ok)
	}
}

func TestInMemoryRoot(t *testing.T) {
	s := notice.NewMemory()
	if _, err := s.SetExpect("default", "内存模式", "老板", -1); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if _, ok := s.Get("default"); !ok {
		t.Fatal("内存模式读不到公告")
	}
	if err := s.Clear("default", "老板", 1); err != nil {
		t.Fatalf("撤下失败：%v", err)
	}
}

func TestImportLegacyBlackboard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	t.Setenv("USERPROFILE", home) // windows UserHomeDir
	legacy := filepath.Join(home, ".niuma_blackboard.md")
	if err := os.WriteFile(legacy, []byte("旧黑板时代的公告\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	open, _ := storetest.NoticeSQLiteBacking(t)
	s := open("")
	if !s.ImportLegacyBlackboard("老板") {
		t.Fatal("首次导入应成功")
	}
	got, ok := s.Get(notice.LobbyKey)
	if !ok || got.Content != "旧黑板时代的公告" {
		t.Fatalf("导入后 Niuma_Studio 公告不符：%+v ok=%v", got, ok)
	}
	// 旧文件保留取证
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("旧黑板文件应保留：%v", err)
	}
	// 大厅已有公告时不覆盖；旧文件仍在也不再导入
	if err := os.WriteFile(legacy, []byte("又改了一版"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.ImportLegacyBlackboard("老板") {
		t.Fatal("Niuma_Studio 已有公告时不应再导入")
	}
	got, _ = s.Get(notice.LobbyKey)
	if got.Content != "旧黑板时代的公告" {
		t.Fatalf("已有公告被覆盖：%+v", got)
	}
}
