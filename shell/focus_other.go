//go:build !darwin

package shell

// FocusApp is a darwin nicety (System Events frontmost switch);
// elsewhere the relaunch path just prints the running room's address.
func FocusApp(port int) {}
