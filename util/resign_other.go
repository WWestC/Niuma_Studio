//go:build !darwin

package util

// resignAppBundle is the darwin-only re-seal after an in-place binary
// swap (see resign_darwin.go); other platforms have no bundle seal to
// break, so there is nothing to redo.
func resignAppBundle(string) {}
