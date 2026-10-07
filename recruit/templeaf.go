package recruit

// templeaf.go — 成员名 → 临时文件叶子的消毒（安全核查修复）：名字
// 源头是 staffing 的员工名（最终可追溯到 LLM 自己跑的 birth），未消
// 毒时 'dh_recall_../x.txt' 能把入职提示词写出 TempDir。折叠规则与
// dispatch.inboxFileSafe / chat.termFileLeaf 同款：斜杠朋友与点连点
// 变 '_'，折叠改动过名字就补一段短摘要——两个敌意名字不共享文件；
// 干净名字（含中文）逐字节不变。

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// SafeTempLeaf renders name as one path-safe leaf with the given
// prefix and extension (e.g. ("dh_recall_", "小牛", ".txt")).
func SafeTempLeaf(prefix, name, ext string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '\x00' || r == '\n' || r == '\r':
			b.WriteByte('_')
		case r == '.' && (b.Len() == 0 || strings.HasSuffix(b.String(), ".")):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	leaf := b.String()
	changed := leaf != name
	if utf8.RuneCountInString(leaf) > 96 {
		leaf = string([]rune(leaf)[:96])
		changed = true
	}
	if leaf == "" {
		leaf, changed = "_", true
	}
	if changed {
		sum := sha256.Sum256([]byte(name))
		leaf += "-" + hex.EncodeToString(sum[:])[:8]
	}
	return prefix + leaf + ext
}
