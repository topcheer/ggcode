package main

import (
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/safego"
	"golang.org/x/term"
)

// readKeyboard reads raw keystrokes from stdin and sends them to the channel.
// Returns a function that restores the terminal to its original state.
func readKeyboard(ch chan<- byte) func() {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return func() {}
	}

	safego.Go("daemon.keyboard.read", func() {
		defer close(ch)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ch <- buf[0]
		}
	})

	return func() {
		term.Restore(int(os.Stdin.Fd()), oldState)
	}
}

// detachToBackground forks the daemon into background mode.
// Returns true only when the child daemon was successfully forked;
// on failure the caller must stay in the foreground (#2633).
func detachToBackground(lang daemon.Lang, cfgFile, workingDir, sessionID string) bool {
	// #552-A: refuse to fork when a daemon already owns this working dir —
	// otherwise 'd' pressed twice (or racing with --background) forks two
	// daemons that interleave logs and fight over the relay.
	if err := daemon.EnsureDaemonSlot(workingDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return false
	}
	var extra []string
	if sessionID != "" {
		extra = []string{"--resume=" + sessionID}
	}
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, sessionID, extra...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return false
	}
	fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_ok", pid))
	return true
}
