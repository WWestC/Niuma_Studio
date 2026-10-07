package vcs

// gitboot_test.go — 立项引导原语（v2.14）的行为面：InitRepo 新仓落基线
// 分支＋一笔空根提交（老 git 无 -b 也能用 symbolic-ref 路线）；在别人
// 的仓库里（含子目录）一律拒绝建仓；EnsureBranch 幂等只建不切——用户
// 在用的工作树一个分支都不动。

import (
	"context"
	"strings"
	"testing"
)

func TestInitRepoCreatesBaseWithRootCommit(t *testing.T) {
	needGit(t)
	dir := t.TempDir() + "/ws" // 不存在：连目录一起建
	repo, err := InitRepo(context.Background(), dir, "mainline", "")
	if err != nil {
		t.Fatalf("InitRepo: %v", err)
	}
	if !samePath(repo.Root, dir) { // macOS 的 /var ↔ /private/var：按真实路径对齐
		t.Fatalf("仓库根应在 %s，实为 %s", dir, repo.Root)
	}
	st, err := repo.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "mainline" {
		t.Fatalf("初始分支应为 mainline，实为 %q", st.Branch)
	}
	msgs, err := repo.LogMessages(context.Background(), "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "项目初始化") {
		t.Fatalf("应恰有一笔空根提交（项目初始化），实 %d 笔：%+v", len(msgs), msgs)
	}
	// 幂等反面：同一目录二次 InitRepo 必须拒绝（已是仓库）。
	if _, err := InitRepo(context.Background(), dir, "other", ""); err == nil || !strings.Contains(err.Error(), "不在别人的仓库里建仓") {
		t.Fatalf("二次建仓应拒绝，得 %v", err)
	}
}

func TestInitRepoRefusesInsideExistingRepo(t *testing.T) {
	needGit(t)
	outer := newRepo(t)
	sub := outer + "/subproj"
	if _, err := InitRepo(context.Background(), sub, "main", ""); err == nil || !strings.Contains(err.Error(), "不在别人的仓库里建仓") {
		t.Fatalf("外层仓库的子目录里建仓应拒绝，得 %v", err)
	}
	// 外层仓库纹丝不动：分支还是那两根，没有多余提交。
	repo, err := Detect(context.Background(), outer)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := repo.Branches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, b := range bs {
		names[b.Name] = true
	}
	if !names["main"] || len(bs) != 1 {
		t.Fatalf("外层仓库被动了：分支 %+v", bs)
	}
}

func TestEnsureBranchIdempotentNoSwitch(t *testing.T) {
	needGit(t)
	dir := newRepo(t)
	repo, err := Detect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 已存在：幂等成功、不重建。
	created, err := repo.EnsureBranch(ctx, "main", "")
	if err != nil || created {
		t.Fatalf("已存在的分支应幂等跳过（created=%v, err=%v）", created, err)
	}
	// 缺失：自 main 切出；当前分支不动（没切走）。
	created, err = repo.EnsureBranch(ctx, "dev-line", "main")
	if err != nil || !created {
		t.Fatalf("缺失分支应新建（created=%v, err=%v）", created, err)
	}
	st, err := repo.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" {
		t.Fatalf("EnsureBranch 不得切走工作树（现驻 %s）", st.Branch)
	}
	found := false
	if bs, err := repo.Branches(ctx); err == nil {
		for _, b := range bs {
			if b.Name == "dev-line" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("dev-line 应已在分支表里")
	}
}
