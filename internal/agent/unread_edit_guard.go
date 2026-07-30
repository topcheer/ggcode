package agent

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Unread-file edit guard.
//
// Research basis: Claude Code and Cursor both enforce "read-before-edit"
// semantics. When an agent edits a file it hasn't read in the current
// session, it is operating on assumed content — a leading cause of failed
// edits and broken code. Aider similarly warns when editing files not in
// its chat context.
//
// This guard tracks files read via read_file/multi_file_read during the
// current run. When an edit tool targets a file that was never read (or
// was read so long ago it was likely compacted away), it injects a concise
// hint to re-read before editing.
//
// Design:
//   - Tracks normalized file paths in a set, reset each run.
//   - Fires at most once per file per run (avoids nagging).
//   - Only checks edit_file and multi_edit_file (NOT write_file — creating
//     new files doesn't require prior reads).
//   - multi_file_edit: checks each file in the batch.
//   - Skips files created by write_file earlier in the run (agent knows
//     their content from creating them).

// unreadEditState tracks which files have been read during the current run.
type unreadEditState struct {
	// filesRead stores normalized paths of files the agent has read.
	filesRead map[string]bool

	// filesCreated stores normalized paths of files the agent created
	// via write_file during this run. These don't need prior reads.
	filesCreated map[string]bool

	// warnedFiles tracks files for which the unread-edit hint has fired.
	// Prevents duplicate warnings for the same file within a run.
	warnedFiles map[string]bool
}

func newUnreadEditState() *unreadEditState {
	return &unreadEditState{
		filesRead:    make(map[string]bool),
		filesCreated: make(map[string]bool),
		warnedFiles:  make(map[string]bool),
	}
}

func (s *unreadEditState) reset() {
	s.filesRead = make(map[string]bool)
	s.filesCreated = make(map[string]bool)
	s.warnedFiles = make(map[string]bool)
}

// normalizePath converts a file path to a consistent form for comparison.
// Lowercased and trimmed of trailing slashes — sufficient for dedup.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, "/")
	return p
}

// recordRead marks a file as read. Called after successful read_file/multi_file_read.
func (s *unreadEditState) recordRead(path string) {
	if path == "" {
		return
	}
	s.filesRead[normalizePath(path)] = true
}

// recordCreated marks a file as created via write_file. These are exempt
// from the unread-edit check since the agent authored their content.
func (s *unreadEditState) recordCreated(path string) {
	if path == "" {
		return
	}
	s.filesCreated[normalizePath(path)] = true
}

// hasBeenRead returns true if the file was read or created during this run.
func (s *unreadEditState) hasBeenRead(path string) bool {
	n := normalizePath(path)
	return s.filesRead[n] || s.filesCreated[n]
}

// checkUnreadEdit returns a non-empty hint if the agent is editing a file
// it hasn't read in this run. Returns "" if the file was read, created,
// or already warned about.
func (s *unreadEditState) checkUnreadEdit(path string) string {
	if path == "" {
		return ""
	}
	n := normalizePath(path)
	if s.filesRead[n] || s.filesCreated[n] {
		return ""
	}
	if s.warnedFiles[n] {
		return ""
	}
	s.warnedFiles[n] = true
	debug.Log("agent", "unread-edit guard: editing %s without prior read", n)
	return "You are editing this file without reading it first in this session. Read it with read_file to ensure your edit targets the actual content."
}

// extractEditFilePaths returns file paths from edit tool arguments.
func extractEditFilePaths(toolName string, args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	switch toolName {
	case "edit_file":
		if p, ok := m["file_path"].(string); ok {
			return []string{p}
		}
		if p, ok := m["path"].(string); ok {
			return []string{p}
		}
	case "multi_edit_file":
		if p, ok := m["file_path"].(string); ok {
			return []string{p}
		}
	case "multi_file_edit":
		if files, ok := m["files"].([]any); ok {
			var paths []string
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
			return paths
		}
	}
	return nil
}

// extractReadFilePaths returns file paths from read tool arguments.
func extractReadFilePaths(toolName string, args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	switch toolName {
	case "read_file":
		if p, ok := m["path"].(string); ok {
			return []string{p}
		}
	case "multi_file_read":
		if files, ok := m["files"].([]any); ok {
			var paths []string
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
			return paths
		}
	}
	return nil
}

// extractCreateFilePaths returns the path from write_file/multi_file_write.
func extractCreateFilePaths(toolName string, args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	switch toolName {
	case "write_file":
		if p, ok := m["path"].(string); ok {
			return []string{p}
		}
	case "multi_file_write":
		if files, ok := m["files"].([]any); ok {
			var paths []string
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p, ok := fm["path"].(string); ok {
						paths = append(paths, p)
					}
				}
			}
			return paths
		}
	}
	return nil
}
