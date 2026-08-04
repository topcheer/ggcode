//go:build !darwin

package main

// On non-macOS platforms the global hotkey is a no-op.
// Windows/Linux support can be added later via platform-specific APIs.

func (a *App) initGlobalHotkey()   {}
func (a *App) removeGlobalHotkey() {}
