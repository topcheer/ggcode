//go:build unix

package tui

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// displaySleepMsg is sent by the stdout health monitor when stdout
// becomes unwritable (display sleep, terminal closed, SSH disconnect).
type displaySleepMsg struct{}

// displayWakeMsg is sent when stdout becomes writable again.
type displayWakeMsg struct{}

// stdoutDeadFlag is an atomic flag shared between the health monitor and
// the renderer. When true, the renderer should skip Write() calls.
var stdoutDeadFlag atomic.Bool

// IsStdoutDead returns true if stdout has been detected as dead.
func IsStdoutDead() bool {
	return stdoutDeadFlag.Load()
}

// stdoutHealthInterval is how often we check if stdout is still writable.
const stdoutHealthInterval = 2 * time.Second

// stdoutProbeTimeout is the write deadline for the stdout health probe.
// If a tiny write takes longer than this, stdout is considered dead.
const stdoutProbeTimeout = 500 * time.Millisecond

// startStdoutHealthMonitor watches stdout for writability. When stdout
// becomes unwritable (display sleep, terminal closed, SSH disconnect),
// it sets stdoutDead=true and sends displaySleepMsg to the program.
// When stdout recovers, it sends displayWakeMsg.
//
// This prevents bubbletea's renderer from blocking on Write() to a dead
// terminal, which would freeze the entire TUI update loop.
func startStdoutHealthMonitor(ctx context.Context, sendMsg func(any)) (stop func()) {
	// Check if stdout is a terminal — if piped, skip monitoring
	if !isTerminalStdout() {
		return func() {}
	}

	wctx, cancel := context.WithCancel(ctx)
	dead := false

	go func() {
		ticker := time.NewTicker(stdoutHealthInterval)
		defer ticker.Stop()

		for {
			select {
			case <-wctx.Done():
				return
			case <-ticker.C:
				alive := probeStdout()
				if !alive && !dead {
					debug.Log("repl", "stdout-health: stdout appears dead (display sleep/terminal closed?)")
					stdoutDeadFlag.Store(true)
					dead = true
					if sendMsg != nil {
						sendMsg(displaySleepMsg{})
					}
				} else if alive && dead {
					debug.Log("repl", "stdout-health: stdout recovered")
					stdoutDeadFlag.Store(false)
					dead = false
					if sendMsg != nil {
						sendMsg(displayWakeMsg{})
					}
				}
			}
		}
	}()

	return cancel
}

// probeStdout checks if stdout is still writable by attempting a
// zero-byte write with a deadline. Returns true if stdout is healthy.
func probeStdout() bool {
	// Get file status flags
	fd := int(os.Stdout.Fd())

	// Try a non-blocking probe: write zero bytes (doesn't actually
	// send data but checks if the fd is in a valid state).
	// On macOS, when the display sleeps, writing to the terminal
	// will eventually return EIO or block indefinitely.

	// Strategy: set non-blocking, attempt tiny write, check result
	flags, err := fdGetFlags(fd)
	if err != nil {
		return false
	}

	// Set non-blocking
	_ = fdSetFlags(fd, flags|0x800) // O_NONBLOCK
	defer fdSetFlags(fd, flags)     // restore

	// Try writing a no-op ANSI sequence (cursor position report response
	// that doesn't change display). This is 0 bytes of visible output.
	probe := []byte{}
	_, err = os.Stdout.Write(probe)

	if err != nil {
		// Write failed — stdout is dead
		return false
	}

	return true
}

func isTerminalStdout() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
