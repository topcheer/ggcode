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
)

// enableBubbleteaTrace points bubbletea v2 at a per-pid log file by setting
// TEA_TRACE before tea.NewProgram is constructed. Bubbletea reads this env
// var inside NewProgram and, if set, attaches a logger that records cancel-
// reader/readLoop activity. Controlled by GGCODE_DEBUG_BUBBLETEA env var.
func enableBubbleteaTrace() {
	// If user already set TEA_TRACE, respect it.
	if existing, ok := os.LookupEnv("TEA_TRACE"); ok && existing != "" {
		return
	}
	// Only enable if GGCODE_DEBUG_BUBBLETEA is explicitly set.
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

// drainStdinResidual reads and discards any bytes already sitting in the
// terminal input buffer before bubbletea starts. This prevents synchronized-
// output mode probe responses (CSI ?2026$p → ESC[?2026;2$y) and stale paste
// content from leaking back to the parent shell or being misinterpreted as
// keypresses by the readLoop.
//
// Strategy: temporarily mark stdin O_NONBLOCK, read until EAGAIN/EOF, restore
// the original flags. We deliberately avoid term.MakeRaw here because
// bubbletea will do that itself a few ms later and we don't want to fight it.
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

// startTTYWatchdog periodically inspects the controlling terminal's termios
// state and logs whenever bubbletea's expected raw-mode invariants appear to
// be broken (e.g. ICANON re-enabled, ECHO turned back on). It runs until ctx
// is canceled.
//
// In addition to detection, when raw-mode loss is observed we proactively
// re-apply MakeRaw on stdin. This is a defensive measure for terminals
// (notably some Warp / multiplexer setups on macOS) where bubbletea v2's
// initial MakeRaw call returns success but the change does not take effect
// — leaving the kernel TTY in cooked mode while bubbletea renders to the
// alt-screen, which causes typed bytes and mouse SGR sequences to be
// echoed by the line discipline directly on top of the alt-screen frame.
func startTTYWatchdog(ctx context.Context) (stop func()) {
	fd := int(os.Stdin.Fd())
	if !isTTY(fd) {
		return func() {}
	}
	// Capture the shell's pre-bubbletea state once, so we can log a clean
	// baseline diff and so we have something to restore on shutdown if our
	// own MakeRaw ends up being the last writer.
	preBubbletea, _ := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if preBubbletea != nil {
		debug.Log("repl", "tty-watchdog pre-bubbletea lflag=0x%x ICANON=%v ECHO=%v",
			preBubbletea.Lflag, preBubbletea.Lflag&unix.ICANON != 0, preBubbletea.Lflag&unix.ECHO != 0)
	}
	wctx, cancel := context.WithCancel(ctx)
	go func() {
		// Tight initial loop: aggressively re-apply raw mode for the first
		// ~1.5s after bubbletea startup. This is the window in which bubbletea
		// (or something it calls into) has been observed to silently revert
		// the kernel TTY to cooked mode, causing typed bytes to be echoed by
		// the line discipline on top of the alt-screen frame.
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
		// Steady-state slow check.
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
	}()
	return cancel
}

// forceRawMode applies a cfmakeraw-equivalent transformation to the given fd.
// This mirrors x/term's MakeRaw but is safe to call repeatedly and never
// touches state we don't intend to (we operate on the *current* termios so
// other tweaks bubbletea may have made survive).
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
