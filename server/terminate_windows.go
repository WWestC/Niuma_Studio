//go:build windows

package server

import "os"

// gracefulTerminate is a no-op on Windows: there is no SIGTERM, so a
// takeover degrades straight to the hard-Kill path (v0.6 §13-6).
func gracefulTerminate(p *os.Process) bool {
	return false
}
