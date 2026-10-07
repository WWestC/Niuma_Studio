package wire_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/WWestC/Niuma_Studio/meeting"
	"github.com/WWestC/Niuma_Studio/merge"
	"github.com/WWestC/Niuma_Studio/plan"
	"github.com/WWestC/Niuma_Studio/requirements"
	"github.com/WWestC/Niuma_Studio/tasks"
)

// The mirror discipline's teeth: every domain payload and its wire
// mirror must marshal to IDENTICAL JSON for a fully-populated fixture.
// If a domain field changes and the mirror is not updated WITH it, the
// corresponding test here fails — that is the moment to decide whether
// the wire changes too (update mirror + regenerate wireconv) or the
// field stays off the wire (leave the mirror alone). This file is the
// ONLY place the wire test binary may import domain packages.
//
// The fixtures are FULL-field by discipline, and requireFullFixture
// pins that discipline itself: a field the fixture forgets to
// populate would let a broken conversion slip through the byte
// comparison (both sides marshal the zero value), so every exported
// field of every fixture must be non-zero — the fixture can't silently
// stop covering a field the domain grew.

// fullFixtureSkips lists justified zero fields per fixture type
// (none today — the discipline is genuinely full-field).
var fullFixtureSkips = map[string]map[string]bool{}

func requireFullFixture(t *testing.T, v any) {
	t.Helper()
	assertNonZero(t, reflect.ValueOf(v), reflect.TypeOf(v).String())
}

func assertNonZero(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			t.Fatalf("fixture 未填满：%s 是 nil——满字段纪律要求每个导出字段非零（豁免须登记 fullFixtureSkips）", path)
		}
		assertNonZero(t, v.Elem(), path)
	case reflect.Struct:
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath != "" {
				continue // unexported
			}
			fp := fmt.Sprintf("%s.%s", path, f.Name)
			if fullFixtureSkips[typ.String()] != nil && fullFixtureSkips[typ.String()][f.Name] {
				continue
			}
			assertNonZero(t, v.Field(i), fp)
		}
	case reflect.Slice, reflect.Map:
		if v.Len() == 0 {
			t.Fatalf("fixture 未填满：%s 为空——满字段纪律要求集合字段至少一个元素", path)
		}
		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				assertNonZero(t, v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		} else {
			iter := v.MapRange()
			for iter.Next() {
				assertNonZero(t, iter.Value(), path+"[key]")
			}
		}
	case reflect.String:
		if v.Len() == 0 {
			t.Fatalf("fixture 未填满：%s 是空串——满字段纪律要求字符串字段非空", path)
		}
	case reflect.Bool:
		if !v.Bool() {
			t.Fatalf("fixture 未填满：%s 是 false——布尔字段须为 true（false 与零值不可分辨）", path)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Int() == 0 {
			t.Fatalf("fixture 未填满：%s 是 0——数值字段须非零（0 与零值不可分辨）", path)
		}
	}
}

func taskFixture() tasks.Task {
	return tasks.Task{
		ID: "t_07", Title: "根除上帝对象", Desc: "description", Assignee: "小牛",
		Status: "doing", Progress: "70%", CreatedBy: "小马", CreatedTS: 1, UpdatedTS: 2,
		Log: []tasks.LogEntry{{TS: 3, By: "小牛", Note: "状态 todo→doing"}},
		Pending: &tasks.Proposal{
			By:    "小马",
			Patch: tasks.Patch{Status: "done", Progress: "100%", Note: "n", Assignee: "a", Project: "p", Version: "v", Start: "s", End: "e", Deps: "d", Milestone: "m", Pct: "c", Priority: "high"},
			Need:  []string{"小牛", "小马"}, Got: []string{"小牛"}, Kind: "change", TS: 4,
		},
		ProjectKey: "proj-x", Parent: "t_01", Req: "r_02", Version: "v1", PlanID: "p_03",
		StartTS: 5, EndTS: 6, Deps: []string{"t_05", "t_06"}, Milestone: true,
		ProgressPct: 70, Priority: "urgent", DoingTS: 7, DoneTS: 8,
	}
}

