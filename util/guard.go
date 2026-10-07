package util

// guard.go — goroutine 边界的 panic 隔离。本应用是一个进程一间工作
// 室：房间、调度舰队、成员会话桥全部同生共死，任何一条后台循环里的
// 一次 panic 都会带走整个进程——这与「坏历史文件降级为空、持久化绝不
// 拒绝启动」的既有哲学相悖。Guard/LogPanic 把 panic 变成一行带栈的
// 日志：被守的循环丢掉这一拍/这一批，继续服务下一拍。
//
// 带栅栏（done 通道）的 flusher 不能直接套 Guard——恢复路径必须先把
// 欠下的栅栏关掉，否则排水方（FlushHistory/FlushInbox 的等待者）会
// 永远等下去：那些循环手写自己的 recover，只复用 LogPanic 的日志
// 尾巴。

import (
	"log"
	"runtime/debug"
)

// LogPanic logs one recovered panic with its stack — the shared tail
// of Guard and the hand-rolled flusher recovers.
func LogPanic(scope string, r any) {
	log.Printf("%s: panic 已隔离（进程照活，本拍/本批已弃）：%v\n%s", scope, r, debug.Stack())
}

// Guard runs fn with a panic fence: a panic becomes one logged line
// instead of a dead goroutine. For tick loops (clocks, keepers,
// watchers) and one-shot workers — the guarded body is one beat; the
// loop ticks on. It is deliberately NOT for call paths that need the
// panic's failure to propagate: the fence swallows, it does not
// convert into an error.
func Guard(scope string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			LogPanic(scope, r)
		}
	}()
	fn()
}
