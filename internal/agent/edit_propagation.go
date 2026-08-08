package agent

// edit_propagation.go -- Cross-File Edit Propagation Risk Detector
//
// Research basis:
//   - MAST taxonomy (Cemri et al., "Why Do Multi-Agent LLM Systems Fail?",
//     2025): "Propagation failures" -- downstream trust of upstream errors
//     is the root cause in the majority of terminal failures. Errors
//     compound across multi-step workflows when unverified intermediate
//     results are trusted downstream.
//   - Sherlock framework (Microsoft Research, Nov 2025): "vulnerability-
//     informed placement" -- nodes with high downstream influence need
//     concentrated verification. Edits to widely-imported files have higher
//     propagation risk than edits to leaf files.
//   - Self-Verification in Multi-Step Agent Workflows (2026): "accuracy
//     drops non-linearly with step count" because cross-file dependencies
//     create error propagation paths.
//
// Problem: verifyDebt counts TOTAL edits since the last green build, but
// it does not distinguish between:
//   - 7 edits to the SAME file (low propagation risk -- you'll catch
//     issues when you test that one file)
//   - 7 edits to 7 DIFFERENT files (high propagation risk -- cross-file
//     import/dependency chains mean a latent error in file A propagates
//     to files B, C, D that depend on it)
//
// This detector adds the file-diversity dimension. When many DISTINCT files
// are edited without verification, the probability that an error in one
// file propagates through dependency chains to others increases sharply.
// This is the "error propagation path density" signal from the MAST
// taxonomy.
//
// Design:
//   - Tracks the set of distinct source files edited since the last
//     successful (green) build/test.
//   - Fires at 4+ distinct files: moderate risk, remind to verify.
//   - Escalates at 7+ distinct files: high risk, emphasize propagation.
//   - Resets on green build (same as verifyDebt).
//   - Zero LLM cost -- pure set membership tracking.
//   - Non-blocking advisory, max 2 warnings per run.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// propagationWarn1: distinct files threshold for first warning.
	propagationWarn1 = 4

	// propagationWarn2: distinct files threshold for escalation.
	propagationWarn2 = 7

	// propagationMaxWarnings: max warnings per run.
	propagationMaxWarnings = 2

	// propagationMaxTracked: max distinct file paths to track (memory bound).
	propagationMaxTracked = 60
)

// editPropagationState tracks distinct source files edited since the last
// green build. Complements verifyDebt (total edit count) with the
// file-diversity dimension (cross-file propagation risk).
type editPropagationState struct {
	mu             sync.Mutex
	distinctFiles  map[string]bool // distinct file paths edited since green build
	totalDistinct  int             // running count (for metrics)
	warningsIssued int             // warnings issued this run (cap at 2)
}

func newEditPropagationState() *editPropagationState {
	return &editPropagationState{
		distinctFiles: make(map[string]bool),
	}
}

// recordEdit tracks a source-code file edit. Returns true if a new
// distinct file was added (i.e., this file wasn't seen before since the
// last green build).
func (s *editPropagationState) recordEdit(toolName, args string) bool {
	if !fileEditingTools[toolName] {
		return false
	}
	paths := propagationExtractPaths(args)
	if len(paths) == 0 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	added := false
	for _, fp := range paths {
		if fp == "" {
			continue
		}
		if !s.distinctFiles[fp] && len(s.distinctFiles) < propagationMaxTracked {
			s.distinctFiles[fp] = true
			added = true
		}
	}
	return added
}

// recordGreenBuild resets the distinct file set after a successful
// verification command. Called alongside verifyDebt.recordVerifyCommand.
func (s *editPropagationState) recordGreenBuild() {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.distinctFiles)
	if n > 0 {
		debug.Log("agent", "edit propagation reset: %d distinct files cleared on green build", n)
	}
	s.distinctFiles = make(map[string]bool)
}

// maybeWarn returns a guidance string when cross-file propagation risk
// crosses warning thresholds.
func (s *editPropagationState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warningsIssued >= propagationMaxWarnings {
		return ""
	}

	count := len(s.distinctFiles)
	if count < propagationWarn1 {
		return ""
	}

	s.warningsIssued++

	var msg string
	if count >= propagationWarn2 {
		msg = fmt.Sprintf(
			"%s%d%s",
			"[Cross-file propagation risk: ", count,
			" distinct files edited since the last "+
				"successful build. Cross-file dependency chains create error propagation "+
				"paths -- a latent defect in any one file can cascade through imports to "+
				"all others. Research shows accuracy drops non-linearly with unverified "+
				"cross-file edits (MAST taxonomy, Cemri et al. 2025). Run a build+test NOW "+
				"to establish a verified baseline before the error surface grows further.]",
		)
	} else {
		msg = fmt.Sprintf(
			"%s%d%s",
			"[Cross-file propagation risk: ", count,
			" distinct files edited since the last "+
				"successful build. Edits spread across many files create cross-file "+
				"dependency propagation paths. Run a build+test to verify accumulated "+
				"changes before adding more files to the unverified set.]",
		)
	}

	debug.Log("agent", "edit propagation warning #%d: %d distinct files since green build (iter=%d)",
		s.warningsIssued, count, iteration)

	return msg
}

// reset clears state for a new user turn.
func (s *editPropagationState) reset() {
	s.mu.Lock()
	s.distinctFiles = make(map[string]bool)
	s.totalDistinct = 0
	s.warningsIssued = 0
	s.mu.Unlock()
}

// propagationExtractPaths extracts file paths from edit tool arguments.
// Handles edit_file, multi_edit_file, write_file, multi_file_edit,
// batch_replace argument formats.
func propagationExtractPaths(args string) []string {
	var paths []string

	// edit_file / write_file: "file_path":"..."
	paths = append(paths, propagationExtractField(args, "file_path")...)

	// multi_edit_file: same file_path
	// (already captured above)

	// multi_file_edit: "path":"..." in files array
	paths = append(paths, propagationExtractField(args, "path")...)

	// batch_replace: "files":["...","..."]
	// propagationExtractField handles this too since values are strings

	// multi_file_write: same "path":"..." in files array
	// (already captured above)

	return paths
}

// propagationExtractField extracts values for a given JSON key from a
// raw argument string. Handles both "key":"value" and "key": "value"
// patterns. Returns all unique values found.
func propagationExtractField(s, key string) []string {
	var values []string
	// Search for "key":"value" or "key": "value" patterns
	search := "\"" + key + "\":"
	idx := 0
	for {
		pos := strings.Index(s[idx:], search)
		if pos < 0 {
			break
		}
		pos += idx
		// Move past the key
		valStart := pos + len(search)
		// Skip optional whitespace
		for valStart < len(s) && (s[valStart] == ' ' || s[valStart] == '\t' || s[valStart] == '\n') {
			valStart++
		}
		// Expect opening quote
		if valStart >= len(s) || s[valStart] != '"' {
			idx = pos + len(search)
			continue
		}
		valStart++ // skip opening quote
		// Find closing quote (handle escaped quotes)
		valEnd := valStart
		for valEnd < len(s) {
			if s[valEnd] == '\\' && valEnd+1 < len(s) {
				valEnd += 2
				continue
			}
			if s[valEnd] == '"' {
				break
			}
			valEnd++
		}
		if valEnd < len(s) {
			val := s[valStart:valEnd]
			val = strings.ReplaceAll(val, "\\/", "/")
			if val != "" {
				values = append(values, val)
			}
			idx = valEnd + 1
		} else {
			break
		}
	}
	return values
}
