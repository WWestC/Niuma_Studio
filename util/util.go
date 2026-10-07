// Package util is the repo's cross-package micro-helper home (the dsh
// zero-dependency util-group discipline, 范式重构 S9): one home per
// fact — the string/int/time twins that used to live as private copies
// in tasks/cli/ui now have exactly one implementation. Leaf by
// construction: stdlib only, imports nothing from this module
// (the one exception: util/wsbinding.go's atomic save goes through the
// persist leaf, which is itself stdlib-only).
package util

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// FirstLine returns the prefix before the first newline (the whole
// string when there is none). NOTE: this is the "first line" of a
// note, not its first non-blank line — recruit's output parser has the
// other semantics and keeps its own helper.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Contains reports whether the list holds the exact string.
func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ClampInt pins v into [lo, hi].
func ClampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Now is the repo's single timestamp idiom (Unix seconds).
// Now 是全局时钟。测试缝＝SetNow（r_16 t_179：停滞检查的时基——测试
// 拨快 20 分钟无需真等）。生产路径不变（time.Now）。缝必须原子交换：
// 读 Now 的多为 hub 常驻 goroutine，它们跨测试存活，裸赋值（旧的
// `util.Now = ...` 写法）会和正在打时间戳的 goroutine 数据竞态。
var Now = func() int64 { return (*nowFn.Load())() }

var nowFn atomic.Pointer[func() int64]

func init() {
	f := func() int64 { return time.Now().Unix() }
	nowFn.Store(&f)
}

// SetNow swaps the clock seam (tests only — production never calls it).
// f 必须是普通时钟函数：把 Now 自己（外层原子读包装器）存回缝里会
// 无限递归——保存/恢复请用 NowFn 取目标函数。
func SetNow(f func() int64) {
	nowFn.Store(&f)
}

// NowFn returns the seam's current target — the save/restore face for
// tests that stub the clock and put it back afterwards.
func NowFn() func() int64 {
	return *nowFn.Load()
}

// ClockTime renders a Unix-seconds stamp as local HH:MM — the 排队说明
// header's 发于 slot. Hour:minute is the honest resolution for a chat
// line's age: a second-precision stamp implies a punctuality nobody
// promised, and a date would only matter past midnight (where the local
// clock still disambiguates better than an ISO date ever would in a
// bubble).
func ClockTime(ts int64) string {
	if ts <= 0 {
		return "?"
	}
	return time.Unix(ts, 0).Format("15:04")
}

// HumanWait renders a duration in seconds as the coarse Chinese a room
// reads naturally: 整分 rounds down to 「N 分钟」, sub-minute waits to
// 「N 秒」. The 排队说明's 等了约 slot only needs order-of-magnitude
// honesty — false precision would invite exactly the clock-watching the
// stamp exists to prevent.
func HumanWait(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	return fmt.Sprintf("%d 分钟", seconds/60)
}

// Env resolves the NIUMA_<name> environment variable with the
// pre-rename DH_<name> spelling as fallback: the DH_ form keeps every
// existing launch script and shell export working unchanged, the
// NIUMA_ form (set to a non-empty value) always wins.
func Env(name string) string {
	if v := os.Getenv("NIUMA_" + name); strings.TrimSpace(v) != "" {
		return v
	}
	return os.Getenv("DH_" + name)
}
