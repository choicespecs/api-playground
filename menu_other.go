//go:build !darwin

package main

// setupMainMenu is a no-op outside macOS: WebView2 and WebKitGTK handle
// clipboard shortcuts natively without an application menu.
func setupMainMenu() {}
