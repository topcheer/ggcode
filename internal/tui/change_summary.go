package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/diff"
)

// appendRunChangeSummary displays a concise summary of files changed during
// the agent run that just completed. This gives users an at-a-glance view of
// what the agent modified without needing to scroll through tool calls or
// run /diff manually.
//
// Competitor feature parity:
//   - Claude Code: shows changed files summary after agent runs
//   - Cursor: shows a diff panel after Composer runs
//   - Aider: shows git diff stats after each edit
//
// Our implementation is zero-cost (no git subprocess, no LLM call): it
// cross-references RunStats.FilesEdited with checkpoint data to produce
// a compact "M path (+N -M)" / "A path (new)" listing.
func (m *Model) appendRunChangeSummary() {
	if m.agent == nil {
		return
	}
	stats := m.agent.LastRunStats()
	if stats == nil || len(stats.FilesEdited) == 0 {
		return
	}

	cpMgr := m.agent.CheckpointManager()

	// Build a set of files edited in this run for quick lookup.
	runFiles := make(map[string]bool, len(stats.FilesEdited))
	for _, f := range stats.FilesEdited {
		runFiles[f] = true
	}

	// Gather checkpoint data per file, filtered to the run that just
	// finished. See accumulateRunChanges for the accounting rules.
	var changes map[string]*fileChange
	if cpMgr != nil {
		changes = accumulateRunChanges(cpMgr.List(), stats.RunID(), runFiles)
	}
	if changes == nil {
		changes = make(map[string]*fileChange)
	}

	// For files with no checkpoint data (e.g., checkpoint evicted), still
	// show them in the summary without line counts.
	for _, f := range stats.FilesEdited {
		if _, ok := changes[f]; !ok {
			changes[f] = &fileChange{isNew: false, edits: 0}
		}
	}

	if len(changes) == 0 {
		return
	}

	// Build the summary text.
	workingDir := m.agent.WorkingDir()
	totalAdded := 0
	totalDeleted := 0
	fileCount := len(changes)
	var lines []string

	// Sort: new files first, then modified, alphabetical within each group.
	sortedFiles := make([]string, 0, fileCount)
	newFiles := make([]string, 0)
	modFiles := make([]string, 0)
	for f := range changes {
		if changes[f].isNew {
			newFiles = append(newFiles, f)
		} else {
			modFiles = append(modFiles, f)
		}
	}
	sortedFiles = append(sortedFiles, sortStrings(newFiles)...)
	sortedFiles = append(sortedFiles, sortStrings(modFiles)...)

	for _, f := range sortedFiles {
		fc := changes[f]
		display := shortenPath(f, workingDir)
		if fc.isNew {
			lines = append(lines, fmt.Sprintf("  A  %s  (new file)", display))
		} else if fc.added > 0 || fc.deleted > 0 {
			lines = append(lines, fmt.Sprintf("  M  %s  (+%d -%d)", display, fc.added, fc.deleted))
		} else {
			lines = append(lines, fmt.Sprintf("  M  %s", display))
		}
		totalAdded += fc.added
		totalDeleted += fc.deleted
	}

	header := fmt.Sprintf("Files changed in this run (%d %s, +%d -%d):",
		fileCount, pluralFile(fileCount), totalAdded, totalDeleted)

	summary := header + "\n" + strings.Join(lines, "\n")
	m.chatWriteSystem(nextSystemID(), summary)
}

// fileChange is the net change for one file across a single agent run.
type fileChange struct {
	added   int
	deleted int
	isNew   bool
	edits   int
}

// accumulateRunChanges computes per-file net changes for one agent run
// from the checkpoint log (issue #541). It fixes three accounting bugs of
// the previous implementation:
//
//   - Only the first checkpoint per file was counted, so multiple edits to
//     the same file systematically under-reported the totals. The net diff
//     now runs from the run's first checkpoint's OldContent to its LAST
//     checkpoint's NewContent, accumulating every edit.
//   - Checkpoints were not filtered by RunID, so earlier runs' checkpoints
//     contaminated the current run's summary. Only checkpoints created by
//     runID are counted now.
//   - isNew treated any empty pre-edit content as "new file". A file that
//     existed with content and was emptied before the run also has empty
//     OldContent; such files are recognized via older checkpoints that saw
//     non-empty content and are reported as modified, not new.
func accumulateRunChanges(cps []checkpoint.Checkpoint, runID string, runFiles map[string]bool) map[string]*fileChange {
	// Files that any checkpoint (any run) saw with non-empty content before
	// an edit: proof the file existed before it was possibly emptied.
	// #883: exclude the CURRENT run's checkpoints — a file created by
	// write_file then edited by edit_file in the same run has non-empty
	// OldContent at cp2, which wrongly counted as pre-existing evidence and
	// reported the file as Modified instead of Added. The main loop applies
	// the same RunID filter (see below).
	existedWithContent := make(map[string]bool)
	for _, cp := range cps {
		if runID != "" && cp.RunID == runID {
			continue
		}
		if cp.OldContent != "" {
			existedWithContent[cp.FilePath] = true
		}
	}

	changes := make(map[string]*fileChange)
	firstOld := make(map[string]string)
	lastNew := make(map[string]string)
	for _, cp := range cps {
		if !runFiles[cp.FilePath] {
			continue
		}
		if runID != "" && cp.RunID != runID {
			continue
		}
		if _, ok := firstOld[cp.FilePath]; !ok {
			firstOld[cp.FilePath] = cp.OldContent
		}
		lastNew[cp.FilePath] = cp.NewContent
		if changes[cp.FilePath] == nil {
			changes[cp.FilePath] = &fileChange{}
		}
		changes[cp.FilePath].edits++
	}
	for f, fc := range changes {
		old, nw := firstOld[f], lastNew[f]
		fc.added, fc.deleted = diff.CountChanges(old, nw)
		fc.isNew = old == "" && !existedWithContent[f]
	}
	return changes
}

// shortenPath converts an absolute path to a project-relative one for display.
func shortenPath(absPath, workingDir string) string {
	if workingDir != "" {
		if rel, err := filepath.Rel(workingDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	// Fall back to ~ if under home.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if strings.HasPrefix(absPath, home) {
			return "~" + absPath[len(home):]
		}
	}
	return absPath
}

func pluralFile(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

// sortStrings returns a sorted copy of the input slice.
func sortStrings(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
