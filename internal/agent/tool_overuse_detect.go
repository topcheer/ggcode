package agent

// Tool Overuse / Self-Awareness Detector
//
// Research basis:
//   - "The Tool-Overuse Illusion" (arXiv 2604.19749, 2026) identifies
//     "knowledge epistemic illusion" as a primary mechanism driving
//     unnecessary tool invocation - agents miscalibrate their internal
//     knowledge and call tools for information they already possess.
//   - KAPRO (arXiv 2606.20661, 2026) benchmarks the "Knowing-Acting"
//     quadrant, showing that self-awareness capability (knowing whether
//     a problem needs external tools vs internal knowledge) strongly
//     correlates with task success and degrades sharply in
//     internal-capability settings.
//   - SMART (2025) proposes self-aware agents that mitigate tool overuse
//     by recognizing when parametric knowledge suffices.
//
// Problem: AI coding agents frequently invoke tools to retrieve information
// already available in their context window:
//
//  1. Read-after-write: agent writes a file, then reads it back 1-2
//     iterations later. The new content is already in context from the
//     write/edit tool result. This wastes context tokens and an
//     iteration.
//  2. Directory re-list: agent calls list_directory on a path it
//     recently listed, with no files added or removed since.
//  3. Trivial command: agent runs version/status commands (go version,
//     git status, pwd) whose answers are in the system prompt's
//     environment block.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - redundant_read_guard.go: catches read→read of the same file, but
//     explicitly allows read after write (recordWrite invalidates cache).
//   - tool_redundancy.go: catches identical tool+args fingerprints, but
//     write→read uses different tools so it doesn't fire.
//   - loop_detect.go: catches consecutive identical calls only.
//
// Gap: No detector identifies the metacognitive failure of calling tools
// to retrieve information the agent just produced or already has. This
// detector addresses the "knowledge epistemic illusion" directly.
//
// Design:
//   - Tracks recently written/edited files with iteration timestamps
//   - When read_file targets a recently-written file (within N iterations),
//     injects guidance that the content is already in context
//   - Tracks directory listing timestamps; warns on unchanged re-lists
//   - Detects trivial version/status commands answerable from env block
//   - Zero LLM cost - pure deterministic heuristics
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"strings"
)

const (
	overuseReadAfterWriteWindow = 3 // iterations after write where read is suspicious
	overuseDirRelistWindow      = 4 // iterations after list where re-list is suspicious
	overuseMaxWarnings          = 2 // max warnings per run
)

type toolOveruseState struct {
	// filesWritten tracks files written/edited with the iteration number
	filesWritten map[string]int // path → iteration number

	// dirsListed tracks directory paths listed with iteration number
	dirsListed map[string]int // path → iteration number

	// dirsWritten tracks directories that received file writes (invalidates listing)
	dirsWritten map[string]int // dir → iteration number of last write

	warnings int
}

func newToolOveruseState() *toolOveruseState {
	return &toolOveruseState{
		filesWritten: make(map[string]int),
		dirsListed:   make(map[string]int),
		dirsWritten:  make(map[string]int),
	}
}

func (s *toolOveruseState) reset() {
	for k := range s.filesWritten {
		delete(s.filesWritten, k)
	}
	for k := range s.dirsListed {
		delete(s.dirsListed, k)
	}
	for k := range s.dirsWritten {
		delete(s.dirsWritten, k)
	}
	s.warnings = 0
}

// recordWrite tracks file write/edit operations. success reflects the
// tool result: only a SUCCESSFUL write makes later reads of the file
// "suspicious" (the content is in context). A FAILED edit leaves no write
// result in context — in fact the canonical recovery is to RE-READ the
// file (edit_fail_recovery recommends exactly that), so a failed write
// must clear any stale entry (#495).
func (s *toolOveruseState) recordWrite(path string, iter int, success bool) {
	path = overuseNormalizePath(path)
	if path == "" {
		return
	}
	if !success {
		delete(s.filesWritten, path)
		return
	}
	s.filesWritten[path] = iter
	// Also track the parent directory as modified
	if idx := strings.LastIndex(path, "/"); idx > 0 {
		dir := path[:idx]
		s.dirsWritten[dir] = iter
	}
}

// recordDirList tracks list_directory calls.
func (s *toolOveruseState) recordDirList(path string, iter int) {
	path = overuseNormalizePath(path)
	if path == "" {
		return
	}
	s.dirsListed[path] = iter
}

// checkReadAfterWrite detects when a read targets a recently-written file.
// Returns guidance message if the content is likely already in context.
func (s *toolOveruseState) checkReadAfterWrite(path string, iter int) string {
	if s.warnings >= overuseMaxWarnings {
		return ""
	}
	path = overuseNormalizePath(path)
	if path == "" {
		return ""
	}
	writeIter, ok := s.filesWritten[path]
	if !ok {
		return ""
	}
	if iter-writeIter > overuseReadAfterWriteWindow {
		return ""
	}
	// File was written recently and not invalidated by another write
	s.warnings++
	return fmt.Sprintf(
		"[tool-overuse] You wrote/edited %s at iteration %d (%d iteration(s) ago). "+
			"The current content is already in your context from the write result. "+
			"Re-reading wastes context tokens and an iteration. "+
			"Trust the content from your edit and proceed with the next action. "+
			"If you need to verify the edit applied, use git diff instead.",
		path, writeIter, iter-writeIter,
	)
}

