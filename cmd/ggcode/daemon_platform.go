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
func detachToBackground(lang daemon.Lang, cfgFile, workingDir, sessionID string) {
	var extra []string
	if sessionID != "" {
		extra = []string{"--resume=" + sessionID}
	}
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, sessionID, extra...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return
	}
	fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_ok", pid))
}
