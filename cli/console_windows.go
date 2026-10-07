//go:build windows

package cli

import "golang.org/x/sys/windows"

// init switches the console output code page to UTF-8 so Chinese
// message text echoes correctly in default (GBK) Windows consoles.
func init() {
	_ = windows.SetConsoleOutputCP(65001)
}
