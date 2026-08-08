package agent

// Solution Fixation Detector - Diagnosis Anchoring in Failed Edit Clusters
//
// Research basis:
//   - Anchoring Effect in LLMs (arXiv:2505.15392, IEEE 2026): LLMs anchor
//     heavily on their first hypothesis/diagnosis and are slow to abandon it
//     even in the face of contradicting evidence.
//   - AgentDebug / "Where LLM Agents Fail" (arXiv:2509.25370): agents that
//     don't learn from failures keep repeating variations of the same
//     approach, achieving up to 26% improvement when given principled
//     debugging feedback that forces re-diagnosis.
//   - ACONIC (IBM Research 2026): heuristic decomposition without formal
//     constraint analysis causes agents to fixate on one sub-problem.
//
// The core insight: when an LLM coding agent encounters a failure, it forms
// a root-cause hypothesis ("the bug is in auth.go's token validation"). It
// then tries fix after fix in that same location. Each failed fix is evidence
// against the hypothesis - but the anchoring effect means the agent keeps
// trying variations instead of stepping back and reconsidering whether the
// diagnosis itself is wrong.
//
// Distinction from existing detectors:
//   - error_strategy_loop.go: same ERROR CATEGORY recurring (the error text
//     pattern repeats). This detector tracks the FIX LOCATION, not the error.
//   - fix_cascade.go: edit->verify->fail->edit cycle (structural cycle across
//     different errors). This detector specifically targets SAME-FILE clusters.
//   - edit_fail_recovery.go: single edit failure -> recovery hint. This detector
//     fires only after MULTIPLE failures on the same target.
//   - edit_oscillation.go: same-file semantic content reversal (undo/redo).
//     This detector fires on FAILED edits, not content reversals.
//   - premature_commitment.go: behavioral commitment pattern. This detector
//     is about diagnosis-level fixation in debugging.
//
// What this detects: 3+ FAILED edit attempts targeting the same file (or
// same pair of files) within a sliding window of 12 tool calls. This
// pattern means the agent is anchored on "the problem is HERE" and keeps
// hammering the same location without reconsidering its root-cause analysis.
//
// Design:
//   - Zero LLM cost - pure file-path tracking + failure counting
//   - Fires at most 2 times per run
//   - Non-blocking: guidance injected as user message, agent continues
//   - Threshold: 3+ failed edits to the same file within window of 12

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	fixationThreshold   = 3 // failed edits on same file to trigger
	fixationWindow      = 12
	maxFixationWarnings = 2
)

// fixationEntry tracks one edit attempt.
type fixationEntry struct {
	filePath string
	isError  bool
}

type solutionFixationState struct {
	recentEdits  []fixationEntry // sliding window of edit attempts
	failedByFile map[string]int  // failed edit count per file in current window
	warningCount int
	firedFor     map[string]bool // files we already warned about this run
}

func newSolutionFixationState() *solutionFixationState {
	return &solutionFixationState{
		recentEdits:  make([]fixationEntry, 0, fixationWindow+1),
		failedByFile: make(map[string]int),
		firedFor:     make(map[string]bool),
	}
}

func (s *solutionFixationState) reset() {
	s.recentEdits = s.recentEdits[:0]
	s.failedByFile = make(map[string]int)
	s.warningCount = 0
	s.firedFor = make(map[string]bool)
}

// editToolsFixation maps tool names that perform file edits.
var editToolsFixation = map[string]bool{
	"edit_file":       true,
	"write_file":      true,
	"multi_edit_file": true,
	"notebook_edit":   true,
}

// extractFilePathFromEditArgs extracts the target file path from edit tool arguments.
// Returns empty string if path cannot be extracted.
func extractFilePathFromEditArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}

	type editArgs struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Notebook string `json:"notebook_path"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}

	var parsed editArgs
	if err := json.Unmarshal([]byte(args), &parsed); err == nil {
		if parsed.FilePath != "" {
			return normalizePathFixation(parsed.FilePath)
		}
		if parsed.Path != "" {
			return normalizePathFixation(parsed.Path)
		}
		if parsed.Notebook != "" {
			return normalizePathFixation(parsed.Notebook)
		}
		if len(parsed.Files) > 0 && parsed.Files[0].Path != "" {
			return normalizePathFixation(parsed.Files[0].Path)
		}
	}

	// Fallback: lightweight extraction of "file_path":"..." or "path":"..."
	path := extractJSONStringFieldFixation(args, "file_path")
	if path == "" {
		path = extractJSONStringFieldFixation(args, "path")
	}
	if path == "" {
		path = extractJSONStringFieldFixation(args, "notebook_path")
	}
	return normalizePathFixation(path)
}

// extractJSONStringFieldFixation does a lightweight scan for "field":"value" pattern.
func extractJSONStringFieldFixation(jsonStr, field string) string {
	needle := `"` + field + `"`
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {
		return ""
	}
	rest := jsonStr[idx+len(needle):]
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == ':' || rest[0] == '\t' || rest[0] == '\n') {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func normalizePathFixation(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Use base name to group related paths and reduce noise
	if idx := strings.LastIndexByte(p, '/'); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// recordEdit records an edit tool call result. Called after each tool execution.
func (s *solutionFixationState) recordEdit(toolName, args string, isError bool) {
	if !editToolsFixation[toolName] {
		return
	}

	filePath := extractFilePathFromEditArgs(args)
	if filePath == "" {
		return
	}

	entry := fixationEntry{filePath: filePath, isError: isError}
	s.recentEdits = append(s.recentEdits, entry)

	if isError {
		s.failedByFile[filePath]++
	}

	// Evict oldest entry if window exceeded
	if len(s.recentEdits) > fixationWindow {
		old := s.recentEdits[0]
		s.recentEdits = s.recentEdits[1:]
		if old.isError {
			s.failedByFile[old.filePath]--
			if s.failedByFile[old.filePath] <= 0 {
				delete(s.failedByFile, old.filePath)
			}
		}
	}
}

// checkAndWarn returns a guidance message if solution fixation is detected.
func (s *solutionFixationState) checkAndWarn() string {
	if s.warningCount >= maxFixationWarnings {
		return ""
	}

	var worstFile string
	worstCount := 0
	for f, c := range s.failedByFile {
		if s.firedFor[f] {
			continue // already warned about this file
		}
		if c > worstCount {
			worstCount = c
			worstFile = f
		}
	}

	if worstCount < fixationThreshold {
		return ""
	}

	s.firedFor[worstFile] = true
	s.warningCount++

	msg := "[Solution Fixation Alert] %d failed edit attempts have targeted %q. " +
		"You appear anchored on the hypothesis that the root cause is in this file, " +
		"but repeated failures strongly suggest the diagnosis is wrong. " +
		"STEP BACK: reconsider the root-cause analysis from scratch. " +
		"Ask: (1) Is the bug actually triggered from a different caller or upstream module? " +
		"(2) Could the error be environmental (config, dependency, env var) rather than code? " +
		"(3) Should you add diagnostic logging or print the actual error to verify your assumption? " +
		"Do not make another edit to %s until you have gathered new evidence."

	return fmt.Sprintf(msg, worstCount, worstFile, worstFile)
}
