package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/sessionidxd"
)

// newSessionIdxdCmd is the hidden self-launch entry the sessionidxd client
// spawns ("<argv0> sessionidxd-serve --sessions ... --home ..."). Hidden:
// not part of the user-facing surface (#5149).
func newSessionIdxdCmd() *cobra.Command {
	var sessionsDir, home string
	cmd := &cobra.Command{
		Use:    "sessionidxd-serve",
		Short:  "Internal: shared session-index daemon (auto-spawned)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionsDir == "" || home == "" {
				return fmt.Errorf("--sessions and --home are required")
			}
			return sessionidxd.Serve(sessionsDir, home)
		},
	}
	cmd.Flags().StringVar(&sessionsDir, "sessions", "", "sessions directory")
	cmd.Flags().StringVar(&home, "home", "", "ggcode home directory")
	return cmd
}
