#import <AppKit/AppKit.h>

// shellBlendTitlebar melts the native title bar into the app chrome,
// ZCode-style: full-size content — the page runs all the way up under
// a transparent title bar (title text and macOS 11's hairline gone),
// so the traffic lights float on the page's own sidebar. The window
// background is still painted (shellSetTitlebarColor) as the
// first-frame / resize-edge fallback — the page reports its theme
// color over the setChromeColor binding.
//
// The strip needs a hood: the webview core sets the WKWebView straight
// as the window's contentView, and under FullSizeContentView that view
// spans the whole window — hit-testing the top strip lands on the
// WKWebView, whose views answer mouseDownCanMoveWindow NO, so every
// press on the strip is eaten by the page and the window can't be
// moved at all (measured: hitTest@strip → WKWebView, canMove=0). So
// the WKWebView is wrapped in a plain container view and a transparent
// 28pt hood — as tall as the strip the page keeps clear
// (html[data-nativetb] --tb-h in web/index.html) — is laid over the
// top of it.
//
// The hood handles its own presses (mouseDownCanMoveWindow NO — the
// window's background-drag machinery must not hijack the event):
// double-click zooms the window like a native titlebar, a drag moves
// it with the classic nested event loop (press → pump
// nextEventMatchingMask until mouse-up, apply each event's cursor
// delta to the frame). The modern -performDragWithEvent: is gone on
// this OS, and this path is the long-standing replacement. The page
// fills the container and keeps every event below the hood; the
// traffic lights sit in the titlebar container above the content view
// and stay clickable.
//
// Toolbar passthrough (ZCode-style titlebar buttons): the page parks
// its ☰ / ← / → cluster in this strip right of the traffic lights,
// and reports its x-range over the setTitlebarPassZone binding. The
// hood's hitTest returns nil inside that range, so the press falls
// through to the WKWebView beneath and the buttons receive it; the
// rest of the strip stays drag. CSS px == points for the hit-test
// (the page lays out in points; only the backing store is 2x), so
// the page's getBoundingClientRect numbers map 1:1. Zone is x-only —
// it spans the hood's full height, which the strip already is.
static const CGFloat kShellHoodH = 28;
static double gShellPassX0 = 0, gShellPassW = 0; // w<=0 → off (no zone)

@interface ShellHoodView : NSView
@end
@implementation ShellHoodView
- (BOOL)mouseDownCanMoveWindow { return NO; }
- (NSView *)hitTest:(NSPoint)point {
	// 覆盖 super 的命中：穿透带内返 nil，AppKit 落到罩子下面的
	// WKWebView——按钮吃到的就是普通页面点击（悬停/光标同享此路）。
	if (gShellPassW > 0 && point.x >= gShellPassX0 &&
	    point.x < gShellPassX0 + gShellPassW) {
		return nil;
	}
	return [super hitTest:point];
}
- (void)mouseDown:(NSEvent *)event {
	NSWindow *w = self.window;
	if (event.clickCount >= 2) {
		[w zoom:nil];
		return;
	}
	// Cursor positions come from the events themselves (global display
	// coords, y down) — deltas map onto AppKit coords with a y flip.
	CGPoint prev = CGEventGetLocation(event.CGEvent);
	for (;;) {
		NSEvent *e = [w nextEventMatchingMask:
		    NSEventMaskLeftMouseDragged | NSEventMaskLeftMouseUp];
		if (!e || e.type == NSEventTypeLeftMouseUp) break;
		CGPoint cur = CGEventGetLocation(e.CGEvent);
		[w setFrameOrigin:NSMakePoint(w.frame.origin.x + (cur.x - prev.x),
		                              w.frame.origin.y - (cur.y - prev.y))];
		prev = cur;
	}
}
@end

void shellBlendTitlebar(void *window) {
	NSWindow *w = (NSWindow *)window;
	w.titlebarAppearsTransparent = YES;
	w.titleVisibility = NSWindowTitleHidden;
	w.styleMask |= NSWindowStyleMaskFullSizeContentView;
	if (@available(macOS 11.0, *)) {
		w.titlebarSeparatorStyle = NSTitlebarSeparatorStyleNone;
	}
	// Wrap the webview (manual retain/release — no ARC here). The
	// container is the contentView the window auto-resizes; the webview
	// fills it and the hood stays glued to the top edge. If the view
	// under the window stops being a WKWebView some day, leave the
	// hierarchy alone rather than wrap the wrong thing.
	NSView *web = w.contentView;
	if (![web isKindOfClass:NSClassFromString(@"WKWebView")]) {
		return;
	}
	// The page lays out BELOW the native titlebar strip, permanently:
	// WKWebView auto-insets its content under FullSizeContentView and
	// the kill-switch is GONE on this macOS — WKWebView no longer
	// responds to automaticallyAdjustsContentInsets (instancesRespond ==
	// NO, measured on this Tahoe box 2026-10-03), so the old KVC throws,
	// and viewport-fit=cover changes nothing on AppKit either. An earlier
	// experiment faked edge-to-edge with an empty NSToolbar (one-line
	// 52pt chrome, lights centered 26, hood 52, page pad 52) — it reads
	// as a big blank band because the inset stacks on the page pad. So
	// the contract here is: the 28pt strip is solid window background
	// (shellSetTitlebarColor), the page starts under it and pads its own
	// --tb-h (28) as the ☰/←/→ cluster row (web/index.html). The hood
	// stays 28 == the strip, so drag/zoom covers exactly the chrome.
	NSView *wrap = [[NSView alloc] initWithFrame:web.bounds];
	wrap.autoresizesSubviews = YES;
	wrap.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	web.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
	web.frame = wrap.bounds;
	[wrap addSubview:web];
	NSView *hood = [[ShellHoodView alloc]
	    initWithFrame:NSMakeRect(0, NSHeight(wrap.bounds) - kShellHoodH,
	                        NSWidth(wrap.bounds), kShellHoodH)];
	hood.autoresizingMask = NSViewWidthSizable | NSViewMinYMargin;
	[wrap addSubview:hood];
	w.contentView = wrap;
	[wrap release];
	[hood release];
	// One-line startup probe (unified log, `log show --predicate
	// 'process == "niuma"'`): what the strip hit-test lands on once the
	// hood is up — if the strip ever stops answering presses, this says
	// whether the hood is still the top view under it.
	NSView *hit = [[w contentView] hitTest:
	    NSMakePoint(NSWidth(w.frame) / 2, NSHeight(w.frame) - kShellHoodH / 2)];
	NSLog(@"shellTitlebar: contentView=%@ hitStrip=%@",
	      NSStringFromClass([[w contentView] class]), NSStringFromClass([hit class]));
}

void shellSetTitlebarColor(void *window, double r, double g, double b) {
	NSWindow *w = (NSWindow *)window;
	w.backgroundColor = [NSColor colorWithSRGBRed:r green:g blue:b alpha:1];
}

// shellSetHoodPassZone arms/disarms the toolbar passthrough (x range in
// viewport CSS px from the window's left edge; w<=0 disarms). Main thread
// only — the binding hops there via Dispatch before calling.
void shellSetHoodPassZone(double x, double w) {
	gShellPassX0 = x;
	gShellPassW = w;
}
