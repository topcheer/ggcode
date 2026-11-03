//go:build !darwin

package main

import "errors"

func (a *App) initGlobalHotkey() error {
	return errHotkeyUnsupported
}
func (a *App) removeGlobalHotkey() {}

var errHotkeyUnsupported = errors.New("global hotkey is not supported on this platform")
