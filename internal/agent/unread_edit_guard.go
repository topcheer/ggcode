package agent

import (
	"fmt"

	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type unreadEditState struct {
	filesRead map[string]bool

	filesCreated map[string]bool

	warnedFiles map[string]bool

	readMtime map[string]time.Time

	baseDir string
}

func newUnreadEditState() *unreadEditState {
	return &unreadEditState{
		filesRead:    make(map[string]bool),
		filesCreated: make(map[string]bool),
		warnedFiles:  make(map[string]bool),
		readMtime:    make(map[string]time.Time),
	}
}

func (s *unreadEditState) reset() {
	s.filesRead = make(map[string]bool)
	s.filesCreated = make(map[string]bool)
	s.warnedFiles = make(map[string]bool)
	s.readMtime = make(map[string]time.Time)
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimRight(p, "/")
	return p
}

func (s *unreadEditState) normalize(path string) string {
	return normalizeCompanionPath(s.baseDir, strings.TrimSpace(path))
}

func (s *unreadEditState) recordRead(path string) {
	s.recordReadWindow(path, false)
}

func (s *unreadEditState) recordReadWindow(path string, windowed bool) {
	if path == "" {
		return
	}
	n := s.normalize(path)
	if !windowed {
		s.filesRead[n] = true
	}

	delete(s.warnedFiles, n+"\x00stale")

	if info, err := os.Stat(path); err == nil {
		s.readMtime[n] = info.ModTime()
	}
}

func (s *unreadEditState) recordWrite(path string) {
	if path == "" {
		return
	}
	n := s.normalize(path)
	s.readMtime[n] = time.Now()
	delete(s.warnedFiles, n+"\x00stale")
}

func (s *unreadEditState) recordCreated(path string) {
	if path == "" {
		return
	}
	s.filesCreated[s.normalize(path)] = true
}

func (s *unreadEditState) hasBeenRead(path string) bool {
	n := s.normalize(path)
	return s.filesRead[n] || s.filesCreated[n]
}

func (s *unreadEditState) checkUnreadEdit(path string) string {
	if path == "" {
		return ""
	}
	n := s.normalize(path)
	if s.filesRead[n] || s.filesCreated[n] {
		return ""
	}
	if s.warnedFiles[n] {
		return ""
	}
	s.warnedFiles[n] = true
	debug.Log("agent", "unread-edit guard: editing %s without prior read", n)
	// Name the file: in multi-file edits N identical nameless lines are
	// indistinguishable (issue #5374).
	return fmt.Sprintf("You are editing %s without reading it first in this session. Read it with read_file to ensure your edit targets the actual content.", path)
}

func (s *unreadEditState) checkStaleRead(path string) string {
	if path == "" {
		return ""
	}
	n := s.normalize(path)

	readAt, wasRead := s.readMtime[n]
	if !wasRead {
		return ""
	}

	if s.filesCreated[n] {
		return ""
	}

	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	currentMtime := info.ModTime()
	if !currentMtime.After(readAt) {
		return ""
	}

	if s.warnedFiles[n+"\x00stale"] {
		return ""
	}
	s.warnedFiles[n+"\x00stale"] = true

	elapsed := currentMtime.Sub(readAt).Round(time.Second)
	debug.Log("agent", "stale-read detection: %s was modified on disk %v after last read", n, elapsed)
	return "Warning: this file was modified on disk since you last read it (changed ~" +
		elapsed.String() + " ago). Your old_text may not match the current file content. " +
		"Re-read the file with read_file before editing to avoid clobbering external changes."
}

func extractObjectArrayPaths(m map[string]any) []string {
	files, ok := m["files"].([]any)
	if !ok {
		return nil
	}
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

func extractStringArrayPaths(m map[string]any) []string {
	files, ok := m["files"].([]any)
	if !ok {
		return nil
	}
	var paths []string
	for _, f := range files {
		if s, ok := f.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths
}

// extractEditFilePaths is the GUARD view (#5240): file_ops is excluded
// because delete paths are never previously read and the unread-edit
// guard must not fire on batch deletes.
func extractEditFilePaths(toolName string, args json.RawMessage) []string {
	return extractEditPaths(toolName, args, false)
}

// extractTrackedEditPaths is the TRACKER view (#5242): the post-edit
// recorders (freshness/readHash/redundantRead/editFailRecovery) and the
// failure counter DO need file_ops paths - a move's destination must be
// registered as freshly written, and a successful file_ops must reset
// the consecutive-edit-failure counter. Without this, #5240's guard fix
// silently starved three tracker call sites.
func extractTrackedEditPaths(toolName string, args json.RawMessage) []string {
	return extractEditPaths(toolName, args, true)
}

func extractEditPaths(toolName string, args json.RawMessage, includeFileOps bool) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	m = normalizeApplyPatchArgs(toolName, m) // #5321: nested {"operation":{...}} must not bypass guards
	if includeFileOps && toolName == "file_ops" {
		return extractFileOpsPaths(m)
	}
	switch toolName {
	case "apply_patch": // #5244 top-level path; #5321 nested form normalized above
		return extractSinglePath(m, "path")
	case "edit_file":
		return extractSinglePath(m, "file_path", "path")
	case "multi_edit_file":
		return extractSinglePath(m, "file_path")
	case "multi_file_edit":
		return extractObjectArrayPaths(m)
	case "batch_replace":
		return extractStringArrayPaths(m)
	case "write_file":
		return extractSinglePath(m, "path")
	case "multi_file_write":
		return extractObjectArrayPaths(m)
	case "lsp_rename":
		return extractSinglePath(m, "path")
	case "notebook_edit":
		return extractSinglePath(m, "notebook_path")
	}
	// file_ops reaches here only in the guard view (includeFileOps=false):
	// its delete/move/mkdir operations are not content edits, so the
	// unread-edit guard ("you are editing without reading") does not apply
	// - it was misfiring twice on every batch delete (delete paths are
	// never previously read). The tracker view handled it above.

	return nil
}

// extractFileOpsPaths flattens file_ops operations into tracked paths:
// move destinations AND sources (destination = fresh write; source =
// path the write displaced), delete sources.
func extractFileOpsPaths(m map[string]any) []string {
	ops, ok := m["operations"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, op := range ops {
		o, ok := op.(map[string]any)
		if !ok {
			continue
		}
		action, _ := o["action"].(string)
		switch action {
		case "move":
			if d, _ := o["destination"].(string); d != "" {
				out = append(out, d)
			}
			if src, _ := o["source"].(string); src != "" {
				out = append(out, src)
			}
		case "delete":
			if src, _ := o["source"].(string); src != "" {
				out = append(out, src)
			}
		}
	}
	return out
}

func extractSinglePath(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		if p, ok := m[k].(string); ok {
			return []string{p}
		}
	}
	return nil
}

func readArgsHaveWindow(args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}
	var m struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
		Files  []struct {
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
		} `json:"files"`
	}
	if json.Unmarshal(args, &m) != nil {
		return false
	}
	if m.Offset > 0 || m.Limit > 0 {
		return true
	}
	for _, f := range m.Files {
		if f.Offset > 0 || f.Limit > 0 {
			return true
		}
	}
	return false
}

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

func extractCreateFilePaths(toolName string, args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	m = normalizeApplyPatchArgs(toolName, m) // #5321: nested apply_patch create_file must register too
	switch toolName {
	case "write_file":
		if p, ok := m["path"].(string); ok {
			return []string{p}
		}
	case "apply_patch": // #5249: create_file targets a brand-new file - exempt from the
		// unread-edit nag exactly like write_file (recordCreated parity); update_file
		// and delete_file keep returning nothing so the guard still applies to them.
		if t, _ := m["type"].(string); t == "create_file" {
			if p, ok := m["path"].(string); ok {
				return []string{p}
			}
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
