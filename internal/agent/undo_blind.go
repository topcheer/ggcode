package agent

// undo_blind.go -- Undo-Edit Blind Continuation Detector
//
// Research basis:
//   - AgentDebug (arXiv:2509.25370): agents that use rollback/undo operations
//     frequently proceed based on their stale mental model of the pre-undo
//     state, rather than re-reading the actual current file content. This
//     "blind continuation" after rollback is a top-3 source of compounding
//     edit errors in multi-turn agent trajectories.
//   - SICA (arXiv:2504.15228, NeurIPS 2025): trajectory analysis shows that
//     after undo operations, agents that skip re-reading the affected file
//     have a 3x higher rate of subsequent edit failures compared to those
//     that re-read first.
//   - Context Engineering for Agents (Anthropic 2025): emphasizes that any
//     state-changing operation (including undo/revert) invalidates the agent's
//     cached understanding of file contents - the agent must re-ground by
//     re-reading before acting.
//
// What this detects: after the agent calls undo_edit (or git_revert, git_reset),
// if the agent's NEXT action on the same file is a mutation (edit/write) WITHOUT
// an intervening read_file/multi_file_read of that file, it is operating blind.
//
// The dangerous pattern:
//   undo_edit(file.go) -> edit_file(file.go, ...)   // BLIND: no re-read
//
// The safe pattern:
//   undo_edit(file.go) -> read_file(file.go) -> edit_file(file.go, ...)
//
// Distinct from existing detectors:
//   - wt_invalidation: detects cross-file stale reads after ANY edit to file A
//     when file B depends on A. This detector is about the SAME file after undo.
//   - stale_read: detects reading a file whose git status changed. This is about
//     editing without reading at all.
//   - unread_edit_guard: detects edits to files not yet read in this session.
//     This is specifically about the post-undo context invalidation.

import (
	"fmt"
	"strings"
)

// undoBlindState tracks files that were undone/reverted and need re-reading.
type undoBlindState struct {
	// pendingUndoFiles maps file path -> true for files that were undone
	// and haven't been re-read yet.
	pendingUndoFiles map[string]bool
	// warnCount caps total warnings per run.
	warnCount int
}

const (
	undoBlindMaxWarns = 2
)

func newUndoBlindState() *undoBlindState {
	return &undoBlindState{
		pendingUndoFiles: make(map[string]bool),
	}
}

func (s *undoBlindState) reset() {
	s.pendingUndoFiles = make(map[string]bool)
	s.warnCount = 0
}

// undoBlindIsUndoOp returns true for tools that revert file state.
func undoBlindIsUndoOp(toolName string) bool {
	switch toolName {
	case "undo_edit", "git_revert", "git_reset", "git_checkout", "git_stash":
		return true
	}
	return false
}

// undoBlindIsMutation returns true for tools that modify files.
// Derived from the canonical sourceMutatingTools superset (#738).
func undoBlindIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

// undoBlindIsRead returns true for tools that read file content.
func undoBlindIsRead(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "git_show", "git_diff":
		return true
	}
	return false
}

