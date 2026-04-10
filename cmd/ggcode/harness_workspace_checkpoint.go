package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/topcheer/ggcode/internal/harness"
)

func newHarnessCheckpointConfirmer(in io.Reader, out io.Writer, interactive bool) harness.ConfirmDirtyWorkspaceFunc {
	var (
		asked    bool
		approved bool
	)
	return func(checkpoint harness.DirtyWorkspaceCheckpoint) (bool, error) {
		if asked {
			return approved, nil
		}
		asked = true
		ok, err := confirmHarnessWorkspaceCheckpoint(in, out, interactive, checkpoint)
		if err != nil {
			return false, err
		}
		approved = ok
		return approved, nil
	}
}

func confirmHarnessWorkspaceCheckpoint(in io.Reader, out io.Writer, interactive bool, checkpoint harness.DirtyWorkspaceCheckpoint) (bool, error) {
	if !interactive {
		return false, fmt.Errorf("dirty workspace needs a checkpoint commit before harness run; rerun interactively to confirm (%s)", checkpoint.Summary)
	}
	fmt.Fprintln(out, "Harness run found uncommitted or untracked project files in the main workspace.")
	fmt.Fprintf(out, "These changes will be checkpointed before creating a task worktree:\n- %s\n", checkpoint.Summary)
	fmt.Fprintf(out, "Commit message: %s\n", checkpoint.CommitMessage)
	fmt.Fprintln(out, "Continue? [y]es / [n]o")
	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "> ")
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no", "":
			fmt.Fprintln(out, "Harness run cancelled.")
			return false, nil
		default:
			fmt.Fprintln(out, "Please enter y or n.")
		}
	}
}
