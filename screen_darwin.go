package main

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>

static void mainScreenLogicalSize(int *w, int *h) {
    // CGDisplayBounds returns values in logical points (not physical pixels),
    // so on Retina displays this already reflects the scaled resolution.
    CGRect bounds = CGDisplayBounds(CGMainDisplayID());
    *w = (int)bounds.size.width;
    *h = (int)bounds.size.height;
}
*/
import "C"

// screenSize returns the main display's logical size in points.
// On a Retina MacBook set to "looks like 1280×800" this returns 1280, 800.
func screenSize() (width, height int) {
	var w, h C.int
	C.mainScreenLogicalSize(&w, &h)
	return int(w), int(h)
}
