package chat

// queues.go 的全态投影纪律：SetQueued 每次整体替换该成员的排队集
// （加、缩、清一帧一义，无增减歧义）；只认历史环里的 say；快照按
// seq 升序、成员名排序；SayLine 把原行交给调度器的自动引用。

import (
	"reflect"
	"testing"
)

func queueWhoOf(h *Hub, seq int64) []string {
	for _, row := range h.QueueRows() {
		if row.Seq == seq {
			return row.Who
		}
	}
	return nil
}

func TestSetQueuedReplacesWholesale(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("张三", "", false)
	b, _ := h.Join("李四", "", false)
	_ = b
	h.Say(a, "第一条")
	s1 := h.LastSeq()
	h.Say(a, "第二条")
	s2 := h.LastSeq()

	h.SetQueued("张三", s1)
	if got := queueWhoOf(h, s1); !reflect.DeepEqual(got, []string{"张三"}) {
		t.Fatalf("s1 排队集应为 [张三]，得到 %v", got)
	}

	// 整体替换：加入 s2 的同时必须退出 s1 —— 全态不是并集
	h.SetQueued("张三", s2)
	if got := queueWhoOf(h, s1); got != nil {
		t.Fatalf("替换后 s1 不该再有 张三：%v", got)
	}
	if got := queueWhoOf(h, s2); !reflect.DeepEqual(got, []string{"张三"}) {
		t.Fatalf("s2 排队集应为 [张三]，得到 %v", got)
	}

	// 清空：空集帧即离场
	h.SetQueued("张三")
	if rows := h.QueueRows(); len(rows) != 0 {
		t.Fatalf("清空后不该再有排队行：%v", rows)
	}
}

func TestSetQueuedIgnoresNonSayAndStaleSeqs(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("张三", "", false)
	h.Say(a, "say 行")
	saySeq := h.LastSeq()
	h.SystemRecorded("系统行") // 落史但非 say：不该挣到排队投影
	sysSeq := h.LastSeq()
	if sysSeq == saySeq {
		t.Fatal("前提失败：系统行应占独立 seq")
	}

	h.SetQueued("张三", saySeq, sysSeq, saySeq /* 去重 */, 0 /* 噪声 */)
	if got := queueWhoOf(h, saySeq); !reflect.DeepEqual(got, []string{"张三"}) {
		t.Fatalf("say 行排队集应为 [张三]，得到 %v", got)
	}
	if got := queueWhoOf(h, sysSeq); got != nil {
		t.Fatalf("系统行不该有排队投影：%v", got)
	}
	if rows := h.QueueRows(); len(rows) != 1 {
		t.Fatalf("去重后只该一行，得到 %d", len(rows))
	}
}

func TestQueueRowsSorted(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("张三", "", false)
	h.Say(a, "一")
	s1 := h.LastSeq()
	h.Say(a, "二")
	s2 := h.LastSeq()

	h.SetQueued("李四", s2, s1) // 乱序喂入
	h.SetQueued("张三", s2)
	rows := h.QueueRows()
	if len(rows) != 2 || rows[0].Seq != s1 || rows[1].Seq != s2 {
		t.Fatalf("行序应按 seq 升序：%v", rows)
	}
	if !reflect.DeepEqual(rows[1].Who, []string{"张三", "李四"}) {
		t.Fatalf("成员名应排序：%v", rows[1].Who)
	}
}

func TestSayLineHandsBackOriginal(t *testing.T) {
	h := NewHub()
	a, _ := h.Join("李四", "", false)
	h.Say(a, "等一下别发了 稍等")
	seq := h.LastSeq()

	from, text, ok := h.SayLine(seq)
	if !ok || from != "李四" || text != "等一下别发了 稍等" {
		t.Fatalf("SayLine 应还回原行：%q %q %v", from, text, ok)
	}
	h.SystemRecorded("系统行")
	if _, _, ok := h.SayLine(h.LastSeq()); ok {
		t.Fatal("系统行不是 say，不该还回")
	}
	if _, _, ok := h.SayLine(seq + 999); ok {
		t.Fatal("不存在的 seq 不该还回")
	}
}
