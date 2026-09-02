//go:build !darwin

package main

import "errors"

// On non-macOS platforms the global hotkey is unsupported.
// #1430-B: this used to return nil (fake success) - the enable path
// persisted the preference and told the UI 'enabled', while the OS
// never registered anything; Windows/Linux users got a toggle that
// silently did nothing, surviving restarts (the startup path returned
// nil too). Returning an error engages the #615 rollback chain
// ("OS registration fails -> persisted preference rolled back") so the
// UI can tell the truth. Windows/Linux support can be added later via
// platform-specific APIs.
func (a *App) initGlobalHotkey() error {
	return errHotkeyUnsupported
}
func (a *App) removeGlobalHotkey() {}

var errHotkeyUnsupported = errors.New("global hotkey is not supported on this platform")
