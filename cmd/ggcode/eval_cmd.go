package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/tapeeval"
	"github.com/topcheer/ggcode/internal/toolreplay"
)

// eval_cmd implements `ggcode eval` — the offline half of eval-driven
// development (Anthropic "Demystifying evals for AI agents", 2025;
// arXiv:2411.13768). A tape recorded via GGCODE_TOOL_TAPE=record:<path> is
// a transcript; this command grades it against a declarative eval spec
// using deterministic code-based graders (tool-call verification +
// transcript analysis), turning real agent trajectories into CI-runnable
// regression evals with zero LLM cost.
//
// Typical loop:
//
//	GGCODE_TOOL_TAPE=record:session.tape.json ggcode   # capture a good run
//	ggcode eval init session.tape.json                  # scaffold the spec
//	$EDITOR session.eval.json                           # tighten by hand
//	ggcode eval session.tape.json session.eval.json     # gate on every change

func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval <tape.json> <spec.json>",
		Short: "Grade a recorded tool tape against an eval spec (eval-driven regression)",
		Long: `Grades a recorded tool trajectory ("tape") against a declarative eval
spec using deterministic code-based graders — no LLM, no network, no side
effects, suitable as a CI gate.

Tapes are captured by running ggcode with GGCODE_TOOL_TAPE=record:<path>.
Turn a recorded session into a regression eval with "ggcode eval init",
tighten the scaffolded spec by hand, then re-run on every change:

    ggcode eval testdata/session.tape.json testdata/session.eval.json

Exits non-zero when any check fails (or the tape/spec cannot be loaded).`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			tapePath, specPath := args[0], args[1]

			tape, err := toolreplay.LoadTape(tapePath)
			if err != nil {
				return fmt.Errorf("load tape: %w", err)
			}
			spec, err := tapeeval.LoadSpec(specPath)
			if err != nil {
				return err
			}

			report := tapeeval.Evaluate(tape.Entries(), spec)
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return fmt.Errorf("encode report: %w", err)
				}
			} else {
				fmt.Print(tapeeval.FormatReport(report, tapePath, specPath))
			}
			if !report.Pass {
				return fmt.Errorf("eval %q FAILED: %d/%d checks passed", spec.Name, report.Passed(), len(report.Results))
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "emit the report as JSON")
	cmd.AddCommand(newEvalInitCmd())
	return cmd
}

// newEvalInitCmd scaffolds an eval spec from a recorded tape: the
// "capability eval graduates into a regression eval" workflow.
func newEvalInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init <tape.json> [spec.json]",
		Short: "Scaffold an eval spec from a recorded tape",
		Long: `Derives a baseline eval spec from a recorded tape. The scaffold passes on
its own source tape by construction: observed per-tool call counts become
required minimums, the call total becomes the call budget, and the observed
error count becomes the error budget.

Tighten by hand afterwards: drop brittle per-tool minimums, shrink budgets,
add input_regex anchors to pin call parameters.`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tapePath := args[0]
			outPath := ""
			if len(args) == 2 {
				outPath = args[1]
			} else {
				base := strings.TrimSuffix(tapePath, ".json")
				if base == tapePath {
					base = tapePath
				}
				outPath = base + ".eval.json"
			}

			if _, err := os.Stat(outPath); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", outPath)
			}

			tape, err := toolreplay.LoadTape(tapePath)
			if err != nil {
				return fmt.Errorf("load tape: %w", err)
			}
			entries := tape.Entries()
			if len(entries) == 0 {
				return fmt.Errorf("tape %s has no recorded entries", tapePath)
			}

			name := strings.TrimSuffix(filepath.Base(outPath), ".eval.json")
			if name == filepath.Base(outPath) {
				name = filepath.Base(outPath)
			}
			spec := tapeeval.Scaffold(entries, name)

			data, err := json.MarshalIndent(spec, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal spec: %w", err)
			}
			data = append(data, '\n')
			if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}

			fmt.Printf("wrote %s (%d required tools, max_tool_calls=%d, max_error_results=%d)\n",
				outPath, len(spec.RequiredTools), spec.MaxToolCalls, spec.MaxErrorResults)
			fmt.Printf("review and tighten it, then: ggcode eval %s %s\n", tapePath, outPath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing spec file")
	return cmd
}
