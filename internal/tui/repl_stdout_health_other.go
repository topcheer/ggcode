//go:build !unix

package tui

// displaySleepMsg is a no-op on non-Unix platforms (stdout health monitoring is Unix-only).
type displaySleepMsg struct{}

// displayWakeMsg is a no-op on non-Unix platforms.
type displayWakeMsg struct{}

// startStdoutHealthMonitor is a no-op on non-Unix platforms.
func startStdoutHealthMonitor(p *Program) {}
