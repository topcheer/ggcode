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

type displaySleepMsg struct{}

type displayWakeMsg struct{}

func stdoutDead() bool { return stdoutDeadFlag.Load() }

var stdoutDeadFlag atomic.Bool

const stdoutHealthInterval = 2 * time.Second

func startStdoutHealthMonitor(ctx context.Context, sendMsg func(any)) (stop func()) {

	if !isTerminalStdout() {

		return func() { stdoutDeadFlag.Store(false) }
	}

	wctx, cancel := context.WithCancel(ctx)

	dead := stdoutDeadFlag.Load()

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

	return func() {
		cancel()

		stdoutDeadFlag.Store(false)
	}
}

func probeStdout() bool {

	fd := int(os.Stdout.Fd())
	pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	n, err := unix.Poll(pollFds, 0)
	if err != nil {

		return false
	}
	if n == 0 {

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
