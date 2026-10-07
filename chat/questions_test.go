package chat

// questions_test.go — 向房主提问的台账侧：出卡登记、快照、收口与
// 换代清场的状态机。广播本身不在此考（Broadcast 无连接时静默落空），
// 线上往返由 dispatch/asks_test.go 端到端钉住。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/util"
)

func TestQuestionLedgerRoundTrip(t *testing.T) {
	h := NewHub()
	if got := h.OpenQuestions(); len(got) != 0 {
		t.Fatalf("空台账应有 0 开放问题，得到 %d", len(got))
	}

	q := &Question{ID: "q_1", From: "小牛", TS: util.Now(), Prompt: "标签视觉方向",
		Asks: []QuestionAsk{{
			Question: "主打哪种风格？",
			Header:   "风格",
			Options:  []QuestionOption{{Label: "像素复古", Desc: "8-bit 办公室"}, {Label: "终端极客"}},
		}}}
	h.AskQuestion(q)
	open := h.OpenQuestions()
	if len(open) != 1 {
		t.Fatalf("出卡后应有 1 开放问题，得到 %d", len(open))
	}
	if open[0].From != "小牛" || open[0].Prompt != "标签视觉方向" {
		t.Fatalf("快照丢字段：%+v", open[0])
	}
	if len(open[0].Asks) != 1 || len(open[0].Asks[0].Options) != 2 ||
		open[0].Asks[0].Options[0].Label != "像素复古" {
		t.Fatalf("问题/选项结构未如实入账：%+v", open[0].Asks)
	}

	// 收口：首次成功、开放集清空、重复收口拒绝
	if !h.ResolveQuestion("q_1", "answered", "房主", map[string]string{"主打哪种风格？": "像素复古"}) {
		t.Fatal("首次收口应成功")
	}
	if got := h.OpenQuestions(); len(got) != 0 {
		t.Fatalf("收口后开放集应清空，仍有 %d", len(got))
	}
	if h.ResolveQuestion("q_1", "expired", "", nil) {
		t.Fatal("重复收口应返回 false（先到先得）")
	}

	// 换代清场：没有等待者的卡全部过期关闭
	h.AskQuestion(&Question{ID: "q_2", From: "小马", TS: util.Now()})
	h.AskQuestion(&Question{ID: "q_3", From: "小牛", TS: util.Now()})
	h.ExpireAllQuestions()
	if got := h.OpenQuestions(); len(got) != 0 {
		t.Fatalf("清场后开放集应清空，仍有 %d", len(got))
	}
}

func TestAskQuestionIgnoresEmptyID(t *testing.T) {
	h := NewHub()
	h.AskQuestion(nil)
	h.AskQuestion(&Question{ID: "", From: "小牛", TS: util.Now()})
	if got := h.OpenQuestions(); len(got) != 0 {
		t.Fatalf("空 id 不应入账，仍有 %d", len(got))
	}
}
