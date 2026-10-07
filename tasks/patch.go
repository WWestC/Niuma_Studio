// v2 P4-a: the Patch six text keys' parse/apply/summary machinery
// (PRD §4.2 合并定案, orchestration §5.1). The keys stay strings on the
// wire and the CLI — the house "全 string＋CLI flag 直映" idiom — and are
// parsed here into a schedPatch, applied to Task's ten-field extension,
// and summarized for the change log. Storage and protocol keep the O6
// 口径: Unix seconds and 0–100 int; "YYYY-MM-DD HH:MM" is a human INPUT
// encoding only.
package tasks

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/util"
)

// schedTimeLayout is the human input encoding for --start/--end. Local
// time (the single-machine trust model has one clock that matters).
const schedTimeLayout = "2006-01-02 15:04"

// summaryTimeLayout clips a timestamp for one-line log summaries
// ("排期 → 10-01 09:00~10-02 12:00").
const summaryTimeLayout = "01-02 15:04"

// pctInText finds the first percentage in a free-form progress note —
// the Q5 写时双写 rule's trigger.
var pctInText = regexp.MustCompile(`(\d{1,3})\s*%`)

// depIDPattern is the t_NN reference shape deps tokens must carry.
var depIDPattern = regexp.MustCompile(`^t_\d{1,4}$`)

// maxVersionRunes mirrors the projects domain's version-name cap, so a
// task's version attachment cannot outgrow the entity it names.
const maxVersionRunes = 48

// schedPatch is the parsed projection of a Patch's text keys — the six
// scheduling keys plus priority, which shares the same set-flag/clear
// ("‑") machinery though it is a display key, not a scheduling one.
// Each set* flag marks "this key was present" so an explicit zero
// ("pct 0", "milestone false", "priority -") is distinguishable from
// "leave unchanged".
type schedPatch struct {
	setProject   bool
	project      string
	setVersion   bool
	version      string
	setStart     bool
	startTS      int64
	setEnd       bool
	endTS        int64
	setDeps      bool
	deps         []string
	setMilestone bool
	milestone    bool
	setPct       bool
	pct          int
	setPriority  bool
	priority     string
}

// parseSchedKeys converts the six text keys (plus the Q5 dual-write
// rule over Progress) into their field values. Empty key = leave
// unchanged; "-" clears (project/version/deps, start/end); malformed
// input is an error the write path turns into a denial.
func parseSchedKeys(p Patch) (schedPatch, error) {
	var s schedPatch

	if v := strings.TrimSpace(p.Project); v != "" {
		s.setProject = true
		if v != "-" {
			s.project = v
		}
	}
	if v := strings.TrimSpace(p.Version); v != "" {
		s.setVersion = true
		if v != "-" {
			s.version = clampRunes(v, maxVersionRunes)
		}
	}
	for _, kv := range []struct {
		name, raw string
		dst       *int64
	}{
		{"start", p.Start, &s.startTS},
		{"end", p.End, &s.endTS},
	} {
		v := strings.TrimSpace(kv.raw)
		if v == "" {
			continue
		}
		if v == "-" {
			if kv.name == "start" {
				s.setStart = true
			} else {
				s.setEnd = true
			}
			continue
		}
		ts, err := time.ParseInLocation(schedTimeLayout, v, time.Local)
		if err != nil {
			return s, errors.New(i18n.Sf("非法 %s %q（要 YYYY-MM-DD HH:MM、\"-\" 清除或留空不改）", kv.name, v))
		}
		if kv.name == "start" {
			s.setStart, s.startTS = true, ts.Unix()
		} else {
			s.setEnd, s.endTS = true, ts.Unix()
		}
	}
	if v := strings.TrimSpace(p.Deps); v != "" {
		s.setDeps = true
		if v != "-" {
			var deps []string
			for _, tok := range strings.Split(v, ",") {
				tok = strings.TrimSpace(tok)
				if tok == "" || !depIDPattern.MatchString(tok) {
					return s, errors.New(i18n.Sf("非法依赖列表 %q（要 t_01,t_02 形式、\"-\" 清空）", v))
				}
				if !util.Contains(deps, tok) {
					deps = append(deps, tok)
				}
			}
			s.deps = deps
		}
	}
	if v := strings.TrimSpace(p.Milestone); v != "" {
		s.setMilestone = true
		switch v {
		case "true":
			s.milestone = true
		case "false":
		default:
			return s, errors.New(i18n.Sf("非法 milestone %q（true|false）", v))
		}
	}
	if v := strings.TrimSpace(p.Pct); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 100 {
			return s, errors.New(i18n.Sf("非法 pct %q（0–100）", v))
		}
		s.setPct, s.pct = true, n
	}
	if v := strings.TrimSpace(p.Priority); v != "" {
		s.setPriority = true
		if v != "-" {
			if !ValidPriority(v) {
				return s, errors.New(i18n.Sf("非法 priority %q（urgent|high|medium|low、\"-\" 清除）", v))
			}
			s.priority = v
		}
	}

	// Q5 写时双写: a progress note carrying a percentage syncs the
	// numeric field when the patch did not set pct explicitly (an
	// explicit --pct wins and never touches the text the other way).
	if !s.setPct && strings.TrimSpace(p.Progress) != "" {
		if m := pctInText.FindStringSubmatch(p.Progress); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil || n < 0 || n > 100 {
				return s, errors.New(i18n.Sf("进度文本中的百分数越界（0–100）: %s", m[0]))
			}
			s.setPct, s.pct = true, n
		}
	}
	return s, nil
}

