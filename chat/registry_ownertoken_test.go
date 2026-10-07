package chat

// owner-M1：registry 级统一房主凭证——每个实例化房间的预席房主席持有的
// token 与 OwnerCredential() 同源（一件事：无座通道的署名动词只认这一
// 枚，名字不是身份——p/book 事故的根治锚点）。无房主 registry（裸内嵌）
// 凭证为空，门自然解除武装。

import (
	"testing"

	"github.com/WWestC/Niuma_Studio/projects"
)

func TestRegistryOwnerCredentialUnified(t *testing.T) {
	store, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	if _, err := store.Create(projects.Project{Key: "p1", Name: "项目一", Workspace: t.TempDir()}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Activate("p1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	r := NewRegistry(store, "", "房主", "房主")
	cred := r.OwnerCredential()
	if cred == "" {
		t.Fatal("有房主 registry 应铸造统一凭证")
	}
	h, err := r.Hub("p1")
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	tok, live := h.SeatHolderToken("房主")
	if !live {
		t.Fatal("实例化房间的房主席应在座")
	}
	if tok != cred {
		t.Fatalf("预席房主席应持统一凭证：got %q want %q", tok, cred)
	}
	// 重实例化（卸载再开）后仍同源
	if err := r.Unload("p1"); err != nil {
		t.Fatalf("unload: %v", err)
	}
	h2, err := r.Hub("p1")
	if err != nil {
		t.Fatalf("re-hub: %v", err)
	}
	tok2, live2 := h2.SeatHolderToken("房主")
	if !live2 || tok2 != cred {
		t.Fatalf("重实例化后预席仍应持统一凭证：live=%v tok=%q", live2, tok2)
	}
}

func TestRegistryOwnerCredentialEmptyWithoutOwner(t *testing.T) {
	store, err := projects.Open("")
	if err != nil {
		t.Fatalf("projects.Open: %v", err)
	}
	r := NewRegistry(store, "", "", "")
	if r.OwnerCredential() != "" {
		t.Fatal("无房主 registry（裸内嵌）凭证应为空——门自然解除武装")
	}
}
