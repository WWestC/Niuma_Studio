package server

// lang_test.go — the /lang face's behavior: GET paints the process
// mirror, POST persists + swaps it in one breath, junk payloads are
// rejected with 400, and no registry root 404s the whole face — the
// same discipline as /reveal. i18n's mirror is a package global, so
// every test resets it on both ends (start and cleanup) to keep the
// suite order-free.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/i18n"
)

func newLangStage(t *testing.T) *Server {
	t.Helper()
	i18n.SetLang("zh")
	t.Cleanup(func() { i18n.SetLang("zh") })
	return &Server{opts: Options{Registry: chat.NewRegistry(nil, t.TempDir(), "房主", "staff")}}
}

func (s *Server) callLang(t *testing.T, method, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/lang", nil)
	} else {
		req = httptest.NewRequest(method, "/lang", strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	s.handleLang(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// GET 画进程镜像（默认 zh）；POST en 落盘＋热切一口气，GET 随后画 en，
// S() 出词典；POST zh 切回。
func TestLangRoundtrip(t *testing.T) {
	s := newLangStage(t)
	path := filepath.Join(s.opts.Registry.Root(), "lang.json")

	code, out := s.callLang(t, http.MethodGet, "")
	if code != http.StatusOK || out["lang"] != "zh" {
		t.Fatalf("GET 默认 = %d %v, want 200 zh", code, out)
	}

	if code, out = s.callLang(t, http.MethodPost, `{"lang":"en"}`); code != http.StatusOK || out["lang"] != "en" {
		t.Fatalf("POST en = %d %v", code, out)
	}
	if got := i18n.LoadLang(path); got != "en" {
		t.Fatalf("落盘 = %q, want en", got)
	}
	if i18n.Lang() != "en" {
		t.Fatalf("进程镜像 = %q, want en（下一条 S/Sf 已按新语言出）", i18n.Lang())
	}
	if got := i18n.S("载荷缺失"); got != "payload missing" {
		t.Fatalf("热切后 S() = %q, want 词典值", got)
	}
	if code, out = s.callLang(t, http.MethodGet, ""); code != 200 || out["lang"] != "en" {
		t.Fatalf("GET after en = %d %v", code, out)
	}

	if _, out = s.callLang(t, http.MethodPost, `{"lang":"zh"}`); out["lang"] != "zh" {
		t.Fatalf("POST zh 回执 = %v", out)
	}
	if i18n.Lang() != "zh" || i18n.LoadLang(path) != "zh" {
		t.Fatalf("切回 zh 未生效：Lang=%q 盘=%q", i18n.Lang(), i18n.LoadLang(path))
	}
}

// 载荷必须带 zh|en：缺失（null）、非法值与坏 JSON 都是 400，镜像不动。
func TestLangPostValidation(t *testing.T) {
	s := newLangStage(t)
	for _, body := range []string{`{}`, `{"lang":null}`, `{"lang":"fr"}`, `{bad`, ``} {
		if code, _ := s.callLang(t, http.MethodPost, body); code != http.StatusBadRequest {
			t.Errorf("POST %q = %d, want 400", body, code)
		}
	}
	if i18n.Lang() != "zh" {
		t.Fatalf("被拒的载荷不该换语言：Lang = %q", i18n.Lang())
	}
}

// 无注册根（内嵌/测试形态）整面 404——/retention 的同一纪律。
func TestLangNoRoot(t *testing.T) {
	s := &Server{}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/lang", strings.NewReader(`{"lang":"en"}`))
		rec := httptest.NewRecorder()
		s.handleLang(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 无根 = %d, want 404", method, rec.Code)
		}
	}
}
