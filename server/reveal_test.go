package server

// reveal_test.go — the /reveal face's behavior: GET paints the
// platform's live verdict, POST persists + swaps the in-process mirror
// in one breath, and the HEAL nudge (RevealIndexWorkspaces) fires on
// the turning-ON write only. The zcode mirror is a package global, so
// every test resets it (both ends: start, and cleanup) — the suite
// stays order-free.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/chat"
	"github.com/WWestC/Niuma_Studio/zcode"
)

// rvStage is one handler-wired Server over a temp registry root. No
// Start/listener needed — the handler only touches opts.Registry and
// the zcode mirror.
type rvStage struct {
	s     *Server
	fired int // RevealIndexWorkspaces heal-nudge count
}

func newRvStage(t *testing.T) *rvStage {
	t.Helper()
	t.Setenv("NIUMA_REVEAL", "") // env 不表态：文件与覆写是唯一变量
	zcode.SetRevealOverride("")
	t.Cleanup(func() { zcode.SetRevealOverride("") })
	st := &rvStage{}
	st.s = &Server{opts: Options{Registry: chat.NewRegistry(nil, t.TempDir(), "房主", "staff"),
		Fleet: FleetFuncs{RevealIndexFn: func() { st.fired++ }}}}
	return st
}

func (st *rvStage) call(t *testing.T, method, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/reveal", nil)
	} else {
		req = httptest.NewRequest(method, "/reveal", strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	st.s.handleReveal(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// GET 未表态时画平台默认：darwin 关、其余开——开关画的就是进程
// 真正会做的事，这一条是「面与门同源」的钉子。
func TestRevealGetDefault(t *testing.T) {
	st := newRvStage(t)
	code, out := st.call(t, http.MethodGet, "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d, want 200", code)
	}
	if out["mode"] != "" {
		t.Errorf("默认 mode = %v, want \"\"", out["mode"])
	}
	if want := runtime.GOOS != "darwin"; out["on"] != want {
		t.Errorf("默认 on = %v, want %v（平台默认）", out["on"], want)
	}
	if out["darwin"] != (runtime.GOOS == "darwin") {
		t.Errorf("darwin 位与真实平台不符：%v", out["darwin"])
	}
}

// POST 开：落盘 link（darwin）/auto（其余）、镜像当场换、治疗性强投
// 恰好一发；GET 随后画开。POST 关：落盘 off、不强投。
func TestRevealPostRoundtrip(t *testing.T) {
	st := newRvStage(t)
	path := filepath.Join(st.s.opts.Registry.Root(), "reveal.json")

	code, out := st.call(t, http.MethodPost, `{"on":true}`)
	if code != http.StatusOK {
		t.Fatalf("POST on = %d: %v", code, out)
	}
	wantMode := "auto"
	if runtime.GOOS == "darwin" {
		wantMode = "link"
	}
	if out["mode"] != wantMode || out["on"] != true {
		t.Fatalf("POST on 回执 = %v, want mode %s on true", out, wantMode)
	}
	if got := zcode.LoadRevealMode(path); got != wantMode {
		t.Fatalf("落盘 = %q, want %q", got, wantMode)
	}
	if got := zcode.RevealMode(); got != wantMode {
		t.Fatalf("进程镜像 = %q, want %q（下一次投递立刻按新模式走）", got, wantMode)
	}
	if st.fired != 1 {
		t.Fatalf("开启强投次数 = %d, want 1（治疗性一发）", st.fired)
	}
	if code, out = st.call(t, http.MethodGet, ""); code != 200 || out["on"] != true {
		t.Fatalf("GET after on = %d %v, want on true", code, out)
	}

	if _, out = st.call(t, http.MethodPost, `{"on":false}`); out["mode"] != "off" || out["on"] != false {
		t.Fatalf("POST off 回执 = %v, want mode off on false", out)
	}
	if got := zcode.LoadRevealMode(path); got != "off" {
		t.Fatalf("落盘 = %q, want off", got)
	}
	if st.fired != 1 {
		t.Fatalf("关闭不该强投：fired = %d, want 1", st.fired)
	}
}

// 载荷必须带布尔 on：缺失（null）与坏 JSON 都是 400，一行不改。
func TestRevealPostValidation(t *testing.T) {
	st := newRvStage(t)
	for _, body := range []string{`{}`, `{"on":null}`, `{bad`, ``} {
		if code, _ := st.call(t, http.MethodPost, body); code != http.StatusBadRequest {
			t.Errorf("POST %q = %d, want 400", body, code)
		}
	}
	if st.fired != 0 {
		t.Fatalf("被拒的载荷不该强投：fired = %d", st.fired)
	}
}

// 无注册根（内嵌/测试形态）整面 404——/retention 的同一纪律。
func TestRevealNoRoot(t *testing.T) {
	s := &Server{}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/reveal", strings.NewReader(`{"on":true}`))
		rec := httptest.NewRecorder()
		s.handleReveal(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 无根 = %d, want 404", method, rec.Code)
		}
	}
}
