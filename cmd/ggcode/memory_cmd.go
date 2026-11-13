package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// newMemoryCmd: manual trigger surface for the #5512 autonomous curator
// sweep. Weekly scheduling lives in the resident process (root.go); this
// command runs one round on demand, or the double-run go-live gate with
// --gate.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Persistent memory maintenance",
	}
	sweep := &cobra.Command{
		Use:   "sweep [--gate]",
		Short: "Run one autonomous LLM curator sweep over global memory (#5512)",
		Long: `Adjudicate every global memory entry with an LLM curator pass (main model):
keep / archive / rewrite / unpin / split, each with mandatory rationale, auto-applied
under archive-only safety rails. Audit trail lands in sweep-audit.jsonl.

--gate runs the double-run consistency self-certification instead: two
independent judge passes; entry-level divergence resolves to keep, overall
divergence above 0.3 fails the gate and applies nothing.`,
		RunE: runMemorySweepCmd,
	}
	sweep.Flags().Bool("gate", false, "double-run consistency gate (go-live self-certification)")
	cmd.AddCommand(sweep)
	return cmd
}

// blocksToText concatenates text content blocks into plain text.
func blocksToText(blocks []provider.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" || blk.Text != "" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func runMemorySweepCmd(cmd *cobra.Command, args []string) error {
	gate, _ := cmd.Flags().GetBool("gate")
	cfgFile, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForRun(cfgFile, &adhocLLM)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	prov, _, err := ResolveProvider(cfg)
	if err != nil {
		return fmt.Errorf("resolve provider: %w", err)
	}
	// main model, not flash: empty curator model = provider default.
	am := memory.NewAutoMemory()
	judge := agentruntime.SweepJudgeFunc(func(jctx context.Context, prompt string) (string, error) {
		resp, cerr := prov.Chat(jctx, []provider.Message{{
			Role:    "user",
			Content: []provider.ContentBlock{provider.TextBlock(prompt)},
		}}, nil)
		if cerr != nil {
			return "", cerr
		}
		return blocksToText(resp.Message.Content), nil
	})
	opts := agentruntime.SweepOptions{AuditBase: am.Dir(), DryRunRounds: cfg.MemoryCuratorSweepDryRunRounds(), ForceDryRun: cfg.MemoryCuratorSweepForceDryRun()}
	if gate {
		rep, ok, gerr := agentruntime.RunMemorySweepGate(context.Background(), am, judge, opts)
		if gerr != nil {
			return gerr
		}
		if rep.Skipped {
			fmt.Fprintln(os.Stdout, "sweep gate: concurrency slot held by another instance - skipped")
			return nil
		}
		if !ok {
			fmt.Fprintf(os.Stdout, "sweep gate FAILED (divergence > 0.3 over %d entries) - nothing applied\n", rep.Entries)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "sweep gate PASS: %d entries, %d applied, %d kept, %d noop, %d deferred\n",
			rep.Entries, rep.Applied, rep.Kept, rep.Noop, len(rep.Deferred))
		return nil
	}
	rep, serr := agentruntime.RunMemorySweep(context.Background(), am, judge, opts)
	if serr != nil {
		return serr
	}
	if rep.Skipped {
		fmt.Fprintln(os.Stdout, "sweep: concurrency slot held by another instance - skipped")
		return nil
	}
	fmt.Fprintf(os.Stdout, "sweep: %d entries judged, %d applied, %d kept, %d noop, %d deferred (audit: %s)%s",
		rep.Entries, rep.Applied, rep.Kept, rep.Noop, len(rep.Deferred), rep.AuditPath, dryRunSuffix(rep))
	_ = debug.Log // keep debug linked for the manual path
	return nil
}

// dryRunSuffix renders the #5527 dry-run observation state for the manual
// sweep command so operators can tell observe-only rounds from applied
// ones at a glance.
func dryRunSuffix(rep *agentruntime.SweepReport) string {
	if rep == nil || !rep.DryRun {
		return "\n"
	}
	return " [DRY-RUN: verdict table only, store untouched (#5527)]\n"
}
