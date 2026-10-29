package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// setupMainMenu installs a minimal macOS menu bar: an app menu (Quit) and a
// standard Edit menu. Without an Edit menu, macOS never routes ⌘C / ⌘V / ⌘X /
// ⌘A / ⌘Z to the WKWebView, so copy and paste silently do nothing.
// The first item of the main menu is always treated as the application menu.
static void setupMainMenu(void) {
    NSMenu *mainMenu = [[NSMenu alloc] init];

    // ── App menu ──────────────────────────────────────────────────────────
    NSMenuItem *appItem = [[NSMenuItem alloc] init];
    NSMenu *appMenu = [[NSMenu alloc] init];
    [appMenu addItemWithTitle:@"Hide API Playground" action:@selector(hide:) keyEquivalent:@"h"];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItemWithTitle:@"Quit API Playground" action:@selector(terminate:) keyEquivalent:@"q"];
    [appItem setSubmenu:appMenu];
    [mainMenu addItem:appItem];

    // ── Edit menu ─────────────────────────────────────────────────────────
    // Actions are nil-targeted, so they go to the first responder (the WKWebView).
    NSMenuItem *editItem = [[NSMenuItem alloc] init];
    NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"Edit"];
    [editMenu addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
    [editMenu addItemWithTitle:@"Redo" action:@selector(redo:) keyEquivalent:@"Z"]; // uppercase = ⇧⌘Z
    [editMenu addItem:[NSMenuItem separatorItem]];
    [editMenu addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
    [editMenu addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
    [editMenu addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
    [editMenu addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
    [editItem setSubmenu:editMenu];
    [mainMenu addItem:editItem];

    [NSApp setMainMenu:mainMenu];
}
*/
import "C"

// setupMainMenu installs the macOS menu bar so standard editing shortcuts
// (copy, paste, cut, select all, undo) reach the WKWebView.
// Must be called on the main thread after webview.New() has created NSApp.
func setupMainMenu() {
	C.setupMainMenu()
}