func TestTaskMirrorJSONIdentical(t *testing.T) {
	d := taskFixture()
	a, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	requireFullFixture(t, d)
	b, err := json.Marshal(*d.Wire())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("task JSON diverged:\n domain: %s\n mirror: %s", a, b)
	}
	// round-trip back: TaskFromWire(Wire(t)) must also marshal identical
	back := tasks.TaskFromWire(d.Wire())
	c, _ := json.Marshal(*back)
	if string(a) != string(c) {
		t.Fatalf("task round-trip diverged:\n orig: %s\n back: %s", a, c)
	}
}

func TestPatchMirrorJSONIdentical(t *testing.T) {
	d := tasks.Patch{Status: "done", Progress: "1", Note: "n", Assignee: "a",
		Project: "p", Version: "v", Start: "s", End: "e", Deps: "d", Milestone: "m", Pct: "c", Priority: "low"}
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(d.Wire())
	if string(a) != string(b) {
		t.Fatalf("patch JSON diverged:\n domain: %s\n mirror: %s", a, b)
	}
	requireFullFixture(t, d)
	w := d.Wire()
	c, _ := json.Marshal(tasks.PatchFromWire(&w))
	if string(a) != string(c) {
		t.Fatalf("patch round-trip diverged:\n orig: %s\n back: %s", a, c)
	}
}

func TestPlanMirrorJSONIdentical(t *testing.T) {
	d := plan.Plan{
		ID: "p_01", Req: "r_01", Title: "提案", Need: "需要",
		ProjectKey: "proj-x", Version: "v1",
		Tasks: []plan.PlanTask{
			{Title: "甲", Desc: "d", Assignee: "小牛", StartTS: 1, EndTS: 2, Deps: []int{2}, Milestone: true},
			{Title: "乙", Desc: "d2", Assignee: "小马", StartTS: 3, EndTS: 4, Deps: []int{1}, Milestone: true},
		},
		SubmittedBy: "小牛", SubmittedTS: 3,
	}
	requireFullFixture(t, d)
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(*d.Wire())
	if string(a) != string(b) {
		t.Fatalf("plan JSON diverged:\n domain: %s\n mirror: %s", a, b)
	}
	back := plan.PlanFromWire(d.Wire())
	c, _ := json.Marshal(*back)
	if string(a) != string(c) {
		t.Fatalf("plan round-trip diverged")
	}
}

func TestMergeMirrorJSONIdentical(t *testing.T) {
	d := merge.Merge{
		ID: "m_01", ProjectKey: "proj-x", Branch: "feat/x", Into: "main", Req: "r_01",
		Tasks: []string{"t_01", "t_02"}, Commits: 3, Files: 4, Additions: 10, Deletions: 2,
		SubmittedBy: "小牛", SubmittedTS: 5,
	}
	requireFullFixture(t, d)
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(*d.Wire())
	if string(a) != string(b) {
		t.Fatalf("merge JSON diverged:\n domain: %s\n mirror: %s", a, b)
	}
}

func TestReqMirrorJSONIdentical(t *testing.T) {
	d := requirements.Req{
		ID: "r_01", ProjectKey: "proj-x", Title: "需求", Body: "body", Status: "open",
		CreatedBy: "小马", CreatedTS: 1, ParkNote: "等待", ParkTS: 2, ReviewAfter: 3, ClosedTS: 4,
	}
	requireFullFixture(t, d)
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(*d.Wire())
	if string(a) != string(b) {
		t.Fatalf("req JSON diverged:\n domain: %s\n mirror: %s", a, b)
	}
}

func TestMeetingMirrorJSONIdentical(t *testing.T) {
	d := meeting.Meeting{
		ID: "m_01", ProjectKey: "proj-x", ReqID: "r_01", ReqTitle: "需求",
		Chair: "小牛", Participants: []string{"小马", "小鸟"}, Status: "discuss", Phase: "summary",
		Opening: "开场", Conclusion: "结论",
		Transcript: []meeting.Line{{TS: 1, By: "小马", Text: "发言"}},
		StartTS:    2, EndTS: 3,
	}
	requireFullFixture(t, d)
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(*d.Wire())
	if string(a) != string(b) {
		t.Fatalf("meeting JSON diverged:\n domain: %s\n mirror: %s", a, b)
	}
	back := meeting.MeetingFromWire(d.Wire())
	c, _ := json.Marshal(*back)
	if string(a) != string(c) {
		t.Fatalf("meeting round-trip diverged")
	}
}
