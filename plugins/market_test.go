package plugins

// market.go 的装货测试：zip 直装、文件夹裹一层下钻、zip-slip 拒收、
// sha256 钉校验。

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func zipBuf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const zipManifest = `{"id":"a.zipper","name":"拉链","version":"0.1.0","contributes":{"boards":[{"id":"z","title":"Z","entry":"web/x.js"}]}}`

func TestInstallFromZip(t *testing.T) {
	s, root, _ := openTempStore(t)
	data := zipBuf(t, map[string]string{
		"plugin.json": zipManifest,
		"web/x.js":    "export function activate(){}",
	})
	id, err := s.InstallFromZip(data, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if id != "a.zipper" {
		t.Fatalf("装出来的 id=%s", id)
	}
	if _, err := os.Stat(filepath.Join(root, "a.zipper", "web", "x.js")); err != nil {
		t.Fatalf("zip 内容没落位：%v", err)
	}
}

func TestInstallFromZipNestedDir(t *testing.T) {
	s, root, _ := openTempStore(t)
	// 手贱用文件夹打包的 zip：一切裹在 a.zipper/ 一层里
	data := zipBuf(t, map[string]string{
		"a.zipper/plugin.json": zipManifest,
		"a.zipper/web/x.js":    "export function activate(){}",
	})
	id, err := s.InstallFromZip(data, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if id != "a.zipper" {
		t.Fatalf("下钻后装出来的 id=%s", id)
	}
	if _, err := os.Stat(filepath.Join(root, "a.zipper", "web", "x.js")); err != nil {
		t.Fatalf("下钻没生效：%v", err)
	}
}

func TestInstallFromZipSlipRejected(t *testing.T) {
	s, _, _ := openTempStore(t)
	data := zipBuf(t, map[string]string{
		"plugin.json":  zipManifest,
		"../escape.js": "gotcha",
	})
	if _, err := s.InstallFromZip(data, "", false); err == nil {
		t.Fatal("zip-slip 该整个包拒收")
	}
}

func TestInstallFromZipShaPin(t *testing.T) {
	s, _, _ := openTempStore(t)
	data := zipBuf(t, map[string]string{"plugin.json": zipManifest})
	if _, err := s.InstallFromZip(data, "deadbeef", false); err == nil {
		t.Fatal("sha 不符该拒装")
	}
	sum := sha256.Sum256(data)
	if _, err := s.InstallFromZip(data, hex.EncodeToString(sum[:]), false); err != nil {
		t.Fatalf("sha 相符该放行：%v", err)
	}
}

// 安全核查修复①：目录条目的 '../' 在任何 mkdir 之前就得拦——旧序
// 先建目录后查路径，目录条目能越根建空目录。
func TestUnzipTreeDirEntrySlipRejected(t *testing.T) {
	s, _, _ := openTempStore(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.CreateHeader(&zip.FileHeader{Name: "../../pwn/", Method: zip.Deflate}); err != nil {
		t.Fatal(err)
	}
	pw, err := zw.Create("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write([]byte(zipManifest)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallFromZip(buf.Bytes(), "", false); err == nil {
		t.Fatal("目录条目 zip-slip 该整个包拒收")
	}
}

// 安全核查修复②：解压上限看实测字节，不看 zip 头的声明——谎报
// UncompressedSize64 的解压炸弹要写满实测上限才被拒。Go 的 zip.Writer
// 不写谎报头（流式输出还带 ZIP64 扩展，事后补丁会破包），这里手工
// 逐字节拼一个"声明 1 字节、实际 200 字节"的最小 zip；上限临时调小
// 到 100B 让测试轻跑。
func lyingZip(t *testing.T, name string, body []byte, lieUncomp uint32) []byte {
	t.Helper()
	var comp bytes.Buffer
	fw, err := flate.NewWriter(&comp, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	nameB := []byte(name)
	crc := crc32.ChecksumIEEE(body)
	le := binary.LittleEndian
	u16 := func(v uint16) []byte { b := make([]byte, 2); le.PutUint16(b, v); return b }
	u32 := func(v uint32) []byte { b := make([]byte, 4); le.PutUint32(b, v); return b }

	var out bytes.Buffer
	localOff := out.Len()
	out.Write([]byte("PK\x03\x04"))              // 本地文件头
	for _, v := range []uint16{20, 0, 8, 0, 0} { // ver/flags/deflate/time/date
		out.Write(u16(v))
	}
	out.Write(u32(crc))
	out.Write(u32(uint32(comp.Len())))
	out.Write(u32(lieUncomp)) // 本地头跟着一起撒谎更逼真
	for _, v := range []uint16{uint16(len(nameB)), 0} {
		out.Write(u16(v))
	}
	out.Write(nameB)
	out.Write(comp.Bytes())

	cdOff := out.Len()
	out.Write([]byte("PK\x01\x02"))                  // 中心目录
	for _, v := range []uint16{20, 20, 0, 8, 0, 0} { // made/needed/flags/method/time/date
		out.Write(u16(v))
	}
	out.Write(u32(crc))
	out.Write(u32(uint32(comp.Len())))
	out.Write(u32(lieUncomp))                                    // 谎报的声明大小——Reader 的真相源
	for _, v := range []uint16{uint16(len(nameB)), 0, 0, 0, 0} { // name/extra/comment/disk/iattr
		out.Write(u16(v))
	}
	out.Write(u32(0)) // eattr
	out.Write(u32(uint32(localOff)))
	out.Write(nameB)

	cdSize := out.Len() - cdOff              // EOCD 自身的字节不算进目录大小
	out.Write([]byte("PK\x05\x06"))          // EOCD
	for _, v := range []uint16{0, 0, 1, 1} { // disk/cd-disk/entries-here/entries-total
		out.Write(u16(v))
	}
	out.Write(u32(uint32(cdSize)))
	out.Write(u32(uint32(cdOff)))
	out.Write(u16(0))
	return out.Bytes()
}

func TestUnzipTreeLyingHeaderCapped(t *testing.T) {
	s, _, _ := openTempStore(t)
	body := bytes.Repeat([]byte("A"), 200)

	// 先钉构造本身：真话 zip（声明=实际=200）能解包落盘——防止后面的
	// 拒收是"包本来就坏"的空赢。
	truthful := lyingZip(t, "bomb.bin", body, 200)
	dst := t.TempDir()
	if err := unzipTree(truthful, dst); err != nil {
		t.Fatalf("真话包该解包成功（构造自检）：%v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "bomb.bin")); err != nil || len(b) != 200 {
		t.Fatalf("真话包内容走样：%v %d", err, len(b))
	}

	// 谎报包（声明 1、实际 200）在 100B 上限下装不成、超限字节不上盘。
	// 拒收可能来自两道轨之一：Go 1.27 的 checksumReader 在读时强制声明
	// 大小（超读即 ErrFormat），或本包自己的实测字节上限——安全属性是
	// "装不成且写盘不超限"，不锁死具体哪道轨先响。
	oldPer, oldTotal := unzipPerFileCap, unzipTotalCap
	unzipPerFileCap, unzipTotalCap = 100, 200
	t.Cleanup(func() { unzipPerFileCap, unzipTotalCap = oldPer, oldTotal })
	data := lyingZip(t, "bomb.bin", body, 1)
	if _, err := s.InstallFromZip(data, "", false); err == nil {
		t.Fatal("谎报头的超限流该被拒")
	}
	dst2 := t.TempDir()
	_ = unzipTree(data, dst2) // 错误是预期；只查写盘
	if fi, err := os.Stat(filepath.Join(dst2, "bomb.bin")); err == nil && fi.Size() > 100 {
		t.Fatalf("谎报包把超限字节写上了盘：%d 字节", fi.Size())
	}
}

// 安全核查修复③：明文 http 源仅限回环字面量——https 到哪都行。
func TestFetchSchemeGate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pkg"))
	}))
	t.Cleanup(ts.Close)
	if _, err := FetchZip(context.Background(), ts.URL+"/pkg.zip"); err != nil {
		t.Fatalf("回环明文源该放行（本地测试 registry 的形状）：%v", err)
	}
	if _, err := FetchZip(context.Background(), "http://example.com/pkg.zip"); err == nil {
		t.Fatal("非回环明文源该拒")
	}
	if _, err := FetchMarketIndex(context.Background(), "http://example.com/index.json"); err == nil {
		t.Fatal("非回环明文索引源该拒")
	}
	if _, err := FetchZip(context.Background(), "ftp://example.com/pkg.zip"); err == nil {
		t.Fatal("非 http(s) 方案该拒")
	}
}
