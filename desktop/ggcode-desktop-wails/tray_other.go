//go:build !darwin

package main

// initSystemTray is a no-op on non-macOS platforms.
// System tray support is currently macOS-only.
func (a *App) initSystemTray() {}
