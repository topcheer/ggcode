package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/audit"
)

// auditLedgerEnvPath mirrors internal/agent's auditLedgerEnv: the ledger is
// opt-in via GGCODE_AUDIT_LEDGER, so the same variable is the default source
// for reading one back.
const auditCmdEnv = "GGCODE_AUDIT_LEDGER"

// newAuditCmd exposes the audit ledger's reader side. The ledger itself has
// existed since the governance-audit work (hash-chained, append-only), but
// nothing in the product consumed it after the fact: "the log exists, the
// reader is missing". `audit report` reconstructs what a run actually did
// (approvals, invariant rejections, A2A handoffs, error clusters, per-tool
// rollups) together with the chain's tamper state.
func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Read the tamper-evident agent audit ledger",
		Long:  "Read the tamper-evident agent audit ledger.\n\nThe ledger is written when GGCODE_AUDIT_LEDGER points at a file during a\nsession; these commands reconstruct and verify it after the fact.",
	}

	var (
		session string
		pathArg string
		files   string
	)
	reportCmd := &cobra.Command{
		Use:   "report",
		Short: "Reconstruct a run from the audit ledger as a human-readable report",
		Long: "Reconstruct a run from the audit ledger as a human-readable report:\n" +
			"human-gate decisions, invariant rejections, A2A peer attribution,\n" +
			"per-tool activity, error clusters, and the chain's verification state.\n\n" +
			"The ledger path defaults to $GGCODE_AUDIT_LEDGER (the same variable\n" +
			"that enables writing it); override with --path.\n\n" +
			"Pass the run's changed files (--files, comma-separated) to get a\n" +
			"blast-radius review-depth hint: copy < code < critical.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := strings.TrimSpace(pathArg)
			if path == "" {
				path = strings.TrimSpace(os.Getenv(auditCmdEnv))
			}
			if path == "" {
				return fmt.Errorf("no ledger path: pass --path or set %s", auditCmdEnv)
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("ledger not found at %s: %w", path, err)
			}
			var changed []string
			if files != "" {
				for _, f := range strings.Split(files, ",") {
					if f = strings.TrimSpace(f); f != "" {
						changed = append(changed, f)
					}
				}
			}
			d, err := audit.Digest(path, strings.TrimSpace(session))
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), d.Render(changed))
			return nil
		},
	}
	reportCmd.Flags().StringVar(&session, "session", "", "restrict the report to one session ID (default: all sessions)")
	reportCmd.Flags().StringVar(&pathArg, "path", "", "audit ledger JSONL path (default $"+auditCmdEnv+")")
	reportCmd.Flags().StringVar(&files, "files", "", "comma-separated changed files, used for the blast-radius review hint")

	cmd.AddCommand(reportCmd)
	return cmd
}
