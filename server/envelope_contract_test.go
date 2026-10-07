package server

// envelope_contract_test.go — the seated face's envelope pin (two
// faces): ① freshness — the committed frame contract's envelope
// section must be exactly the verbs package's Envelope (change the
// struct without `go run ./tools/wiregen` and this goes red, the same
// discipline wire's contract_gen_test.go holds over the wire package);
// ② vocabulary — every envelope slot must exist in the wire frame
// vocabulary (wire.Message's json tags, plus the documented
// inbound-only exceptions below). A typo'd json tag decodes a frame
// field into nothing and the verb silently no-ops — that drift class
// is a named test failure here, not an eyeball tax.

import (
	"encoding/json"
	"github.com/WWestC/Niuma_Studio/server/verbs"
	"os"
	"reflect"
	"strings"
	"testing"
)

type envelopeField struct {
	Field string `json:"field"`
	Tag   string `json:"tag"`
}

type envelopeContract struct {
	Types    map[string][]envelopeField `json:"types"`
	Envelope []envelopeField            `json:"envelope"`
}

func loadContract(t *testing.T) envelopeContract {
	t.Helper()
	b, err := os.ReadFile("../web/wire/gen/frame-contract.json")
	if err != nil {
		t.Fatalf("读取已提交契约失败：%v —— 请运行 `go run ./tools/wiregen` 生成并提交", err)
	}
	var c envelopeContract
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("契约不是合法 JSON：%v", err)
	}
	return c
}

func TestEnvelopeContractIsFresh(t *testing.T) {
	if os.Getenv("NIUMA_SKIP_WIREGEN") != "" {
		t.Skip("NIUMA_SKIP_WIREGEN set")
	}
	c := loadContract(t)
	if len(c.Envelope) == 0 {
		t.Fatal("契约缺 envelope 段——运行 `go run ./tools/wiregen` 重生并提交")
	}
	// 反射按声明序给字段与 tag——与生成器的 AST 提取同序可比。
	rt := reflect.TypeOf(verbs.Envelope{})
	got := make([]envelopeField, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		got = append(got, envelopeField{Field: f.Name, Tag: f.Tag.Get("json")})
	}
	if !reflect.DeepEqual(got, c.Envelope) {
		t.Fatalf("frame-contract.json 的 envelope 段与 verbs.Envelope 现状不一致——运行 `go run ./tools/wiregen` 并连同前端镜像一起提交")
	}
}

func TestEnvelopeSlotsLiveInWireVocabulary(t *testing.T) {
	c := loadContract(t)
	// 入站独占槽：wire.Message 没有对应字段的信封 tag。每条都必须带
	// 出处理由——rank 是 rank_set 的载荷，该动词族（kick/rank_set/
	// agent_archive）的帧常量住在 server 包（pre-wire 历史，contract.
	// test.js 头注释同款记载），出站走 system 回执，故协议帧结构里
	// 无此字段；wire.js 的 Frame 文档仍盖住它（JS 侧镜像测试钉）。
	inboundOnly := map[string]string{
		"rank": "rank_set 载荷（动词常量在 server 包的 pre-wire 历史）",
	}
	msgTags := map[string]bool{}
	for _, f := range c.Types["Message"] {
		msgTags[strings.SplitN(f.Tag, ",", 2)[0]] = true
	}
	var drift []string
	for _, f := range c.Envelope {
		tag := strings.SplitN(f.Tag, ",", 2)[0]
		if msgTags[tag] {
			continue
		}
		if _, ok := inboundOnly[tag]; ok {
			continue
		}
		drift = append(drift, f.Field+"（json:"+tag+"）")
	}
	if len(drift) > 0 {
		t.Fatalf("信封槽不在 wire.Message 词表内（帧照收、字段解码成零值、动词静默 no-op 的漂移面）：%s\n——要么改信封 tag 对齐 wire.Message，要么 wire 补字段，要么进 inboundOnly 白名单并写明理由", strings.Join(drift, "、"))
	}
}
