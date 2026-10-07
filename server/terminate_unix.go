//go:build !windows

package server

import (
	"os"
	"syscall"
)

// gracefulTerminate asks the old instance to exit gracefully — the
// v0.6 takeover notice path (SIGTERM: announce, wait, close). Returns
// false when the polite signal could not be delivered, in which case
// the caller's hard-Kill fallback applies.
func gracefulTerminate(p *os.Process) bool {
	return p.Signal(syscall.SIGTERM) == nil
}
