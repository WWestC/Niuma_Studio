package plugins

// seed.go — 出厂预装：宿主把内嵌的官方插件（niuma.market 市场本体）
// 铺进插件根。只在首启（启用态文件尚未存在）做一次——之后这台机器
// 的插件目录就是用户的领地：删了不再回来，升级走市场自己。

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FirstRun reports whether the enable-state file has never been written
// — the one-time factory signal main gates the seeding on.
func FirstRun(statePath string) bool {
	if statePath == "" {
		return false
	}
	_, err := os.Stat(statePath)
	return err != nil
}

// SeedPlugin copies one embedded plugin tree (plugin.json at its FS
// root) into <root>/<id>. An existing target is left untouched — the
// user's install always wins over the factory copy.
func SeedPlugin(fsys fs.FS, id, root string) error {
	if fsys == nil || root == "" || !ValidID(id) {
		return nil
	}
	dst := filepath.Join(root, id)
	if _, err := os.Stat(dst); err == nil {
		return nil // 已在（用户装过/预装过）：不覆盖
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, p)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := fsys.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
