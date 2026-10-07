//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// -H windowsgui（build.bat / build.sh / release.yml 三路同接）让双击
// 启动不再弹出终端——但 GUI 子系统进程默认拿不到任何标准流，直接
// 后果有二：双击时 boot 日志无处可写；人在 cmd/PowerShell 里敲
// niuma.exe task list 也看不到输出。本文件在 main 包 init 里把两头
// 都接回来，三种情形各归其位：
//
//   - 标准流已是有效句柄（调度器/外部会话的管道、终端重定向、go run
//     的控制台子系统构建、go test）——原样保留，一字不动；
//   - 标准流无效但父进程有控制台（人在 cmd/PowerShell 里交互运行）：
//     AttachConsole 附着父控制台，把缺失的流重开到 CONOUT$/CONIN$
//     绑回 os.Stdout/os.Stderr/os.Stdin——命令行用法照常有来有回
//     （listen 子命令交互读 stdin，见 cli/listen.go）。cmd 对 GUI 程
//     序不等待，提示符可能先于输出回来——这是 Windows 的固有行为
//     （wt.exe 同款），不是丢输出；
//   - 两头皆无（资源管理器双击）：保持沉默——没有窗口也没有日志，
//     boot 日志落 %TEMP%\niuma-boot.log（每次启动截断重写，只保留
//     最近一次），顶替原终端的日志面板角色，出问题时拿它排障。
//
// log 包的默认 logger 在它自己的 init 里就把 os.Stderr 抄走了，改绑
// 必须跟着 log.SetOutput，否则 stderr 的重定向只对 fmt 生效。
func init() {
	outOK, errOK, inOK := streamOK(os.Stdout), streamOK(os.Stderr), streamOK(os.Stdin)
	if outOK && errOK {
		return
	}
	// ^uintptr(0) = ATTACH_PARENT_PROCESS：附着父进程的控制台（若它
	// 没有——双击启动的父进程是 explorer——Call 返回 0 即失败）。
	attach := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := attach.Call(^uintptr(0)); r != 0 {
		if !outOK || !errOK {
			if con, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
				if !outOK {
					os.Stdout = con
				}
				if !errOK {
					os.Stderr = con
					log.SetOutput(con)
				}
			}
		}
		if !inOK {
			if con, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
				os.Stdin = con
			}
		}
		return
	}
	if !errOK {
		if f, err := os.OpenFile(filepath.Join(os.TempDir(), "niuma-boot.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644); err == nil {
			log.SetOutput(f)
		}
	}
}

// streamOK mirrors server/isatty_windows.go's Stat probe: a valid pipe,
// file, NUL or console handle stats fine; the NULL handles a windowsgui
// process gets on an interactive/double-click launch do not.
func streamOK(f *os.File) bool {
	_, err := f.Stat()
	return err == nil
}
