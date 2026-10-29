//go:build !darwin && !windows

package main

// screenSize returns a fixed default size on platforms without a native
// screen-size implementation (e.g. Linux). main() caps the window at
// 1400×900, so this simply yields the maximum window size.
func screenSize() (width, height int) {
	return 1440, 960
}
