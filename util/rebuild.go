package util

// rebuild.go — 重新编译并重启的编译半场（设置卡「重新编译并重启」）。
// 从运行中的可执行文件出发找回本仓库的源码根（exe 同目录、.app 包外
// 三级、进程 cwd 三处候选，go.mod 的 module 行验明正身——光有 go.mod
// 不够，落地目录里可能有别人的），跑与 build.sh 同规的 go build 到
// 同目录临时文件，再原子换位到本程序名下：
//
//   unix：rename 只换目录项，运行中的旧进程抱住旧 inode 继续跑，
//         新进程下次启动拿到的就是新件——换位零感知；
//   windows：运行中的 exe 锁着不许覆盖、但许改名——旧件挪 .old 让
//         位（下一轮编译顺手清），新件落原名，失败即还原。
//
// 编译失败绝不动旧二进制一根毫毛：错误连同编译器输出（截尾 8KB）一
// 路带回给设置卡亮出来，应用原地照常服务。重启交接（收摊→落盘→拉
// 替身）住在 main 的 rebuildCh 编排里，与工厂重置共用同一条纪律。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/WWestC/Niuma_Studio/i18n"
)

// niumaModule 是本仓库 go.mod 的 module 行——源码目录的身份证。
const niumaModule = "github.com/WWestC/Niuma_Studio"

// rebuildTimeout 兜住一次编译的墙钟：热缓存几秒、冷缓存（清缓存/换
// 机器）也就几分钟，10 分钟还烧不完就该怀疑机器过载而非仓库太大。
const rebuildTimeout = 10 * time.Minute

// goInstallCandidates 是 shell PATH 之外按序探测的 Go 安装位。GUI
// 双击启动的进程只拿 LaunchServices 的最小 PATH（/usr/bin:/bin:
// /usr/sbin:/sbin）——终端里装得好好的 Go 不在其中，得挨个敲门。
var goInstallCandidates = []string{
	"/usr/local/go/bin/go", // 官方安装器（macOS/Linux 默认位）
	"/opt/homebrew/bin/go", // Homebrew（Apple Silicon）
	"/usr/local/bin/go",    // Homebrew（Intel）或手动放的软链
}

// findGoTool locates the go command: PATH first (终端启动一切如常)，
// then the common install spots above (双击 .app 的最小 PATH 兜底)。
// 绝对路径直接可用——go 命令自会从自身位置推出 GOROOT，无需环境
// 变量扶持。
func findGoTool() (string, error) {
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	if p := firstExisting(goInstallCandidates); p != "" {
		return p, nil
	}
	return "", errors.New(i18n.Sf(
		"未找到 Go 工具链——PATH 里没有 go，常见安装位（%s）也未命中；请安装 Go（https://go.dev/dl/ 或 brew install go）后重试",
		strings.Join(goInstallCandidates, "、")))
}

// firstExisting returns the first path that exists as a plain file
// ("" when none does).
func firstExisting(paths []string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// SourceRoot locates the source tree this running binary belongs to.
// Candidates, in order: the executable's directory (./build.sh 就地落
// 的 niuma), three levels up from it (macOS .app 包外——build.sh 在仓库
// 根就地组包，Niuma_Studio.app/Contents/MacOS 上三级正是仓库根)，and
// the process working directory (go run ./开发态——可执行文件落在临时
// 构建目录，源码根在启动时的 cwd)。A candidate qualifies only when its
// go.mod declares our module AND main.go sits beside it.
func SourceRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.S("解析自身路径失败"), err)
	}
	dir := filepath.Dir(exe)
	candidates := []string{dir}
	for i := 0; i < 3; i++ { // .app/Contents/MacOS → 仓库根
		dir = filepath.Dir(dir)
		candidates = append(candidates, dir)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	return sourceRootFrom(candidates)
}

// sourceRootFrom is SourceRoot's testable core: the first candidate
// that verifies as this repo wins.
func sourceRootFrom(candidates []string) (string, error) {
	for _, dir := range candidates {
		if isNiumaSource(dir) {
			return dir, nil
		}
	}
	return "", errors.New(i18n.S("未找到本应用的源码目录——需要从源码仓库（含 go.mod 与 main.go）构建出的程序里发起，分发的裸应用无法自行编译"))
}

