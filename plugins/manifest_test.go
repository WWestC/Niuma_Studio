package plugins

// manifest.go 的校验面测试：id/engine/板块元数据/资产相对路径的正反
// 用例。problems 是「带病记录」不是拒收——除非测试钉的就是拒收语义。

import (
	"encoding/json"
	"strings"
	"testing"
)

func man(t *testing.T, src string) (*Manifest, []string) {
	t.Helper()
	m, problems, err := ParseManifest([]byte(src))
	if err != nil {
		t.Fatalf("ParseManifest 硬错（本组用例都该能解析）：%v", err)
	}
	return m, problems
}

func hasProblem(problems []string, part string) bool {
	for _, p := range problems {
		if strings.Contains(p, part) {
			return true
		}
	}
	return false
}

func TestParseManifestOK(t *testing.T) {
	m, problems := man(t, `{
		"id": "wwestc.chess", "name": "午休棋盘", "version": "0.1.0",
		"engine": ">=1.0",
		"permissions": ["boards", "chat.read"],
		"contributes": { "boards": [ {"id": "chess", "title": "棋盘", "entry": "web/main.js"} ] }
	}`)
	if len(problems) != 0 {
		t.Fatalf("健康 manifest 不该带问题：%v", problems)
	}
	if m.ID != "wwestc.chess" || len(m.Contributes.Boards) != 1 || m.Contributes.Boards[0].Entry != "web/main.js" {
		t.Fatalf("字段解析走样：%+v", m)
	}
	if !ValidID(m.ID) {
		t.Fatal("ValidID 不认健康 id")
	}
}

func TestParseManifestBadID(t *testing.T) {
	_, problems := man(t, `{"id": "Upper.Case", "name": "x", "version": "0.1.0"}`)
	if !hasProblem(problems, "id") {
		t.Fatalf("大写 id 该被点名，得到 %v", problems)
	}
	if ValidID("no-dot") || ValidID("") || ValidID("../evil") {
		t.Fatal("ValidID 放进了坏 id")
	}
}

func TestParseManifestEngineFloor(t *testing.T) {
	_, problems := man(t, `{"id": "a.b", "name": "x", "version": "0.1.0", "engine": ">=9.9"}`)
	if !hasProblem(problems, "升级") {
		t.Fatalf("未来 engine 该被点名，得到 %v", problems)
	}
	// 同代与留空都放行
	for _, e := range []string{"", "1.0", ">=1.0", "^1.0"} {
		if ok, p := engineOK(e); !ok {
			t.Fatalf("engine %q 该放行，却被拒：%s", e, p)
		}
	}
	if ok, _ := engineOK(">=1.1"); ok {
		t.Fatal("engine >=1.1 该被本宿主（1.0）拒绝")
	}
	if ok, _ := engineOK("banana"); ok {
		t.Fatal("烂 engine 该被拒绝")
	}
}

func TestParseManifestBoardValidation(t *testing.T) {
	_, problems := man(t, `{
		"id": "a.b", "name": "x", "version": "0.1.0",
		"contributes": { "boards": [
			{"id": "chat", "title": "撞内置", "entry": "web/x.js"},
			{"id": "Fine-ID", "title": "大写", "entry": "web/x.js"},
			{"id": "ok1", "title": "", "entry": "web/x.js"},
			{"id": "ok2", "title": "逃逸", "entry": "../etc/passwd"},
			{"id": "ok3", "title": "绝对", "entry": "/abs/main.js"}
		] }
	}`)
	for _, want := range []string{"冲突", "不合法", "title 缺失", "..", "相对路径"} {
		if !hasProblem(problems, want) {
			t.Fatalf("缺一条 %q 的问题，得到 %v", want, problems)
		}
	}
}

func TestParseManifestUnknownPermissionIsRecordedNotFatal(t *testing.T) {
	_, problems := man(t, `{"id": "a.b", "name": "x", "version": "0.1.0", "permissions": ["teleport"]}`)
	if !hasProblem(problems, "未知权限") {
		t.Fatalf("未知权限该被记录，得到 %v", problems)
	}
	if len(problems) != 1 {
		t.Fatalf("未知权限只该记一条，得到 %v", problems)
	}
}

func TestParseManifestBadJSONIsHardError(t *testing.T) {
	if _, _, err := ParseManifest([]byte(`{"id":`)); err == nil {
		t.Fatal("烂 JSON 该硬错")
	}
}

// 顺手钉住 JSON 形状：manifest 的字段名就是发布面契约。
func TestManifestJSONShape(t *testing.T) {
	m, _ := man(t, `{"id":"a.b","name":"n","version":"1.2.3","contributes":{"wardrobe":["w.json"],"boards":[{"id":"z","title":"t","entry":"e.js","icon":"i.png"}]}}`)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"a.b"`, `"wardrobe":["w.json"]`, `"entry":"e.js"`, `"icon":"i.png"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("序列化丢了 %s：%s", want, b)
		}
	}
}

func TestParseManifestThemes(t *testing.T) {
	_, problems := man(t, `{
		"id": "a.b", "name": "x", "version": "0.1.0",
		"contributes": { "themes": [
			{ "id": "neon", "name": "霓虹", "vars": { "--gold": "#00e5ff", "--board": "rgba(21,27,34,1)", "--line-hi": "transparent", "--bubble-me": "rgba(0,229,255,.12)" } },
			{ "id": "neon", "name": "重复id", "vars": { "--gold": "#fff" } },
			{ "id": "bad-slot", "name": "坏槽", "vars": { "--position": "fixed" } },
			{ "id": "bad-color", "name": "坏色", "vars": { "--gold": "url(javascript:alert(1))" } },
			{ "id": "bad-color2", "name": "坏色2", "vars": { "--board": "#12" } },
			{ "id": "empty-vars", "name": "空槽", "vars": {} }
		] }
	}`)
	for _, want := range []string{"重复", "未知槽位", "不是颜色"} {
		if !hasProblem(problems, want) {
			t.Fatalf("缺一条 %q 的问题，得到 %v", want, problems)
		}
	}
	// 坏条目不连坐：健康主题（第一条）不带问题——问题条数恰为坏条数
	if len(problems) != 5 {
		t.Fatalf("问题该恰好 5 条（重复id/坏槽/坏色/坏色2/空槽），得到 %d：%v", len(problems), problems)
	}
}
