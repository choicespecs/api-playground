package main

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>

// mainScreenLogicalSize returns the logical size of the main display in points.
// CGDisplayBounds returns values in logical points (not physical pixels),
// so on Retina displays this already reflects the scaled "looks like" resolution
// (e.g. a MacBook Pro with a 2560×1600 panel set to "looks like 1280×800"
// returns 1280, 800 — the values the window manager works in).
static void mainScreenLogicalSize(int *w, int *h) {
    CGRect bounds = CGDisplayBounds(CGMainDisplayID());
    *w = (int)bounds.size.width;
    *h = (int)bounds.size.height;
}
*/
import "C"

// screenSize returns the main display's logical size in points.
// Called by main() to size the WKWebView window to fit the screen
// (capped at 1400×900 with breathing room for the macOS menu bar).
//
// Requires CGO_ENABLED=1 and the CoreGraphics framework (Xcode CLT).
func screenSize() (width, height int) {
	var w, h C.int
	C.mainScreenLogicalSize(&w, &h)
	return int(w), int(h)
}
