package projects

// gitsettings_test.go — 两级门顶层的存储面钉子（SetGitSettings）：落账、
// 归档只读、基线名校验、全空值不物化计划、InitIfMissing 不被触碰。

import (
	"strings"
	"testing"
)

func TestSetGitSettings(t *testing.T) {
	s := newTestStore(t)
	// 落账：总开关＋基线＋自动建枝一次写全。
	p, err := s.SetGitSettings("alpha", true, "dev", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.GitDisabled || p.GitPlan == nil || p.GitPlan.BaseBranch != "dev" || !p.GitPlan.AutoSeatBranch {
		t.Fatalf("设置落库不合预期: disabled=%v plan=%+v", p.GitDisabled, p.GitPlan)
	}
	// 既有计划可改（关自动建枝也要落账）；InitIfMissing 原样保留。
	_, err = s.SetGitSettings("alpha", true, "dev", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get2(t); got.GitPlan == nil || got.GitPlan.AutoSeatBranch {
		t.Fatalf("自动建枝应可关: %+v", got.GitPlan)
	}
	// 全空值不物化新计划——nil=未配置 的账面语义留给没碰过设置面的项目。
	s2 := newTestStore(t)
	if p2, err := s2.SetGitSettings("alpha", false, "", false, false); err != nil || p2.GitPlan != nil {
		t.Fatalf("全空值不应物化计划: %v %+v", err, p2.GitPlan)
	}
}

func TestSetGitSettingsRefusals(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SetGitSettings("alpha", false, "bad..name", true, false); err == nil || !strings.Contains(err.Error(), "基线分支名") {
		t.Fatalf("非法基线名应被拒: %v", err)
	}
	if _, err := s.SetGitSettings("nope", false, "", false, false); err == nil || !strings.Contains(err.Error(), "项目不存在") {
		t.Fatalf("不存在项目应被拒: %v", err)
	}
	if _, err := s.Activate("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetGitSettings("alpha", true, "", false, true); err == nil || !strings.Contains(err.Error(), "已归档") {
		t.Fatalf("归档后改设置应被拒: %v", err)
	}
}
