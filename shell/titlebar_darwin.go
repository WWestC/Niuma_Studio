//go:build darwin

package shell

/*
#cgo darwin LDFLAGS: -framework AppKit
void shellBlendTitlebar(void *window);
void shellSetTitlebarColor(void *window, double r, double g, double b);
void shellSetHoodPassZone(double x, double w);
*/
import "C"
import "unsafe"

// blendTitlebar hands the NSWindow to the ObjC helper (titlebar_darwin.m):
// transparent titlebar, hidden title text, no separator line.
func blendTitlebar(window unsafe.Pointer) {
	C.shellBlendTitlebar(window)
}

// setTitlebarColor repaints the window background — the color a
// transparent titlebar shows. Called from the main thread only
// (the webview binding hops there via Dispatch).
func setTitlebarColor(window unsafe.Pointer, r, g, b float64) {
	C.shellSetTitlebarColor(window, C.double(r), C.double(g), C.double(b))
}

// setHoodPassZone arms the titlebar hood's click-passthrough range (the
// page's toolbar cluster; w<=0 disarms). Main thread only — the binding
// hops there via Dispatch.
func setHoodPassZone(x, w float64) {
	C.shellSetHoodPassZone(C.double(x), C.double(w))
}
