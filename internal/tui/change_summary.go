package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/diff"
)

func (m *Model) appendRunChangeSummary() {
	if m.agent == nil {
		return
	}
	stats := m.agent.LastRunStats()
	if stats == nil || len(stats.FilesEdited) == 0 {
		return
	}

	cpMgr := m.agent.CheckpointManager()

	runFiles := make(map[string]bool, len(stats.FilesEdited))
	for _, f := range stats.FilesEdited {
		runFiles[f] = true
	}

	var changes map[string]*fileChange
	if cpMgr != nil {
		changes = accumulateRunChanges(cpMgr.List(), stats.RunID(), runFiles)
	}
	if changes == nil {
		changes = make(map[string]*fileChange)
	}

	for _, f := range stats.FilesEdited {
		if _, ok := changes[f]; !ok {
			changes[f] = &fileChange{isNew: false, edits: 0}
		}
	}

	if len(changes) == 0 {
		return
	}

	workingDir := m.agent.WorkingDir()
	totalAdded := 0
	totalDeleted := 0
	fileCount := len(changes)
	var lines []string

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

type fileChange struct {
	added   int
	deleted int
	isNew   bool
	edits   int
}

func accumulateRunChanges(cps []checkpoint.Checkpoint, runID string, runFiles map[string]bool) map[string]*fileChange {

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

func shortenPath(absPath, workingDir string) string {
	if workingDir != "" {

		if rel, err := filepath.Rel(workingDir, absPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return rel
		}
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {

		if absPath == home || strings.HasPrefix(absPath, home+string(os.PathSeparator)) {
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
