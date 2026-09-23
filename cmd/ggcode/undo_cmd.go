package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/checkpoint"
)

// newUndoCmd builds `ggcode undo`: post-mortem rollback of the file edits a
// previous session made. ggcode records every file-tool edit to an on-disk
// undo store (<project>/.ggcode/undo/); this command replays the most recent
// session's records backwards to each file's pre-session state without
// resuming the session.
func newUndoCmd() *cobra.Command {
	var (
		listOnly  bool
		sessionID string
		assumeYes bool
		dryRun    bool
		dirFlag   string
	)
	cmd := &cobra.Command{
		Use:   "undo",
		Short: "Roll back file edits made by a previous ggcode session",
		Long: `Roll back file edits made by a previous ggcode session.

ggcode persists an on-disk record of every file edit its tools make
(edit_file, write_file, ...). "ggcode undo" shows what the most recent
session changed and restores each touched file to its pre-session state —
even after ggcode has exited.

Only edits made through ggcode tools are tracked. Changes executed via
shell commands (rm, git checkout, sed -i, ...) are not recorded and cannot
be rolled back.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFlag
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolving working directory: %w", err)
				}
				dir = checkpoint.DefaultPersistDir(wd)
			}
			if listOnly {
				return runUndoList(cmd, dir)
			}
			return runUndoRollback(cmd, dir, sessionID, assumeYes, dryRun)
		},
	}
	cmd.Flags().BoolVar(&listOnly, "list", false, "List persisted sessions and exit")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID to roll back (default: most recent)")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be rolled back without changing files")
	// dir overrides the undo store location; used by tests.
	cmd.Flags().StringVar(&dirFlag, "dir", "", "Override undo store directory")
	_ = cmd.Flags().MarkHidden("dir")
	return cmd
}

func runUndoList(cmd *cobra.Command, dir string) error {
	sessions, err := checkpoint.ListPersistedSessions(dir)
	if err != nil {
		return fmt.Errorf("listing undo store: %w", err)
	}
	w := cmd.OutOrStdout()
	if len(sessions) == 0 {
		fmt.Fprintf(w, "No persisted edit sessions in %s\n", dir)
		return nil
	}
	fmt.Fprintf(w, "Persisted edit sessions (newest first) in %s:\n", dir)
	for _, s := range sessions {
		fmt.Fprintf(w, "  %-24s %s  %6.1f KB\n", s.ID, s.Modified.Format("2006-01-02 15:04"), float64(s.Size)/1024)
	}
	fmt.Fprintln(w, "\nRoll one back with: ggcode undo --session <id>")
	return nil
}

func runUndoRollback(cmd *cobra.Command, dir, sessionID string, assumeYes, dryRun bool) error {
	if sessionID == "" {
		sessions, err := checkpoint.ListPersistedSessions(dir)
		if err != nil {
			return fmt.Errorf("listing undo store: %w", err)
		}
		if len(sessions) == 0 {
			return fmt.Errorf("no persisted edit sessions in %s (ggcode records edits automatically while running)", dir)
		}
		sessionID = sessions[0].ID
	}
	records, err := checkpoint.LoadSessionRecords(dir, sessionID)
	if err != nil {
		return fmt.Errorf("loading session %s: %w", sessionID, err)
	}
	baselines := checkpoint.SessionBaselines(records)
	w := cmd.OutOrStdout()
	if len(baselines) == 0 {
		fmt.Fprintf(w, "Session %s recorded no file edits.\n", sessionID)
		return nil
	}

	fmt.Fprintf(w, "Session %s · %d file edit(s) across %d file(s)\n", sessionID, len(records), len(baselines))
	for _, b := range baselines {
		fmt.Fprintln(w, "  "+checkpoint.DescribeBaseline(b))
	}
	fmt.Fprintln(w, "\nOnly ggcode tool edits are tracked; shell-command changes are not.")

	if dryRun {
		fmt.Fprintln(w, "Dry run: no files changed.")
		return nil
	}

	if !assumeYes {
		fmt.Fprintf(w, "\nRoll back %d file(s)? [y/N] ", len(baselines))
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(w, "Aborted — no files changed.")
			return nil
		}
	}

	results := checkpoint.RollbackSessionBaselines(baselines)
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(w, "  FAILED   %s: %v\n", r.FilePath, r.Err)
			continue
		}
		fmt.Fprintf(w, "  %-8s %s\n", r.Action, r.FilePath)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d file(s) failed to roll back", failed, len(results))
	}
	fmt.Fprintf(w, "Rolled back %d file(s) to their pre-session state.\n", len(results))
	return nil
}
