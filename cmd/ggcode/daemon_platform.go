package main

import (
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/safego"
	"golang.org/x/term"
)

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

func detachToBackground(lang daemon.Lang, cfgFile, workingDir, sessionID string, runtimeFlags []string) bool {

	if err := daemon.EnsureDaemonSlot(workingDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return false
	}

	var extra []string
	if sessionID != "" {
		extra = append(extra, "--resume="+sessionID)
	}
	extra = append(extra, runtimeFlags...)
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, sessionID, extra...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return false
	}
	fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_ok", pid))
	return true
}
