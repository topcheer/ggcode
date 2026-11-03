//go:build !unix

package tui

import "context"

type displaySleepMsg struct{}

type displayWakeMsg struct{}

func startStdoutHealthMonitor(_ context.Context, _ func(any)) (stop func()) {
	return func() {}
}

func stdoutDead() bool { return false }
