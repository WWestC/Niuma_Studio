//go:build !darwin

package shell

// UserNotice is the darwin-only OS banner (see notice_darwin.go); other
// platforms have no equivalent without a window, so it is a no-op.
func UserNotice(title, body string) {}
