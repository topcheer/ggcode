package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/tool"
)

// newSelfcheckCmd (#5553): hidden pre-restart smoke probe. Boots the
// real builtin tool registry and executes one run_command echo
// round-trip; exits non-zero with diagnostics when the agent-core
// plumbing is broken (e.g. the #5549 zero-toolDefs regression).
// Invoked automatically by the restart tool's pre-arm smoke gate
// (tool.RestartSmoke); also usable manually after a binary swap.
func newSelfcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "selfcheck",
		Short:  "Internal: pre-restart agent-core smoke probe (hidden)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("selfcheck: resolve working dir: %w", err)
			}
			ctx := cmd.Context()
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			if err := tool.SelfCheck(ctx, wd); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "selfcheck: OK - builtin registry booted, run_command echo round-trip asserted")
			return nil
		},
	}
}
