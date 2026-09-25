package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/toolreplay"
)

// Deterministic record/replay utilities for tool tapes (the "VCR/cassette"
// pattern implemented by internal/toolreplay). Record a session with
// GGCODE_TOOL_TAPE=record:<path>, then use `ggcode tape verify` to replay
// the recorded tool boundaries against the current code — Chronicle-style
// cut-point verification that turns a recorded incident into an offline
// regression test (arXiv:2609.20625).
func newTapeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tape",
		Short: "Tool-tape record/replay utilities (deterministic regression testing)",
	}
	cmd.AddCommand(newTapeVerifyCmd())
	cmd.AddCommand(newTapeInfoCmd())
	return cmd
}

func newTapeVerifyCmd() *cobra.Command {
	var cut int
	var includeUnsafe bool
	var workingDir string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "verify <tape.json>",
		Short: "Re-run recorded tool boundaries against current code and report divergences",
		Long: `Cut-point verification of a recorded tool tape.

Boundaries [0, cut) are served from the record (never executed); the rest
are executed LIVE with the current tool code and compared against the
recorded results. Any divergence fails the run (exit code 1), so a tape
attached to a bug report doubles as a CI regression test.

By default only read-only tools are re-executed; mutating boundaries are
reported as skipped-unsafe. Pass --include-unsafe to execute them too —
only in a workspace that tolerates the recorded side effects.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tapePath := args[0]
			tape, err := toolreplay.LoadTape(tapePath)
			if err != nil {
				return fmt.Errorf("load tape: %w", err)
			}
			wd := workingDir
			if wd == "" {
				if wd, err = os.Getwd(); err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
			}
			wd, err = filepath.Abs(wd)
			if err != nil {
				return fmt.Errorf("abs working directory: %w", err)
			}
			reg := tool.NewRegistry()
			// nil policy/sandbox = permissive: verify re-executes only
			// read-only tools by default, so containment is not required.
			if err := tool.RegisterBuiltinTools(reg, nil, wd, nil, nil); err != nil {
				return fmt.Errorf("register builtin tools: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			results, err := toolreplay.Verify(ctx, reg, tape, toolreplay.VerifyOptions{
				Cut:           cut,
				IncludeUnsafe: includeUnsafe,
			})
			if err != nil {
				return fmt.Errorf("verify: %w", err)
			}
			report := toolreplay.VerifyReport{TapePath: tapePath, Results: results}
			fmt.Fprint(cmd.OutOrStdout(), renderVerifyReport(report))
			if !report.Passed() {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&cut, "cut", 0, "serve the first N boundaries from the record instead of executing them")
	cmd.Flags().BoolVar(&includeUnsafe, "include-unsafe", false, "also re-execute mutating tools live (may cause side effects)")
	cmd.Flags().StringVar(&workingDir, "workdir", "", "working directory for tool execution (default: current directory)")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "overall timeout for live executions")
	return cmd
}

func newTapeInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <tape.json>",
		Short: "Summarize the boundaries recorded in a tape",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tape, err := toolreplay.LoadTape(args[0])
			if err != nil {
				return fmt.Errorf("load tape: %w", err)
			}
			entries := tape.EntriesInOrder()
			fmt.Fprintf(cmd.OutOrStdout(), "tape %s: %d boundaries\n", args[0], len(entries))
			for i, e := range entries {
				input := string(e.Input)
				if len(input) > 80 {
					input = input[:80] + "..."
				}
				state := "ok"
				if e.Err != "" {
					state = "error: " + e.Err
				} else if e.Result.IsError {
					state = "error (result)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  #%-3d %-18s %-40s [%s]\n", i, e.ToolName, input, state)
			}
			return nil
		},
	}
}

// renderVerifyReport formats a verify report for terminal output. One line
// per boundary plus a final verdict; logic kept pure for tests.
func renderVerifyReport(report toolreplay.VerifyReport) string {
	var sb strings.Builder
	for _, res := range report.Results {
		input := string(res.Input)
		if idx := indexNewline(input); idx >= 0 {
			input = input[:idx]
		}
		if len(input) > 48 {
			input = input[:48] + "..."
		}
		fmt.Fprintf(&sb, "  #%-3d %-16s %-18s %s", res.Index, res.Status, res.ToolName, input)
		if res.Detail != "" {
			fmt.Fprintf(&sb, "  — %s", res.Detail)
		}
		sb.WriteByte('\n')
	}
	counts := report.Counts()
	total := len(report.Results)
	verdict := "PASS"
	if !report.Passed() {
		verdict = "FAIL"
	}
	fmt.Fprintf(&sb,
		"tape verify: %s — %d/%d boundaries verified (match: %d, diverged: %d, cut: %d, skipped-unsafe: %d, no-tool: %d)\n",
		verdict, counts[toolreplay.VerifyMatch]+counts[toolreplay.VerifyCut], total,
		counts[toolreplay.VerifyMatch], counts[toolreplay.VerifyDiverged],
		counts[toolreplay.VerifyCut], counts[toolreplay.VerifySkippedUnsafe],
		counts[toolreplay.VerifyNoTool])
	return sb.String()
}

func indexNewline(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return i
		}
	}
	return -1
}