// checkDirRelist detects unchanged directory re-listing.
func (s *toolOveruseState) checkDirRelist(path string, iter int) string {
	if s.warnings >= overuseMaxWarnings {
		return ""
	}
	path = overuseNormalizePath(path)
	if path == "" {
		return ""
	}
	listIter, ok := s.dirsListed[path]
	if !ok {
		return ""
	}
	// Check if directory was listed recently
	if iter-listIter > overuseDirRelistWindow {
		return ""
	}
	// Check if no writes occurred in this directory since the listing
	writeIter, hadWrite := s.dirsWritten[path]
	if hadWrite && writeIter > listIter {
		return "" // Directory was modified since listing, re-list is justified
	}
	s.warnings++
	return fmt.Sprintf(
		"[tool-overuse] You listed directory %s at iteration %d (%d iteration(s) ago) "+
			"and no files were added or removed since. The listing is still in your context. "+
			"Re-listing wastes context tokens. Proceed based on the known directory structure.",
		path, listIter, iter-listIter,
	)
}

// trivialCommandPatterns maps command substrings that are typically
// answerable from the system prompt environment block or toolchain info.
var trivialCommandPatterns = []string{
	"go version",
	"git --version",
	"node --version",
	"node -v",
	"npm --version",
	"npm -v",
	"python --version",
	"python3 --version",
	"pwd",
	"whoami",
	"uname",
	"rustc --version",
	"cargo --version",
	"which go",
	"which node",
	"which python",
}

// checkTrivialCommand detects commands whose output is already in the
// system prompt environment block.
func (s *toolOveruseState) checkTrivialCommand(cmd string) string {
	if s.warnings >= overuseMaxWarnings {
		return ""
	}
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))
	for _, pat := range trivialCommandPatterns {
		// #495: whole-word-sequence match. Bare substring matching fired
		// on embedded text (cat .pwd_history, ls /tmp/uname-dir,
		// "which python3-config" style args).
		if overuseContainsCommand(cmdLower, pat) {
			s.warnings++
			return fmt.Sprintf(
				"[tool-overuse] The command `%s` retrieves information typically available "+
					"in your system prompt's environment/toolchain section (Go version, OS, etc.). "+
					"Check the environment block at the top of your context before running "+
					"version/status commands - this saves an iteration and context tokens.",
				strings.TrimSpace(cmd),
			)
		}
	}
	return ""
}

// overuseContainsCommand reports whether the command contains pat as a
// whole word sequence: the characters adjacent to the match must not be
// word characters (#495). "pwd" matches "cd somewhere && pwd" but not
// "cat .pwd_history"; "uname" matches "uname -a" but not "/tmp/uname-dir".
func overuseContainsCommand(cmdLower, pat string) bool {
	for i := 0; i+len(pat) <= len(cmdLower); i++ {
		if cmdLower[i:i+len(pat)] != pat {
			continue
		}
		if i > 0 && overuseIsWordByte(cmdLower[i-1]) {
			continue
		}
		if i+len(pat) < len(cmdLower) && overuseIsWordByte(cmdLower[i+len(pat)]) {
			continue
		}
		return true
	}
	return false
}

func overuseIsWordByte(c byte) bool {
	return c == '_' || c == '-' || c == '/' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// maybeWarn checks for tool overuse patterns given a tool call. Called
// PRE-execution, so it performs only the read-only checks (read-after-
// write, dir re-list, trivial command). Write bookkeeping moved to
// recordWriteResult, which sees the actual tool outcome (#495): the old
// pre-execution recordWrite counted FAILED edits as writes, so the
// recovery read_file after a failed edit got false-premise "trust the
// content from your edit" guidance contradicting edit_fail_recovery.
func (s *toolOveruseState) maybeWarn(toolName, argsJSON string, iter int) string {
	path := extractPathFromArgs(toolName, argsJSON)
	switch toolName {
	case "read_file":
		return s.checkReadAfterWrite(path, iter)
	case "list_directory":
		msg := s.checkDirRelist(path, iter)
		s.recordDirList(path, iter)
		return msg
	case "run_command":
		cmd := extractCmdFromArgs(argsJSON)
		return s.checkTrivialCommand(cmd)
	default:
		return ""
	}
}

// recordWriteResult performs the post-execution write bookkeeping: a
// successful edit/write records the file as written (later reads become
// suspicious); a failed one clears any stale entry (#495).
func (s *toolOveruseState) recordWriteResult(toolName, argsJSON string, iter int, success bool) {
	switch toolName {
	case "write_file", "edit_file", "multi_edit_file", "multi_file_edit":
		s.recordWrite(extractPathFromArgs(toolName, argsJSON), iter, success)
	}
}

// extractPathFromArgs extracts the file/directory path from tool arguments.
func extractPathFromArgs(_ string, argsJSON string) string {
	data := []byte(argsJSON)
	// read_file, write_file, edit_file use "path" or "file_path"
	if p := extractJSONStringField(data, "file_path"); p != "" {
		return p
	}
	if p := extractJSONStringField(data, "path"); p != "" {
		return p
	}
	return ""
}

// extractCmdFromArgs extracts the command from run_command arguments.
func extractCmdFromArgs(argsJSON string) string {
	if c := extractJSONStringField([]byte(argsJSON), "command"); c != "" {
		return c
	}
	return ""
}

// overuseNormalizePath cleans up a path for comparison.
func overuseNormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Remove trailing slashes for consistency
	p = strings.TrimRight(p, "/")
	return p
}