// applyTo writes the parsed values into t's ten-field slots. A cleared
// key lands its zero value (empty string / nil slice), which omitempty
// then drops from the snapshot.
func (s schedPatch) applyTo(t *Task) {
	if s.setProject {
		t.ProjectKey = s.project
	}
	if s.setVersion {
		t.Version = s.version
	}
	if s.setStart {
		t.StartTS = s.startTS
	}
	if s.setEnd {
		t.EndTS = s.endTS
	}
	if s.setDeps {
		t.Deps = s.deps
	}
	if s.setMilestone {
		t.Milestone = s.milestone
	}
	if s.setPct {
		t.ProgressPct = s.pct
	}
	if s.setPriority {
		t.Priority = s.priority
	}
}

// schedParts summarizes the scheduling-field differences between old
// (the pre-patch snapshot) and the task after applyTo — the change-log
// line pieces like 「挂靠 proj-x」「排期 → 10-01 09:00~10-02 12:00」「依赖
// +t_85」. Unchanged fields stay silent.
func schedParts(old, new Task) []string {
	var parts []string
	if old.ProjectKey != new.ProjectKey {
		switch {
		case new.ProjectKey == "":
			parts = append(parts, i18n.S("取消挂靠（回 Niuma_Studio）"))
		case old.ProjectKey == "":
			parts = append(parts, i18n.Sf("挂靠 %s", new.ProjectKey))
		default:
			parts = append(parts, i18n.Sf("挂靠 %s→%s", old.ProjectKey, new.ProjectKey))
		}
	}
	if old.Version != new.Version {
		switch {
		case new.Version == "":
			parts = append(parts, i18n.S("版本清除"))
		case old.Version == "":
			parts = append(parts, i18n.Sf("版本 %s", new.Version))
		default:
			parts = append(parts, i18n.Sf("版本 %s→%s", old.Version, new.Version))
		}
	}
	if old.StartTS != new.StartTS || old.EndTS != new.EndTS {
		switch {
		case new.StartTS > 0 && new.EndTS > 0:
			parts = append(parts, i18n.Sf("排期 → %s~%s",
				fmtSchedTS(new.StartTS), fmtSchedTS(new.EndTS)))
		case new.StartTS > 0:
			parts = append(parts, i18n.Sf("排期开始 → %s", fmtSchedTS(new.StartTS)))
		case new.EndTS > 0:
			parts = append(parts, i18n.Sf("排期截止 → %s", fmtSchedTS(new.EndTS)))
		default:
			parts = append(parts, i18n.S("排期清除"))
		}
	}
	if old.Milestone != new.Milestone {
		if new.Milestone {
			parts = append(parts, i18n.S("设为里程碑"))
		} else {
			parts = append(parts, i18n.S("取消里程碑"))
		}
	}
	if !sameIDs(old.Deps, new.Deps) {
		switch {
		case len(new.Deps) == 0:
			parts = append(parts, i18n.S("依赖清空"))
		default:
			var diff []string
			for _, d := range new.Deps {
				if !util.Contains(old.Deps, d) {
					diff = append(diff, "+"+d)
				}
			}
			for _, d := range old.Deps {
				if !util.Contains(new.Deps, d) {
					diff = append(diff, "-"+d)
				}
			}
			parts = append(parts, i18n.Sf("依赖 %s", strings.Join(diff, ",")))
		}
	}
	if old.ProgressPct != new.ProgressPct {
		parts = append(parts, i18n.Sf("数值进度 %d%%", new.ProgressPct))
	}
	if old.Priority != new.Priority {
		switch {
		case new.Priority == "":
			parts = append(parts, i18n.S("优先级清除"))
		case old.Priority == "":
			parts = append(parts, i18n.Sf("优先级 %s", PriorityLabel(new.Priority)))
		default:
			parts = append(parts, i18n.Sf("优先级 %s→%s", PriorityLabel(old.Priority), PriorityLabel(new.Priority)))
		}
	}
	return parts
}

func fmtSchedTS(ts int64) string { return time.Unix(ts, 0).Format(summaryTimeLayout) }

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