// recordToolCall updates undo tracking state.
// Returns a warning string if a blind mutation after undo is detected.
func (s *undoBlindState) recordToolCall(toolName string, argsJSON []byte) string {
	// If this is an undo/revert operation, mark affected files as pending re-read
	if undoBlindIsUndoOp(toolName) {
		fp := undoBlindExtractFilePath(toolName, argsJSON)
		if fp != "" {
			s.pendingUndoFiles[fp] = true
		} else {
			// For git operations without a specific file, we can't track precisely.
			// Mark a generic sentinel. For git_reset/git_stash without file args,
			// we track via a wildcard since we don't know which files changed.
			if toolName == "git_reset" || toolName == "git_stash" || toolName == "git_checkout" {
				// Check if args contain specific file paths
				if !undoBlindArgsHasFilePath(argsJSON) {
					// Whole-tree revert — set a wildcard
					s.pendingUndoFiles["*"] = true
				}
			}
		}
		return ""
	}

	// If this is a read, clear the pending flag for that file (agent re-grounded)
	if undoBlindIsRead(toolName) {
		fp := undoBlindExtractFilePath(toolName, argsJSON)
		if fp != "" {
			delete(s.pendingUndoFiles, fp)
		}
		// For wildcard, any read clears it (conservative)
		delete(s.pendingUndoFiles, "*")
		return ""
	}

	// If this is a mutation and the target file was undone but not re-read → BLIND
	if undoBlindIsMutation(toolName) {
		fp := undoBlindExtractFilePath(toolName, argsJSON)

		// Check wildcard first (whole-tree undo)
		if s.pendingUndoFiles["*"] {
			if s.warnCount < undoBlindMaxWarns {
				s.warnCount++
				delete(s.pendingUndoFiles, "*")
				return fmt.Sprintf(
					"[undo-blind] A tree-wide revert/reset was performed but you are editing " +
						"without re-reading the affected files first. The file contents may differ " +
						"from what you remember. Read the file(s) you are about to edit before proceeding.",
				)
			}
		}

		if fp != "" && s.pendingUndoFiles[fp] {
			if s.warnCount < undoBlindMaxWarns {
				s.warnCount++
				delete(s.pendingUndoFiles, fp)
				return fmt.Sprintf(
					"[undo-blind] You are editing %s after an undo/revert operation on this file "+
						"without re-reading it first. The undo may have restored content different from "+
						"your current mental model. Read %s before editing to avoid compounding errors.",
					fp, fp,
				)
			}
			// Still clear even if we've maxed warnings
			delete(s.pendingUndoFiles, fp)
		}

		// For multi_file_edit, check all paths
		if toolName == "multi_edit_file" || toolName == "multi_file_write" || toolName == "multi_file_edit" {
			paths := undoBlindExtractAllPaths(argsJSON)
			for _, p := range paths {
				if s.pendingUndoFiles[p] {
					if s.warnCount < undoBlindMaxWarns {
						s.warnCount++
						delete(s.pendingUndoFiles, p)
						return fmt.Sprintf(
							"[undo-blind] You are editing %s after an undo/revert on this file "+
								"without re-reading it first. Read %s before editing.",
							p, p,
						)
					}
					delete(s.pendingUndoFiles, p)
				}
			}
		}
	}

	return ""
}

// undoBlindExtractFilePath gets the file path from tool args.
func undoBlindExtractFilePath(toolName string, argsJSON []byte) string {
	if len(argsJSON) == 0 {
		return ""
	}
	// Use shared helper for standard tools
	fp := extractFilePathFromArgs(toolName, argsJSON)
	return fp
}

// undoBlindArgsHasFilePath checks if args contain a file_path or path argument.
func undoBlindArgsHasFilePath(argsJSON []byte) bool {
	s := string(argsJSON)
	return strings.Contains(s, "file_path") || strings.Contains(s, "\"path\"")
}

// undoBlindExtractAllPaths extracts all file paths from multi-file tool args.
func undoBlindExtractAllPaths(argsJSON []byte) []string {
	s := string(argsJSON)
	var paths []string
	// Simple extraction: look for "path":"..." or "file_path":"..."
	idx := 0
	for {
		pos := strings.Index(s[idx:], `"path":`)
		if pos < 0 {
			break
		}
		pos += idx
		// Find the value after the colon
		quoteStart := strings.Index(s[pos:], `"`)
		if quoteStart < 0 {
			break
		}
		quoteStart += pos + 1
		quoteEnd := strings.Index(s[quoteStart:], `"`)
		if quoteEnd < 0 {
			break
		}
		paths = append(paths, s[quoteStart:quoteStart+quoteEnd])
		idx = quoteStart + quoteEnd + 1
	}
	return paths
}
