package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDispatchSubcommandsDefineFlagsOnce pins the t_招人 regression:
// birth/bind/steer used to redefine --name/--role after parseCommon,
// which panics the FlagSet at definition time — every invocation died
// before any HTTP (HR 招牛马当场炸，只能绕道裸 curl). -h fails the parse
// and returns 2 without ever dialing the room, so the construction
// path is exercised end-to-end.
func TestDispatchSubcommandsDefineFlagsOnce(t *testing.T) {
	for _, sub := range []string{"birth", "bind", "steer"} {
		rc := runDispatch([]string{sub, "-h"})
		if rc != 2 {
			t.Errorf("dispatch %s -h: rc = %d, want 2 (parse stop before HTTP)", sub, rc)
		}
	}
}

// TestHTTPBase pins the ws→http origin conversion shared by the birth
// POST and the seat verification GET.
func TestHTTPBase(t *testing.T) {
	got := httpBase("ws://127.0.0.1:7777/ws")
	if got != "http://127.0.0.1:7777" {
		t.Errorf("httpBase = %q, want http://127.0.0.1:7777", got)
	}
}

// TestRecruitBirthStatuses covers the dispatcher-face branch table: 200
// returns the session id, 404 / 「调度器未启用」 map to errNoDispatcher
// (the one case where the drive fallback is legitimate), anything else
// is a real refusal passed through verbatim.
func TestRecruitBirthStatuses(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSid string
		wantNoD bool
		wantErr bool
	}{
		{"born", http.StatusOK, "sess_abc123", "sess_abc123", false, false},
		{"no face", http.StatusNotFound, "项目 default 无调度器", "", true, false},
		{"bridge nil", http.StatusBadRequest, "调度器未启用（zcode 不可用）", "", true, false},
		{"refusal", http.StatusBadRequest, "agents store 不可用", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/dispatch/birth" || r.Method != http.MethodPost {
					http.Error(w, "bad route", http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			url := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/ws"
			sid, err := recruitBirth(context.Background(), url, "小新", "测试工程师", "")
			if tc.wantNoD {
				if !errors.Is(err, errNoDispatcher) {
					t.Fatalf("err = %v, want errNoDispatcher", err)
				}
				return
			}
			if tc.wantErr {
				if err == nil || errors.Is(err, errNoDispatcher) {
					t.Fatalf("err = %v, want a verbatim non-dispatcher refusal", err)
				}
				if !strings.Contains(err.Error(), tc.body) {
					t.Errorf("err = %v, want body %q echoed", err, tc.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sid != tc.wantSid {
				t.Errorf("sid = %q, want %q", sid, tc.wantSid)
			}
		})
	}
}
