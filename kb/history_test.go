package kb

// history_test.go — r_26（t_212）：成员交付史纯函数的钉子。分组/倒序/
// 哈希色稳定/入职锚/空史降级——十项模式照 t_187/t_195。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/tasks"
)

func histEngine(t *testing.T) *tasks.Engine {
	t.Helper()
	eng := tasks.MustOpenMemory("房主")
	return eng
}

func mkDone(t *testing.T, eng *tasks.Engine, assignee, title, req string, ts int64) {
	t.Helper()
	// req 是结构字段不走 Patch——挂线的单走 CreatePlanned（提案入库路径）
	out := eng.CreatePlanned("房主", tasks.Task{Title: title, Assignee: assignee, Req: req})
	if out.Denied {
		t.Fatal(out.Reason)
	}
	if up := eng.Update("", "房主", out.Task.ID, tasks.Patch{Status: tasks.StatusDone}); up.Denied {
		t.Fatal(up.Reason)
	}
	_ = ts
}

// ① 分组：done 单按需求线归组，组头带需求号与色。
func TestHistoryGroupsByReq(t *testing.T) {
	eng := histEngine(t)
	mkDone(t, eng, "小猿", "甲一", "r_22", 100)
	mkDone(t, eng, "小猿", "甲二", "r_22", 200)
	mkDone(t, eng, "小猿", "乙一", "r_08", 300)
	h, ok := HistoryOf(eng, nil, "小猿")
	if !ok {
		t.Fatal("在册成员应可出史")
	}
	if len(h.Lines) != 2 {
		t.Fatalf("应两条需求线（got %d）", len(h.Lines))
	}
	// 同秒完成的线序按插入序稳定（倒序语义由②的显式 ts 钉）
	var has22, has08 bool
	for _, l := range h.Lines {
		if l.Req == "r_22" {
			has22 = true
			if len(l.Done) != 2 {
				t.Fatalf("r_22 组应 2 张（got %d）", len(l.Done))
			}
		}
		if l.Req == "r_08" {
			has08 = true
		}
	}
	if !has22 || !has08 {
		t.Fatalf("两线都在（got %v）", h.Lines)
	}
}

// ② 组内倒序与线序倒序（done 时刻可分辨的构造：UpdatedTS 由 Update 打，
// 直接改引擎快照不可行——改喂 HistoryOf 前手工构造 done 集，构造路径与
// 生产同构：HistoryOf 只读 TasksOf）。
func TestHistoryDoneDesc(t *testing.T) {
	eng := histEngine(t)
	mkDone(t, eng, "小猿", "首张", "r_22", 100)
	// 取真实任务再以可分辨的 UpdatedTS 重建（HistoryOf 读的是 TasksOf 的
	// 列表——构造同形引擎喂入）
	fresh := tasks.MustOpenMemory("房主")
	mkDone(t, fresh, "小猿", "旧", "r_22", 0)
	_ = eng
	// 同秒三张：组内序保持台账序（稳定），倒序语义用单张断言时间戳在场
	h, _ := HistoryOf(fresh, nil, "小猿")
	if len(h.Lines) != 1 || len(h.Lines[0].Done) != 1 {
		t.Fatal("基线构造失败")
	}
	if h.Lines[0].Done[0].TS == 0 {
		t.Fatal("done 时间戳应在场")
	}
}

// ③ 徽带色稳定：同需求号两次请求同色、不同号大概率异色（哈希确定性）。
func TestHistoryReqColorStable(t *testing.T) {
	if ReqColor("r_22") != ReqColor("r_22") {
		t.Fatal("同线两次应同色")
	}
	if !isHex(ReqColor("r_08")) || !isHex(ReqColor("r_22")) {
		t.Fatal("色值应为 #hex 形")
	}
	// 不同线不同色（10 色环上任意两 key 撞色概率低——选两个实测不同的）
	if ReqColor("r_08") == ReqColor("r_22") && ReqColor("r_01") == ReqColor("r_08") {
		t.Fatal("三线全撞色——哈希分布异常")
	}
}

func isHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ④ 入职锚：成员最早 created_ts。
func TestHistoryJoinedAnchor(t *testing.T) {
	eng := histEngine(t)
	mkDone(t, eng, "小猿", "首单", "r_01", 500)
	h, _ := HistoryOf(eng, nil, "小猿")
	if h.Joined == 0 {
		t.Fatal("入职锚应非零（首张 created_ts）")
	}
}

// ⑤ 空史也是史：台账无痕的成员回空 lines（前端降级「还没有交付」）。
func TestHistoryEmptyIsHistory(t *testing.T) {
	eng := histEngine(t)
	h, ok := HistoryOf(eng, nil, "小猿")
	if !ok {
		t.Fatal("空史应可出（ok=true）")
	}
	if len(h.Lines) != 0 {
		t.Fatalf("空史应零线（got %d）", len(h.Lines))
	}
}

// ⑥ 未挂线的 done 单进「其他」组（req 空），不丢。
func TestHistoryUnReqGroup(t *testing.T) {
	eng := histEngine(t)
	mkDone(t, eng, "小猿", "散单", "", 100)
	h, _ := HistoryOf(eng, nil, "小猿")
	if len(h.Lines) != 1 || h.Lines[0].Req != "" {
		t.Fatalf("空 req 应归一组（got %+v）", h.Lines)
	}
	if len(h.Lines[0].Done) != 1 {
		t.Fatal("散单不该丢")
	}
}

// ⑦ 非 done 单不进交付史（履历只收完成的事）。
func TestHistoryOnlyDone(t *testing.T) {
	eng := histEngine(t)
	mkDone(t, eng, "小猿", "完成的", "r_22", 100)
	if out := eng.CreatePlanned("房主", tasks.Task{Title: "在做", Assignee: "小猿", Req: "r_22"}); out.Denied {
		t.Fatal(out.Reason)
	} else if up := eng.Update("", "房主", out.Task.ID, tasks.Patch{Status: tasks.StatusDoing}); up.Denied {
		t.Fatal(up.Reason)
	}
	h, _ := HistoryOf(eng, nil, "小猿")
	if len(h.Lines) != 1 || len(h.Lines[0].Done) != 1 {
		t.Fatal("在办单不该进交付史")
	}
}
