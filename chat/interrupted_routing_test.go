package chat

// v2.12 卡跟房走的中断接续分发契约：席位按 Project 分组、卡播在
// 席位自己的房间——「小说编写」的活儿断在「小说编写」房里就播在
// 「小说编写」，大厅只收大厅自己的席位，谁也不替谁挡话（项目隔离
// 同口径）。目标房解析不了的席位落大厅兜底，不静默丢。

import (
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

// interruptedFromHistory 收集一间房历史里的全部中断席位（跨多张卡）。
func interruptedFromHistory(t *testing.T, h *Hub) []InterruptedSeat {
	t.Helper()
	var got []InterruptedSeat
	for _, m := range h.History() {
		if m.Type == MsgSystem && m.Event == EventInterrupted {
			got = append(got, m.Interrupted...)
		}
	}
	return got
}

func interruptedNames(seats []InterruptedSeat) string {
	names := make([]string, len(seats))
	for i, s := range seats {
		names[i] = s.Name
	}
	return strings.Join(names, ",")
}

func TestPostInterruptedCardsPerRoom(t *testing.T) {
	store, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := store.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	for _, p := range []projects.Project{
		{Key: "novel", Name: "小说编写", Workspace: t.TempDir()},
		{Key: "web", Name: "官网改版", Workspace: t.TempDir()},
	} {
		if _, err := store.Create(p); err != nil {
			t.Fatalf("create %s: %v", p.Key, err)
		}
		if _, err := store.Activate(p.Key); err != nil {
			t.Fatalf("activate %s: %v", p.Key, err)
		}
	}
	r := NewRegistry(store, "", "房主", "房主")
	if _, err := r.AdoptActive(); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// 三房各有被中断的活儿：大厅（Project 缺省）、小说编写、官网改版。
	r.PostInterruptedCards([]InterruptedSeat{
		{Name: "小笔", Project: "novel", Room: "小说编写"},
		{Name: "小牛"}, // 大厅席位：Project 缺省即大厅
		{Name: "小墨", Project: "novel", Room: "小说编写"},
		{Name: "小马", Project: "web", Room: "官网改版"},
	}, func(names []string) string { return "卡：" + strings.Join(names, "、") })

	lobby, err := r.Lobby()
	if err != nil {
		t.Fatalf("lobby: %v", err)
	}
	novel, err := r.Hub("novel")
	if err != nil {
		t.Fatalf("hub novel: %v", err)
	}
	web, err := r.Hub("web")
	if err != nil {
		t.Fatalf("hub web: %v", err)
	}

	// 各房只见自己人的卡：大厅不替项目挡话，项目房不见别家席位。
	if got := interruptedNames(interruptedFromHistory(t, lobby)); got != "小牛" {
		t.Fatalf("Niuma_Studio 只该有小牛自己的席位卡: %s", got)
	}
	if got := interruptedNames(interruptedFromHistory(t, novel)); got != "小笔,小墨" {
		t.Fatalf("小说编写该收到小笔、小墨的席位卡: %s", got)
	}
	if got := interruptedNames(interruptedFromHistory(t, web)); got != "小马" {
		t.Fatalf("官网改版该只收到小马的席位卡: %s", got)
	}
	// 卡正文由调用方渲染，带各房自己的名单（同房席位并成一张卡）。
	for _, m := range novel.History() {
		if m.Event == EventInterrupted && m.Text != "卡：小笔、小墨" {
			t.Fatalf("项目卡的正文该按本房名单渲染: %q", m.Text)
		}
	}
}

func TestPostInterruptedCardsFallbackToLobby(t *testing.T) {
	store, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := store.EnsureLobby(t.TempDir()); err != nil {
		t.Fatalf("EnsureLobby: %v", err)
	}
	r := NewRegistry(store, "", "房主", "房主")

	// 目标房解析不了（不在登记表）：落大厅兜底，不静默丢。
	r.PostInterruptedCards([]InterruptedSeat{
		{Name: "小笔", Project: "ghost", Room: "幽灵项目"},
	}, func(names []string) string { return names[0] })

	lobby, err := r.Lobby()
	if err != nil {
		t.Fatalf("lobby: %v", err)
	}
	if got := interruptedNames(interruptedFromHistory(t, lobby)); got != "小笔" {
		t.Fatalf("无处可去的席位该落 Niuma_Studio 兜底: %s", got)
	}

	// 空名单/空渲染器是安全空操作。
	r.PostInterruptedCards(nil, func(names []string) string { return "x" })
	r.PostInterruptedCards([]InterruptedSeat{{Name: "小牛"}}, nil)
	if got := interruptedFromHistory(t, lobby); len(got) != 1 {
		t.Fatalf("空输入不得再落卡: %+v", got)
	}
}
