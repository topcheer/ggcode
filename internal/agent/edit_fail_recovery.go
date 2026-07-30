package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Consecutive edit failure recovery guidance.
//
// Research basis: Claude Code and Cursor both observe that LLM agents frequently
// enter "edit failure loops" — the agent tries edit_file, old_text doesn't match,
// it retries with slightly different text, fails again, and burns multiple
// iterations before re-reading the file. Aider reports this as a top-3 source of
// wasted tokens.
//
// The existing loop detector catches exact-duplicate tool calls, and the overseer
// detects general stuck patterns — but neither fires fast enough for the specific
// "edit fail → retry → fail again" chain on a single file. The overseer runs every
// 12 iterations with a spam threshold of 6; this guard fires after just 2
// consecutive failures, giving the agent immediate, targeted recovery guidance.
//
// Design:
//   - Tracks consecutive edit failures per file path (normalized).
//   - On the 2nd consecutive failure for the same file, injects guidance
//     directing the agent to re-read the file before retrying.
//   - Counter resets on: successful edit, file re-read, or a successful edit to
//     a different file (agent moved on).
//   - Guidance fires at most once per file per run (avoids nagging).
//   - No LLM cost — purely mechanical state tracking.

const editFailThreshold = 2

// editFailState tracks consecutive edit failures per file during a run.
type editFailState struct {
	// consecutiveFailures maps normalized file path to consecutive failure count.
	consecutiveFailures map[string]int

	// guidanceFired tracks files for which recovery guidance has been injected.
	guidanceFired map[string]bool
}

func newEditFailState() *editFailState {
	return &editFailState{
		consecutiveFailures: make(map[string]int),
		guidanceFired:       make(map[string]bool),
	}
}

func (s *editFailState) reset() {
	s.consecutiveFailures = make(map[string]int)
	s.guidanceFired = make(map[string]bool)
}

// recordEditSuccess resets the failure counter for the given path. A successful
// edit means the agent's understanding of the file content was correct.
func (s *editFailState) recordEditSuccess(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	delete(s.consecutiveFailures, n)
}

// recordRead resets the failure counter for the given path. When the agent
// re-reads a file, it gets fresh content — any prior failures are no longer
// relevant because the agent now has up-to-date content.
func (s *editFailState) recordRead(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	delete(s.consecutiveFailures, n)
}

// recordEditFailure increments the failure counter and returns recovery guidance
// if the threshold is met. Returns "" if below threshold or guidance already fired.
func (s *editFailState) recordEditFailure(path string) string {
	if path == "" {
		return ""
	}
	n := normalizePath(path)
	s.consecutiveFailures[n]++

	count := s.consecutiveFailures[n]
	if count < editFailThreshold {
		return ""
	}
	if s.guidanceFired[n] {
		return ""
	}
	s.guidanceFired[n] = true
	debug.Log("agent", "edit-fail-recovery: %d consecutive failures on %s, injecting guidance", count, n)
	return fmt.Sprintf(
		"This is your %dth consecutive failed edit to this file. "+
			"The file content likely differs from what you expect. "+
			"Before retrying: (1) re-read the file with read_file to see its current content, "+
			"(2) verify the text you are matching with old_text still exists, "+
			"(3) if the section was moved or renamed, use grep to locate it.",
		count,
	)
}

// extractFileForEdit returns the primary file path from an edit tool call.
// Used to attribute failures to specific files.
func extractFileForEdit(toolName string, args []byte) string {
	paths := extractEditFilePaths(toolName, args)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// summarizeEditFailures returns a brief summary of files with active failure
// counts, for debugging and reflection. Returns empty string if no failures.
func (s *editFailState) summarizeEditFailures() string {
	if len(s.consecutiveFailures) == 0 {
		return ""
	}
	var parts []string
	for path, count := range s.consecutiveFailures {
		if count > 0 {
			short := path
			if idx := strings.LastIndex(short, "/"); idx >= 0 {
				short = short[idx+1:]
			}
			parts = append(parts, fmt.Sprintf("%s(%d)", short, count))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
