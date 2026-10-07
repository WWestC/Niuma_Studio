package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 文档纪律护栏（正文见 CONTRIBUTING.md「文档纪律」）：规格与叙事分离、
// 地图锚点不缺——整理一次烂一次的教训，靠测试钉死。

func TestArchitectureStaysSpec(t *testing.T) {
	b, err := os.ReadFile("ARCHITECTURE.md")
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(string(b), "~~"); i >= 0 {
		t.Fatalf("ARCHITECTURE.md 出现划线尾迹（offset %d）：旧状→现状的叙事属于 docs/history.md，本文件只记现状", i)
	}
}

func TestDocsMapAnchors(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("docs", "README.md"))
	if err != nil {
		t.Fatalf("docs/README.md（文档地图）缺失: %v", err)
	}
	for _, anchor := range []string{
		"ARCHITECTURE.md", "docs/history.md", "kb/manual.md",
	} {
		if !strings.Contains(string(b), anchor) {
			t.Errorf("docs/README.md 文档地图缺锚点 %s", anchor)
		}
		if _, err := os.Stat(anchor); err != nil {
			t.Errorf("地图锚点 %s 指向的文件不存在: %v", anchor, err)
		}
	}
}
