package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestIssue2739_DaemonizedFlagParsesWithRestartFlags(t *testing.T) {

	root := NewRootCmd()

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
