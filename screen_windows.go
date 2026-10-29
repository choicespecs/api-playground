package main

import (
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSystemParamsInfo = user32.NewProc("SystemParametersInfoW")
	procGetDpiForSystem  = user32.NewProc("GetDpiForSystem")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

const (
	spiGetWorkArea = 0x0030 // SPI_GETWORKAREA
	smCxScreen     = 0      // SM_CXSCREEN
	smCyScreen     = 1      // SM_CYSCREEN
)

type rect struct {
	Left, Top, Right, Bottom int32
}

// screenSize returns the primary display's usable work area (excluding the
// taskbar) in logical (96-DPI) units. Called by main() to size the WebView2
// window to fit the screen.
//
// If the process is DPI-aware, Win32 reports physical pixels, so the result is
// divided by the system DPI scale. If it is not DPI-aware, Windows already
// reports scaled values and GetDpiForSystem returns 96, so no scaling occurs.
func screenSize() (width, height int) {
	var r rect
	ok, _, _ := procSystemParamsInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ok != 0 {
		width, height = int(r.Right-r.Left), int(r.Bottom-r.Top)
	} else {
		w, _, _ := procGetSystemMetrics.Call(smCxScreen)
		h, _, _ := procGetSystemMetrics.Call(smCyScreen)
		width, height = int(w), int(h)
	}

	// GetDpiForSystem exists on Windows 10 1607+; skip scaling on older systems.
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi > 0 {
			width = width * 96 / int(dpi)
			height = height * 96 / int(dpi)
		}
	}
	return width, height
}
