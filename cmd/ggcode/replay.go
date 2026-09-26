package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/replay"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

// newReplayCmd builds `ggcode replay <session-id>`: deterministic replay of a
// recorded session's read-only tool calls against the current workspace, with
// a diff of live vs recorded results. Zero LLM inference is performed -- the
// model stays out of the loop, so replay costs no tokens (see
// internal/replay package docs and arXiv:2607.16200).
func newReplayCmd() *cobra.Command {
	var (
		workDir   string
		limit     int
		toolsFlag string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "replay <session-id>",
		Short: "Replay a recorded session's read-only tool calls and diff against recorded results",
		Long: `Replay deterministically re-executes the read-only tool calls recorded in a
session (read_file, grep, glob, ...) against the current workspace and diffs
the live results against what the agent saw during the original run.

No LLM inference happens: the trace comes from the session file, so replay is
free and deterministic. Mutating tools are never re-executed; they are listed
as skipped. Exit code 1 signals drift (at least one live result differs).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if workDir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolving working directory: %w", err)
				}
				workDir = wd
			}
			store, err := session.NewDefaultStore()
			if err != nil {
				return fmt.Errorf("opening session store: %w", err)
			}
			ses, err := store.Load(args[0])
			if err != nil {
				return fmt.Errorf("loading session %q: %w", args[0], err)
			}
			trace := replay.ExtractTrace(ses.Messages)
			if len(trace) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "replay: session has no recorded tool calls")
				return nil
			}
			registry := tool.NewRegistry()
			policy := permission.NewConfigPolicyWithMode(nil, nil, permission.BypassMode)
			if err := tool.RegisterBuiltinTools(registry, policy, workDir, nil, tool.NewSandboxPolicy(false, nil, nil)); err != nil {
				return fmt.Errorf("registering tools: %w", err)
			}
			rp := replay.NewReplayer(registry)
			if toolsFlag != "" {
				allow := make(map[string]bool)
				for _, name := range strings.Split(toolsFlag, ",") {
					if name = strings.TrimSpace(name); name != "" {
						allow[name] = true
					}
				}
				rp.AllowedTools = allow
			}
			if limit > 0 && limit < len(trace) {
				trace = trace[:limit]
			}
			rep := rp.Run(context.Background(), ses.ID, trace)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return fmt.Errorf("encoding report: %w", err)
				}
			} else {
				rep.Render(cmd.OutOrStdout())
			}
			if rep.DriftDetected() {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workDir, "workdir", "", "working directory for replayed tool calls (default: current directory)")
	cmd.Flags().IntVar(&limit, "limit", 0, "replay at most the first N steps (0 = all)")
	cmd.Flags().StringVar(&toolsFlag, "tools", "", "comma-separated allowlist override (default: built-in read-only tools)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON report")
	return cmd
}
