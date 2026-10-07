package wire

// contract_gen_test.go — the freshness pins of the generated mirror
// artifacts. Since wiregen's lib-ification (tools/wiregen/gen), these
// tests import the SAME extraction/emission code the CLI runs — the
// old inlined "structurally identical twin extractor" is dead, and
// with it the twin-drift failure class: there is one implementation,
// and the tests execute it. Pinned fresh:
//   - web/wire/gen/frame-contract.json  (frames + typed shapes + envelope)
//   - web/wire/gen/types.js             (JSDoc full mirrors)
//   - <domain>/wireconv_gen.go          (five domain packages)
// Change a constant, a payload shape, or the envelope without
// re-running `go run ./tools/wiregen` (and committing the outputs),
// and these fail with the regeneration command spelled out.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/WWestC/Niuma_Studio/tools/wiregen/gen"
)

// repoRoot is the module root seen from the wire package's test dir.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func skipIfWiregenDisabled(t *testing.T) {
	t.Helper()
	if os.Getenv("NIUMA_SKIP_WIREGEN") != "" {
		t.Skip("NIUMA_SKIP_WIREGEN set")
	}
}

func TestFrameContractIsFresh(t *testing.T) {
	skipIfWiregenDisabled(t)
	c, err := gen.BuildContract(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := gen.RenderContractJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../web/wire/gen/frame-contract.json")
	if err != nil {
		t.Fatalf("读取已提交契约失败：%v —— 请运行 `go run ./tools/wiregen` 生成并提交", err)
	}
	if !bytes.Equal(bytes.TrimSpace(committed), bytes.TrimSpace(b)) {
		t.Fatalf("frame-contract.json 与 wire 包现状不一致——运行 `go run ./tools/wiregen` 并连同前端镜像一起提交")
	}
}

func TestWireconvGenFilesAreFresh(t *testing.T) {
	skipIfWiregenDisabled(t)
	want, err := gen.GenWireconv(repoRoot(t))
	if err != nil {
		t.Fatalf("生成期失败（镜像谎言或形状无法对齐）：%v", err)
	}
	for rel, content := range want {
		committed, err := os.ReadFile(filepath.Join("..", rel))
		if err != nil {
			t.Fatalf("%s 缺失：%v —— 运行 `go run ./tools/wiregen`", rel, err)
		}
		if !bytes.Equal(committed, content) {
			t.Fatalf("%s 与两侧 struct 现状不一致——运行 `go run ./tools/wiregen` 并提交生成物", rel)
		}
	}
	// 陈旧残留：目录里存在不在生成清单里的 wireconv_gen.go（改名/退役后没删）。
	for _, dom := range []string{"tasks", "plan", "merge", "requirements", "meeting"} {
		_, err := os.Stat(filepath.Join("..", dom, "wireconv_gen.go"))
		if _, want := want[filepath.Join(dom, "wireconv_gen.go")]; want == (err == nil) {
			continue
		}
		t.Fatalf("%s/wireconv_gen.go 在磁盘与生成清单上不一致——运行 `go run ./tools/wiregen`", dom)
	}
}

func TestTypesJsIsFresh(t *testing.T) {
	skipIfWiregenDisabled(t)
	c, err := gen.BuildContract(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	want := gen.GenTypedefs(c)
	committed, err := os.ReadFile("../web/wire/gen/types.js")
	if err != nil {
		t.Fatalf("web/wire/gen/types.js 缺失：%v —— 运行 `go run ./tools/wiregen`", err)
	}
	if !bytes.Equal(committed, want) {
		t.Fatalf("web/wire/gen/types.js 与契约现状不一致——运行 `go run ./tools/wiregen` 并提交")
	}
}
