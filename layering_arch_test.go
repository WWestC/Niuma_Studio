package main

// layering_arch_test.go — 分层格子的白名单护栏（组合根持有）：把
// ARCHITECTURE.md 的分层图整张变成测试。wire 与 chat 各自的 arch_test
// 钉它们那两层；这一份钉其余各层——叶子（只许引叶行兄弟）、档案/桥
// （叶子 + wire）、基座（叶子 + 档案行）、领域（叶子 + wire）。
// 白名单制是刻意的：黑名单认不出「新出现的包」，下一个 kb 式破口就
// 能溜进来——白名单下任何新依赖都必须在这里显式加行，加了行就是一次
// 可评审的分层决策。

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leafDeps is the leaf row's own mutual set: the five zero-business
// packages may reference each other (i18n saves through persist, util
// speaks i18n) but nothing above them. persist itself is pure.
var leafDeps = map[string]bool{
	"/i18n": true, "/persist": true, "/util": true, "/vcs": true, "/media": true,
}

// domainDeps is the domain row's whitelist: leaves plus the protocol
// contract — domains talk to each other through wire mirrors and
// consumer interfaces, never imports.
var domainDeps = map[string]bool{
	"/i18n": true, "/persist": true, "/projects": true, "/util": true, "/wire": true,
}

// layerWhitelist maps every guarded package to its allowed internal
// dependencies (ARCHITECTURE.md's layering diagram, one row per layer;
// the entries pin today's clean state — additions are layering
// decisions, not conveniences).
var layerWhitelist = map[string]map[string]bool{
	// 叶子行：兄弟互引可，向上不可。
	"persist": {},
	"util":    {"/i18n": true, "/persist": true},
	"i18n":    {"/persist": true},
	"vcs":     {"/i18n": true, "/util": true},
	"media":   {"/i18n": true, "/persist": true},
	// 档案/桥行：叶子 + wire。
	"agents": {"/persist": true, "/util": true, "/wire": true},
	"zcode":  {"/i18n": true, "/persist": true, "/util": true},
	// 基座行：叶子 + 档案行 + vcs。
	"projects":   {"/i18n": true, "/persist": true, "/util": true, "/vcs": true},
	"capability": {"/agents": true, "/i18n": true, "/persist": true, "/util": true},
	"staffing":   {"/i18n": true, "/persist": true, "/projects": true, "/util": true, "/vcs": true},
	// identity（身份层基座）：叶子即可——账户/凭据/会话不依赖任何档案
	// 或域包；持久化经 identity.Store 缝（sqlstore 实现在上层实现它）。
	"identity": {"/i18n": true},
	// 领域行：叶子 + wire（domainDeps 共用）。
	"tasks":        domainDeps,
	"requirements": domainDeps,
	"plan":         domainDeps,
	"merge":        domainDeps,
	"meeting":      domainDeps,
	"notice":       domainDeps,
	"kb":           domainDeps,
}

// TestLayeringImportsStayLow walks every non-test file of every guarded
// package and fails on the first import outside that package's
// whitelist.
func TestLayeringImportsStayLow(t *testing.T) {
	for pkg, allowed := range layerWhitelist {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("readdir %s: %v", pkg, err)
		}
		for _, ent := range entries {
			name := ent.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue // tests MAY import anything (fixtures, pin tests like kb/vocab_test.go)
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, filepath.Join(pkg, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("%s/%s: %v", pkg, name, err)
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if !strings.HasPrefix(path, "github.com/WWestC/Niuma_Studio") {
					continue // stdlib and external deps are none of this test's business
				}
				if !allowed[strings.TrimPrefix(path, "github.com/WWestC/Niuma_Studio")] {
					t.Fatalf("%s/%s imports %q — 该包的依赖白名单见 layering_arch_test.go"+
						"（分层格：叶子只引叶子，基座加档案行，领域只加 wire）。"+
						"领域之间经 wire 镜像与消费者接口说话，不经 import；"+
						"新依赖＝分层决策：先改白名单并成文，而不是绕过测试", pkg, name, path)
				}
			}
		}
	}
}
