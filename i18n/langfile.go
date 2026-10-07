package i18n

// langfile.go — 语言设置的落盘面（~/.niuma/lang.json，reveal.json 同款
// 纪律：tmp＋rename 原子换位，0600 只归本用户）。读侧宽松：文件缺席
// 或损坏一律回落空串（＝中文），让 boot 与 /lang GET 各自决定默认值。

import (
	"encoding/json"
	"os"

	"github.com/WWestC/Niuma_Studio/persist"
)

type langFile struct {
	Lang string `json:"lang"`
}

// LoadLang reads the persisted language ("zh"/"en"), or "" when absent,
// unreadable, or invalid — callers treat that as "unset, default zh".
func LoadLang(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var f langFile
	if err := json.Unmarshal(b, &f); err != nil {
		return ""
	}
	if f.Lang != "zh" && f.Lang != "en" {
		return ""
	}
	return f.Lang
}

// SaveLang persists the language atomically (persist.Save).
func SaveLang(path, l string) error {
	if l != "zh" && l != "en" {
		l = "zh"
	}
	b, err := json.Marshal(langFile{Lang: l})
	if err != nil {
		return err
	}
	return persist.Save(path, b, 0o600)
}
