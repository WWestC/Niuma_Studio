package util

// guard_test.go — the fence's own contract: a panicking body returns
// normally (the log line is the trace, not a crash), a clean body runs
// untouched, and LogPanic itself never panics on exotic panic values.

import "testing"

func TestGuardFencesPanic(t *testing.T) {
	ran := false
	Guard("test: fence", func() { ran = true })
	if !ran {
		t.Fatal("干净体应照常执行")
	}
	// The fence itself must return, not crash the test binary — this
	// line running IS the assertion.
	Guard("test: fence", func() { panic("观察者的 bug") })
	Guard("test: fence", func() { panic(nil) }) // 无值 panic 同样被吃
}

func TestLogPanicNeverPanics(t *testing.T) {
	LogPanic("test: log", "字符串值")
	LogPanic("test: log", 42)
	LogPanic("test: log", nil)
}
