//go:build !unix

package tui

import "context"

// displaySleepMsg is a no-op on non-Unix platforms (stdout health monitoring is Unix-only).
type displaySleepMsg struct{}

// displayWakeMsg is a no-op on non-Unix platforms.
type displayWakeMsg struct{}

// startStdoutHealthMonitor is a no-op on non-Unix platforms.
func startStdoutHealthMonitor(_ context.Context, _ func(any)) (stop func()) {
	return func() {}
}
