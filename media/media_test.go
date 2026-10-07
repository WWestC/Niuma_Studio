package media

// 输入图片仓库的契约：白名单与尺寸帽在 Save 收口；Path/Lookup 是同一
// 次盘上探测的两种投影；id 形状是 GET 面的解析半边。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndLookupRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("body", 64)
	img, err := s.Save([]byte(png), "截图.png", "image/png")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !ValidID(img.ID) {
		t.Fatalf("回执 id 不成形: %q", img.ID)
	}
	if img.Mime != "image/png" || img.Name != "截图.png" || img.Bytes != int64(len(png)) {
		t.Fatalf("回执元数据不对: %+v", img)
	}
	path, mime, size, ok := s.Lookup(img.ID)
	if !ok || mime != "image/png" || size != int64(len(png)) {
		t.Fatalf("lookup 不对: %q %q %d %v", path, mime, size, ok)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != png {
		t.Fatalf("盘上字节与存入不符: %v", err)
	}
	if got := s.Path(img.ID); got != path {
		t.Fatalf("Path 与 Lookup 不同步: %q vs %q", got, path)
	}
}

func TestSaveRefusesNonImage(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Save([]byte("plain text"), "note.txt", "text/plain"); err == nil {
		t.Fatal("text/plain 应被拒")
	}
	if _, err := s.Save([]byte("x"), "blob.bin", "application/octet-stream"); err == nil {
		t.Fatal("不可识别后缀的通用流应被拒")
	}
}

func TestSaveFallsBackToExtension(t *testing.T) {
	s := NewStore(t.TempDir())
	// 粘贴上传常见形态：octet-stream + 认得的后缀 → 按后缀归一
	img, err := s.Save([]byte("jpegbytes"), "pasted.jpg", "application/octet-stream")
	if err != nil {
		t.Fatalf("后缀兜底应放行: %v", err)
	}
	if img.Mime != "image/jpeg" {
		t.Fatalf("后缀兜底应归一 MIME，得到 %q", img.Mime)
	}
	if _, _, _, ok := s.Lookup(img.ID); !ok {
		t.Fatal("兜底存入后应可取回")
	}
}

func TestSaveCapsSize(t *testing.T) {
	s := NewStore(t.TempDir())
	big := make([]byte, MaxBytes+1)
	if _, err := s.Save(big, "huge.png", "image/png"); err == nil {
		t.Fatal("超尺寸帽应被拒")
	}
	if _, err := s.Save(nil, "empty.png", "image/png"); err == nil {
		t.Fatal("空文件应被拒")
	}
}

func TestLookupMissShapes(t *testing.T) {
	s := NewStore(t.TempDir())
	var nilStore *Store
	if p := nilStore.Path("img_000000000000"); p != "" {
		t.Fatal("零值仓库应恒 miss")
	}
	for _, bad := range []string{"", "img_short", "img_0000000000000", "img_zzzzzzzzzzzz", "../../etc/passwd"} {
		if _, _, _, ok := s.Lookup(bad); ok {
			t.Fatalf("坏 id 不应命中: %q", bad)
		}
	}
	// 形状对但盘上没有：miss 而不是报错
	if _, _, _, ok := s.Lookup("img_0123456789ab"); ok {
		t.Fatal("不存在的 id 应 miss")
	}
}

func TestRootCreatesDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "media")
	s := NewStore(root)
	if s.Root() != root {
		t.Fatalf("Root 应回报创建的目录: %q", s.Root())
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("NewStore 应顺手建目录: %v", err)
	}
}
