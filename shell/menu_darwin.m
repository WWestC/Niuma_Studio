#import <AppKit/AppKit.h>

// shellInstallEditMenu installs the app's main menu — the piece the
// vendored webview core never builds. macOS delivers ⌘C/⌘V/⌘X/⌘Z/⌘A by
// matching the key equivalent against menu items and sending the
// selector (copy:/paste:/…) down the responder chain; a window with no
// main menu has no such items, so the WKWebView's focused web input
// never hears the keyboard (the right-click context menu works, the
// keys don't). With the standard Edit items in place the selectors land
// on the WKWebView as first responder and it performs them inside the
// focused input — chat composer included.
//
// Deliberately NO "quit" item anywhere in this menu: quitting must go
// through the window-close path (Go main's teardown flushes the room
// snapshot on the way out); an NSApp terminate: here would bypass that.
void shellInstallEditMenu(void) {
  NSMenu *bar = [[NSMenu alloc] initWithTitle:@"MainMenu"];

  // App menu (the bold one): hide-family items only — window-management
  // niceties that never terminate the process.
  NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"牛马工作室"];
  NSMenuItem *hide = [appMenu addItemWithTitle:@"隐藏牛马工作室"
                                        action:@selector(hide:)
                                 keyEquivalent:@"h"];
  hide.target = NSApp;
  NSMenuItem *hideOthers = [appMenu addItemWithTitle:@"隐藏其他"
                                              action:@selector(hideOtherApplications:)
                                       keyEquivalent:@"h"];
  hideOthers.target = NSApp;
  hideOthers.keyEquivalentModifierMask =
      NSEventModifierFlagCommand | NSEventModifierFlagOption;
  NSMenuItem *unhide = [appMenu addItemWithTitle:@"显示全部"
                                          action:@selector(unhideAllApplications:)
                                   keyEquivalent:@""];
  unhide.target = NSApp;
  NSMenuItem *appRoot = [[NSMenuItem alloc] initWithTitle:@"牛马工作室"
                                                    action:nil
                                             keyEquivalent:@""];
  appRoot.submenu = appMenu;
  [bar addItem:appRoot];

  // Edit menu: the reason this all exists. target nil so the action
  // rides the responder chain into the WKWebView; autoenablesItems off
  // and every item always enabled — whether an action is possible is
  // the web layer's call (it no-ops when not, e.g. copy with no
  // selection), the menu must not eat the key press before the page
  // gets a chance.
  NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"编辑"];
  editMenu.autoenablesItems = NO;
  void (^add)(NSString *, SEL, NSString *, NSEventModifierFlags) =
      ^(NSString *title, SEL action, NSString *key,
        NSEventModifierFlags mods) {
        NSMenuItem *it = [editMenu addItemWithTitle:title
                                             action:action
                                      keyEquivalent:key];
        it.keyEquivalentModifierMask = mods;
        it.target = nil;
        it.enabled = YES;
      };
  add(@"撤销", @selector(undo:), @"z", NSEventModifierFlagCommand);
  add(@"重做", @selector(redo:), @"z",
      NSEventModifierFlagCommand | NSEventModifierFlagShift);
  [editMenu addItem:[NSMenuItem separatorItem]];
  add(@"剪切", @selector(cut:), @"x", NSEventModifierFlagCommand);
  add(@"复制", @selector(copy:), @"c", NSEventModifierFlagCommand);
  add(@"粘贴", @selector(paste:), @"v", NSEventModifierFlagCommand);
  add(@"全选", @selector(selectAll:), @"a", NSEventModifierFlagCommand);
  NSMenuItem *editRoot = [[NSMenuItem alloc] initWithTitle:@"编辑"
                                                     action:nil
                                              keyEquivalent:@""];
  editRoot.submenu = editMenu;
  [bar addItem:editRoot];

  NSApp.mainMenu = bar;
}
