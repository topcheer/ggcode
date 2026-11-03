package agent

import (
	"fmt"
	"strings"
)

type undoBlindState struct {
	pendingUndoFiles map[string]bool
	warnCount        int
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

func undoBlindIsUndoOp(toolName string) bool {
	switch toolName {
	case "undo_edit", "git_revert", "git_reset", "git_checkout", "git_stash":
		return true
	}
	return false
}

func undoBlindIsMutation(toolName string) bool {
	return sourceMutatingTools[toolName]
}

func undoBlindIsRead(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "git_show", "git_diff":
		return true
	}
	return false
}

func (s *undoBlindState) recordToolCall(toolName string, argsJSON []byte) string {

	if undoBlindIsUndoOp(toolName) {
		fp := undoBlindExtractFilePath(toolName, argsJSON)
		if fp != "" {
			s.pendingUndoFiles[fp] = true
		} else {

			if toolName == "git_reset" || toolName == "git_stash" || toolName == "git_checkout" ||
				toolName == "git_revert" || toolName == "undo_edit" {

				if !undoBlindArgsHasFilePath(argsJSON) {

					s.pendingUndoFiles["*"] = true
				}
			}
		}
		return ""
	}

	if undoBlindIsRead(toolName) {
		fp := undoBlindExtractFilePath(toolName, argsJSON)
		if fp != "" {
			delete(s.pendingUndoFiles, fp)
		}

		return ""
	}

	if undoBlindIsMutation(toolName) {
		fp := undoBlindExtractFilePath(toolName, argsJSON)

		if s.pendingUndoFiles["*"] {
			if s.warnCount < undoBlindMaxWarns {
				s.warnCount++
				delete(s.pendingUndoFiles, "*")
				return fmt.Sprintf(
					"[undo-blind] A tree-wide revert/reset was performed and this edit ran without " +
						"re-reading the affected files first - its anchors may have come from the " +
						"pre-revert contents. Read the file(s) you just edited back NOW, verify the " +
						"result is what you intended, and re-read before every further edit.",
				)
			}
		}

		if fp != "" && s.pendingUndoFiles[fp] {
			if s.warnCount < undoBlindMaxWarns {
				s.warnCount++
				delete(s.pendingUndoFiles, fp)
				return fmt.Sprintf(
					"[undo-blind] This edit to %s ran after an undo/revert on the file without "+
						"a re-read - it may have used stale pre-revert anchors. Read %s back NOW "+
						"and verify what actually landed; re-read before every further edit.",
					fp, fp,
				)
			}

			delete(s.pendingUndoFiles, fp)
		}

		if toolName == "multi_edit_file" || toolName == "multi_file_write" || toolName == "multi_file_edit" {
			paths := undoBlindExtractAllPaths(argsJSON)
			for _, p := range paths {
				if s.pendingUndoFiles[p] {
					if s.warnCount < undoBlindMaxWarns {
						s.warnCount++
						delete(s.pendingUndoFiles, p)
						return fmt.Sprintf(
							"[undo-blind] This edit to %s ran after an undo/revert on the file "+
								"without a re-read. Read %s back and verify what landed.",
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

func undoBlindExtractFilePath(toolName string, argsJSON []byte) string {
	if len(argsJSON) == 0 {
		return ""
	}

	fp := extractFilePathFromArgs(toolName, argsJSON)
	return fp
}

func undoBlindArgsHasFilePath(argsJSON []byte) bool {
	s := string(argsJSON)
	return strings.Contains(s, "file_path") || strings.Contains(s, "\"path\"")
}

func undoBlindExtractAllPaths(argsJSON []byte) []string {
	s := string(argsJSON)
	var paths []string

	idx := 0
	for {
		pos := strings.Index(s[idx:], `"path":`)
		if pos < 0 {
			break
		}
		pos += idx

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
