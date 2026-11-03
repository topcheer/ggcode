//go:build unix

package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

func enableBubbleteaTrace() {

	if existing, ok := os.LookupEnv("TEA_TRACE"); ok && existing != "" {
		return
	}

	if v := os.Getenv("GGCODE_DEBUG_BUBBLETEA"); v == "" {
		return
	}
	dir := "/tmp/ggcode-debug"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("ggcode-bubbletea-%d.log", os.Getpid()))
	_ = os.Setenv("TEA_TRACE", path)
	debug.Log("repl", "TEA_TRACE enabled path=%s", path)
}

func drainStdinResidual() {
	fd := int(os.Stdin.Fd())
	if !isTTY(fd) {
		return
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
		return
	}
	defer func() {
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
	}()
	buf := make([]byte, 4096)
	totalDropped := 0
	for i := 0; i < 16; i++ {
		n, err := syscall.Read(fd, buf)
		if n > 0 {
			totalDropped += n
		}
		if err != nil || n <= 0 {
			break
		}
	}
	if totalDropped > 0 {
		debug.Log("repl", "drainStdinResidual dropped %d bytes from stdin before tea startup", totalDropped)
	}
}

func startTTYWatchdog(ctx context.Context) (stop func()) {
	fd := int(os.Stdin.Fd())
	if !isTTY(fd) {
		return func() {}
	}

	preBubbletea, _ := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if preBubbletea != nil {
		debug.Log("repl", "tty-watchdog pre-bubbletea lflag=0x%x ICANON=%v ECHO=%v",
			preBubbletea.Lflag, preBubbletea.Lflag&unix.ICANON != 0, preBubbletea.Lflag&unix.ECHO != 0)
	}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	safego.Go("tui.ttyWatchdog", func() {
		defer close(done)

		fastTicker := time.NewTicker(20 * time.Millisecond)
		defer fastTicker.Stop()
		fastDeadline := time.After(1500 * time.Millisecond)
		baselineLogged := false
	fast:
		for {
			select {
			case <-wctx.Done():
				return
			case <-fastDeadline:
				break fast
			case <-fastTicker.C:
			}
			cur, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
			if err != nil {
				continue
			}
			if !baselineLogged {
				debug.Log("repl", "tty-watchdog post-NewProgram baseline lflag=0x%x ICANON=%v ECHO=%v",
					cur.Lflag, cur.Lflag&unix.ICANON != 0, cur.Lflag&unix.ECHO != 0)
				baselineLogged = true
			}
			if cur.Lflag&unix.ICANON != 0 || cur.Lflag&unix.ECHO != 0 {
				debug.Log("repl", "tty-watchdog RAW MODE LOST (fast)! lflag=0x%x ICANON=%v ECHO=%v. Reapplying...",
					cur.Lflag, cur.Lflag&unix.ICANON != 0, cur.Lflag&unix.ECHO != 0)
				forceRawMode(fd, cur)
			}
		}

		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		warned := false
		for {
			select {
			case <-wctx.Done():
				return
			case <-t.C:
			}
			cur, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
			if err != nil {
				continue
			}
			canonical := cur.Lflag&unix.ICANON != 0
			echo := cur.Lflag&unix.ECHO != 0
			if canonical || echo {
				if !warned {
					debug.Log("repl", "tty-watchdog RAW MODE LOST! lflag=0x%x ICANON=%v ECHO=%v. Reapplying...",
						cur.Lflag, canonical, echo)
					warned = true
				}
				forceRawMode(fd, cur)
				continue
			}
			if warned {
				debug.Log("repl", "tty-watchdog raw mode appears restored lflag=0x%x", cur.Lflag)
				warned = false
			}
		}
	})
	return func() {
		cancel()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			debug.Log("repl", "tty-watchdog stop: goroutine did not exit within 2s")
		}

		if preBubbletea != nil {
			if err := unix.IoctlSetTermios(fd, ioctlSetTermios, preBubbletea); err != nil {
				debug.Log("repl", "tty-watchdog restore failed err=%v", err)
			} else {
				debug.Log("repl", "tty-watchdog terminal restored to pre-bubbletea state")
			}
		}
	}
}

func forceRawMode(fd int, cur *unix.Termios) {
	cur.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	cur.Oflag &^= unix.OPOST
	cur.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	cur.Cflag &^= unix.CSIZE | unix.PARENB
	cur.Cflag |= unix.CS8
	cur.Cc[unix.VMIN] = 1
	cur.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, cur); err != nil {
		debug.Log("repl", "tty-watchdog forceRawMode set failed err=%v", err)
		return
	}
	verify, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		debug.Log("repl", "tty-watchdog forceRawMode verify failed err=%v", err)
		return
	}
	debug.Log("repl", "tty-watchdog forceRawMode applied; new lflag=0x%x ICANON=%v ECHO=%v",
		verify.Lflag, verify.Lflag&unix.ICANON != 0, verify.Lflag&unix.ECHO != 0)
}

func isTTY(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	return err == nil
}
