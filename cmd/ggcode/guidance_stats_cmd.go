package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/agent"
)

// newGuidanceStatsCmd returns the `ggcode guidance-stats` subcommand: an
// offline report of detector guidance effectiveness telemetry collected at
// the end of each agent run (<cwd>/.ggcode/guidance-stats.jsonl).
//
// Methodology: per-tag fires, repeat-fire rate (guidance did not stick) and
// negative-attribution rate (user pushback after firing), each with Wilson
// 95% score intervals (NIST binomial-interval guidance; preferred over Wald
// near 0/1 and at modest samples). Tags below the minimum sample size are
// reported as "insufficient evidence" instead of judged. Report-only: the
// stats never auto-suppress any detector.
func newGuidanceStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guidance-stats",
		Short: "Show detector guidance effectiveness statistics (fires, repeat rate, negative attribution with Wilson 95% CIs)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			reports, runs, err := agent.AggregateGuidanceStats(cwd)
			if err != nil {
				return fmt.Errorf("reading guidance stats: %w", err)
			}
			if runs == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No guidance statistics recorded yet.")
				fmt.Fprintln(cmd.OutOrStdout(), "Statistics are appended to ./.ggcode/guidance-stats.jsonl at the end of each agent run in this project.")
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), agent.RenderGuidanceReport(reports, runs))
			return nil
		},
	}
}
