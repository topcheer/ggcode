//go:build !darwin

package main

// detectMacDarkMode always returns true on non-macOS platforms.
// The desktop app defaults to dark theme; this fallback ensures
// the initial window background matches the dark UI.
func detectMacDarkMode() bool {
	return true
}