// isNiumaSource reports whether dir is this project's source root: a
// go.mod whose module line is ours, with main.go beside it.
func isNiumaSource(dir string) bool {
	if dir == "" {
		return false
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	if moduleLine(string(mod)) != niumaModule {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "main.go"))
	return err == nil
}

// moduleLine pulls the `module ...` directive out of a go.mod body
// ("" when absent).
func moduleLine(mod string) string {
	for _, ln := range strings.Split(mod, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "module "))
		}
	}
	return ""
}

// RebuildSelf compiles the source tree into a sibling temp file and
// atomically swaps it over this running executable, returning how long
// the build took. The old binary keeps serving untouched on failure —
// a bad tree never replaces a good process.
func RebuildSelf() (time.Duration, error) {
	goBin, err := findGoTool()
	if err != nil {
		return 0, err
	}
	root, err := SourceRoot()
	if err != nil {
		return 0, err
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", i18n.S("解析自身路径失败"), err)
	}
	// 临时件必须与 exe 同目录：跨文件系统的 rename 不是原子的。
	tmp := exe + ".rebuild"

	t0 := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), rebuildTimeout)
	defer cancel()
	cmd := HideConsole(exec.CommandContext(ctx, goBin, "build", "-trimpath", "-o", tmp, "."))
	cmd.Dir = root
	cmd.Env = buildEnv(os.Environ(), filepath.Dir(goBin))
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		if ctx.Err() == context.DeadlineExceeded {
			return 0, errors.New(i18n.Sf("编译超时（%s）——冷缓存首次编译太慢或机器过载，稍后再试", rebuildTimeout))
		}
		return 0, errors.New(i18n.Sf("编译失败：\n%s", tailOutput(out)))
	}
	if err := swapBinary(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return time.Since(t0), nil
}

// buildEnv shapes the build subprocess's environment from base
// (normally os.Environ()): CGO_ENABLED forced on — webview 走 cgo，
// 与 build.sh 同规（不信任外层环境里可能关着它的残留值）—— and,
// when the go tool was found outside PATH（双击启动的最小 PATH 兜
// 底）, its directory prepended so the toolchain stays reachable for
// the go command's children.
func buildEnv(base []string, goDir string) []string {
	env := make([]string, 0, len(base)+2)
	sawPath := false
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "CGO_ENABLED="):
			continue // 后写的才作数：先摘残留，再钉我们的
		case goDir != "" && strings.HasPrefix(kv, "PATH="):
			sawPath = true
			kv = "PATH=" + goDir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	env = append(env, "CGO_ENABLED=1")
	if goDir != "" && !sawPath {
		env = append(env, "PATH="+goDir)
	}
	return env
}

// swapBinary moves the fresh build over the running executable's path.
func swapBinary(tmp, exe string) error {
	if runtime.GOOS == "windows" {
		// Windows 锁着运行中的 exe 不许覆盖、但许改名：旧件挪 .old
		// 让位（下一轮编译开头顺手清掉），新件落原名；新件落败立刻
		// 把旧件挪回去，不留一个启动不了的空位。
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return fmt.Errorf("%s: %w", i18n.S("挪开旧程序失败（Windows）"), err)
		}
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Rename(old, exe)
			return fmt.Errorf("%s: %w", i18n.S("新程序就位失败（Windows）"), err)
		}
		return nil
	}
	// unix：rename 原子换掉目录项——运行中的旧进程抱住旧 inode 跑到
	// 自己退出为止，新进程一起就是新件。
	if err := os.Rename(tmp, exe); err != nil {
		return fmt.Errorf("%s: %w", i18n.S("新程序换位失败"), err)
	}
	// darwin 收尾：.app 里的可执行件被换位后 bundle 的签名封印就破了
	// ——重签之后通知授权链在 rebuild 之后才算完整（见 resign_darwin.go）。
	resignAppBundle(exe)
	return nil
}

// tailOutput shapes compiler output for the settings card: trimmed,
// and when it overflows 8KB only the tail rides (Go 的报错总在最后)。
func tailOutput(out []byte) string {
	const capBytes = 8 << 10
	s := strings.TrimSpace(string(out))
	if s == "" {
		return i18n.S("（编译器无输出——见终端日志）")
	}
	if len(s) > capBytes {
		return i18n.S("……（前文截断）\n") + s[len(s)-capBytes:]
	}
	return s
}
