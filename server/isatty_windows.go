//go:build windows

package server

import "os"

// stdinIsInteractive on Windows keeps the character-device marker: the
// /dev/null bypass is a unix shape (windows redirects use NUL, whose
// ModeCharDevice behavior differs); the ARB-1 guard's strictness here
// rides on the force flag instead (v0.6 §13-6's platform note).
func stdinIsInteractive() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
