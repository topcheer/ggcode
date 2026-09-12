//go:build unix

package tui

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"golang.org/x/sys/unix"
)

// displaySleepMsg is sent by the stdout health monitor when stdout
// becomes unwritable (display sleep, terminal closed, SSH disconnect).
type displaySleepMsg struct{}

// displayWakeMsg is sent when stdout becomes writable again.
type displayWakeMsg struct{}

// stdoutDeadFlag is an atomic flag shared between the health monitor and
// the renderer. When true, the renderer should skip Write() calls.
var stdoutDeadFlag atomic.Bool

// stdoutHealthInterval is how often we check if stdout is still writable.
const stdoutHealthInterval = 2 * time.Second

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

	safego.Go("tui.stdoutHealth", func() {
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
	})

	return cancel
}

// probeStdout checks if stdout is still writable by attempting a
// zero-byte write with a deadline. Returns true if stdout is healthy.
func probeStdout() bool {
	// #1753 case 2: the probe used to flip O_NONBLOCK and WRITE an SGR
	// reset on the shared stdout fd - on Linux (where non-blocking now
	// actually engages, #2018) a renderer frame mid-window hit EAGAIN and
	// the escape byte interleaved into frames. unix.Poll checks the fd's
	// error/hangup state with ZERO writes and NO flag flipping: no shared
	// state touched, no interleaving possible.
	fd := int(os.Stdout.Fd())
	pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	n, err := unix.Poll(pollFds, 0)
	if err != nil {
		// EBADF and friends: the fd is gone.
		return false
	}
	if n == 0 {
		// Not writable RIGHT NOW - on a tty this is the sleep/death
		// symptom (a healthy pty accepts kernel-buffered writes).
		return false
	}
	revents := pollFds[0].Revents
	return revents&unix.POLLERR == 0 && revents&unix.POLLHUP == 0 && revents&unix.POLLNVAL == 0
}

func isTerminalStdout() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
