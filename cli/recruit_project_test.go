package cli

// The recruit/dispatch project threading (v2.9, the t_223 lesson): a
// hire must be able to NAME its office. Before this, every birth POST
// and every recruit step was project-blind — the fleet's absent-project
// default silently landed the hire in the lobby no matter which office
// was hiring, and 小笔 (born for the book project) seated in 大厅 with
// the onboarding ledger written to the lobby's shelf. These pin the
// three wires: the birth body's project field, the CLI flag → pickScope
// plumbing on dispatch, and the per-office ledger/verify addressing.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestRecruitBirthBodyCarriesProject: the POST body carries project
// when set and omits it when empty (the legacy lobby wire shape).
func TestRecruitBirthBodyCarriesProject(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{} // fresh per request: Decode keeps stale keys
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("sess_p1"))
	}))
	defer srv.Close()
	url := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/ws"

	if _, err := recruitBirth(context.Background(), url, "小笔", "写手", "book"); err != nil {
		t.Fatalf("birth: %v", err)
	}
	if got["project"] != "book" {
		t.Fatalf("body.project = %q, want book", got["project"])
	}

	if _, err := recruitBirth(context.Background(), url, "小新", "测试", ""); err != nil {
		t.Fatalf("legacy birth: %v", err)
	}
	if v, ok := got["project"]; ok {
		t.Fatalf("empty scope must omit the field, got %q", v)
	}
}

// TestDispatchBirthPostsScope: --project rides pickScope into the POST
// body; an unbound cwd (HOME redirected, no env) keeps the field away.
func TestDispatchBirthPostsScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows 的 os.UserHomeDir 读 USERPROFILE——两侧同设，家目录才落进测试沙箱
	t.Setenv("NIUMA_PROJECT", "")

	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{} // fresh per request: Decode keeps stale keys
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("sess_p2"))
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	if rc := runDispatch([]string{"birth", "--name", "小笔", "--role", "写手",
		"--project", "book", "--port", strconv.Itoa(port)}); rc != 0 {
		t.Fatalf("dispatch birth rc = %d, want 0", rc)
	}
	if got["project"] != "book" {
		t.Fatalf("body.project = %q, want book", got["project"])
	}

	if rc := runDispatch([]string{"steer", "--name", "小笔", "--text", "hi",
		"--port", strconv.Itoa(port)}); rc != 0 {
		t.Fatalf("dispatch steer rc = %d, want 0", rc)
	}
	if v, ok := got["project"]; ok {
		t.Fatalf("unscoped steer must omit the field, got %q", v)
	}
}

// TestRoomLogKey: the onboarding ledger's per-office shelf — the same
// one-address-per-office rule as the offboard ceremony.
func TestRoomLogKey(t *testing.T) {
	cases := []struct{ proj, want string }{
		{"", "ops/room-log"},
		{"default", "ops/room-log"},
		{"book", "p/book/room-log"},
	}
	for _, c := range cases {
		if got := roomLogKey(c.proj); got != c.want {
			t.Errorf("roomLogKey(%q) = %q, want %q", c.proj, got, c.want)
		}
	}
}

// TestRecruitSeatPresentProjectRow: a project hire verifies against the
// project's staffing rows (seated or active), not the lobby roster.
func TestRecruitSeatPresentProjectRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p/book/staffing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"project":"book","rows":[` +
			`{"person":"小苗","state":"active","seated":true},` +
			`{"person":"小笔","state":"active","seated":false}]}`))
	}))
	defer srv.Close()
	url := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/ws"

	if err := recruitSeatPresent(url, "小笔", "book"); err != nil {
		t.Fatalf("active row should verify: %v", err)
	}
	if err := recruitSeatPresent(url, "小鹿", "book"); err == nil {
		t.Fatal("absent person must not verify")
	}
}
