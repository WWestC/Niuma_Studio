// projects/gitlinks_test.go — 绑定模型的写入纪律：形状校验、整条替
// 换、幂等解绑、归档只读、浅拷贝不串账。
package projects

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/WWestC/Niuma_Studio/vcs"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create(Project{
		Key:       "alpha",
		Name:      "甲项目",
		Workspace: filepath.Join(t.TempDir(), "alpha"),
		Versions:  []Version{{Name: "v1"}, {Name: "v2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBindBranch(t *testing.T) {
	s := newTestStore(t)
	p, err := s.BindBranch("alpha", "feat-x", BranchLink{Req: "r_12", Version: "v1", BoundBy: "房主"})
	if err != nil {
		t.Fatal(err)
	}
	bl := p.Link("feat-x")
	if bl == nil || bl.Req != "r_12" || bl.Version != "v1" || bl.BoundTS == 0 || bl.BoundBy != "房主" {
		t.Fatalf("绑定落库不合预期: %+v", bl)
	}

	// 整条替换：去掉需求只留版本。
	p, err = s.BindBranch("alpha", "feat-x", BranchLink{Version: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if bl := p.Link("feat-x"); bl == nil || bl.Req != "" || bl.Version != "v2" || bl.BoundTS == 0 {
		t.Fatalf("重绑应整条替换: %+v", bl)
	}

	// 兼容旧读取：Get 拿到的实体也能查绑定。
	if got := s.Get2(t).Link("feat-x"); got == nil || got.Version != "v2" {
		t.Fatalf("Get 侧应看见绑定: %+v", got)
	}
}

// Get2 是测试捷径：拿回 alpha 的当前实体。
func (s *Store) Get2(t *testing.T) Project {
	t.Helper()
	p, ok := s.Get("alpha")
	if !ok {
		t.Fatal("alpha 不见了")
	}
	return p
}

func TestBindBranchRefusals(t *testing.T) {
	s := newTestStore(t)
	cases := []struct {
		name   string
		branch string
		link   BranchLink
		want   string
	}{
		{"空绑定", "feat-x", BranchLink{}, "绑定内容为空"},
		{"非法分支名", "-force", BranchLink{Req: "r_1"}, "非法分支名"},
		{"需求号形状", "feat-x", BranchLink{Req: "12号"}, "需求号须形如"},
		{"版本不存在", "feat-x", BranchLink{Version: "v9"}, "版本列表里"},
		{"项目不存在", "feat-x", BranchLink{Req: "r_1"}, "项目不存在"},
	}
	for _, c := range cases {
		var err error
		if c.name == "项目不存在" {
			_, err = s.BindBranch("ghost", c.branch, c.link)
		} else {
			_, err = s.BindBranch("alpha", c.branch, c.link)
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：期望拒绝（%q），得到 %v", c.name, c.want, err)
		}
	}
	// 备注超长夹断不拒绝。
	p, err := s.BindBranch("alpha", "feat-x", BranchLink{Note: strings.Repeat("长", 500)})
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(p.Link("feat-x").Note)); got != maxNoteRunes {
		t.Fatalf("备注应夹断到 %d rune，得到 %d", maxNoteRunes, got)
	}
}

func TestUnbindBranch(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.BindBranch("alpha", "feat-x", BranchLink{Req: "r_1"}); err != nil {
		t.Fatal(err)
	}
	// 幂等：先解不存在的、再解存在的、再解一次都成功。
	for i := 0; i < 3; i++ {
		if _, err := s.UnbindBranch("alpha", "feat-x"); err != nil {
			t.Fatalf("第 %d 次解绑不该报错: %v", i+1, err)
		}
	}
	if p := s.Get2(t); p.BranchLinks != nil {
		t.Fatalf("解绑归零应收走 map，得到 %#v", p.BranchLinks)
	}
}

func TestGitLinksArchivedReadonly(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Activate("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindBranch("alpha", "feat-x", BranchLink{Req: "r_1"}); err == nil || !strings.Contains(err.Error(), "已归档") {
		t.Fatalf("归档后绑定应被拒: %v", err)
	}
	if _, err := s.UnbindBranch("alpha", "feat-x"); err == nil || !strings.Contains(err.Error(), "已归档") {
		t.Fatalf("归档后解绑应被拒: %v", err)
	}
	if _, err := s.SetGitPolicy("alpha", &vcs.Policy{}); err == nil || !strings.Contains(err.Error(), "已归档") {
		t.Fatalf("归档后改策略应被拒: %v", err)
	}
}

func TestSetGitPolicy(t *testing.T) {
	s := newTestStore(t)
	p, err := s.SetGitPolicy("alpha", &vcs.Policy{CommitTypes: []string{"feat", "修复"}, RequireRef: "task"})
	if err == nil {
		t.Fatal("中文 type 名应被拒")
	}
	p, err = s.SetGitPolicy("alpha", &vcs.Policy{CommitTypes: []string{"feat", "fix"}, RequireRef: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if p.GitPolicy == nil || p.GitPolicy.RequireRefEffective() != vcs.RefTask {
		t.Fatalf("策略落库不合预期: %+v", p.GitPolicy)
	}
	// 引用强制档非法值拒绝；nil 重置回默认。
	if _, err := s.SetGitPolicy("alpha", &vcs.Policy{RequireRef: "随便"}); err == nil {
		t.Fatal("非法 require_ref 应被拒")
	}
	if p, err = s.SetGitPolicy("alpha", nil); err != nil || p.GitPolicy != nil {
		t.Fatalf("nil 应重置策略: %v %+v", err, p.GitPolicy)
	}
}

func TestBindBranchShallowCopySafety(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.BindBranch("alpha", "feat-x", BranchLink{Req: "r_1"}); err != nil {
		t.Fatal(err)
	}
	// 拿到的实体改 map 不许串进账本。
	p := s.Get2(t)
	p.BranchLinks["feat-x"] = BranchLink{Note: "偷改"}
	p.BranchLinks["hack"] = BranchLink{Note: "偷加"}
	if got := s.Get2(t).Link("feat-x"); got.Note != "" || got.Req != "r_1" {
		t.Fatalf("外部改写串进了账本: %+v", got)
	}
	if s.Get2(t).Link("hack") != nil {
		t.Fatal("外部加条目串进了账本")
	}
}
