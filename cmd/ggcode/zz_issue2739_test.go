package main

// #2739 regression (cmd side): the exec-restart argv must carry
// --__daemonized, or daemonIdentityMatches sees the restarted PID as an
// unrelated process (PID-reuse false positive), deletes the PID file, and
// admits a second daemon. This test pins that the daemon command accepts
// the hidden flag combined with the exact restart flags exec-restart
// builds; the identity-match half lives in internal/daemon (same-package
// test hook access).

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestIssue2739_DaemonizedFlagParsesWithRestartFlags(t *testing.T) {
	// The exact flag combination exec-restart builds:
	// [--config X] daemon --follow --__daemonized [--resume ID] [--bypass] ...
	root := NewRootCmd()
	// The flags live on the daemon subcommand; locate it like a real
	// invocation would.
	var daemonCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "daemon" {
			daemonCmd = c
			break
		}
	}
	if daemonCmd == nil {
		t.Fatalf("daemon subcommand not found")
	}
	if err := daemonCmd.ParseFlags([]string{
		"--follow", "--__daemonized", "--bypass",
	}); err != nil {
		t.Fatalf("exec-restart flag combo must parse: %v", err)
	}
	f := daemonCmd.Flags()
	if v, _ := f.GetBool("__daemonized"); !v {
		t.Fatalf("__daemonized must be settable via combined restart argv")
	}
}
