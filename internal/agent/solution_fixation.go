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

// fixationEntry tracks ONE tool call in the sliding window. filePaths holds
// every file the call targeted (multi_file_edit / multi_file_write touch all
// entries of files[], so a failed batch edit is attributed to each file it
// touched, #639); nil for non-edit calls, which still advance the window
// because the documented window unit is "12 tool calls", not "12 edits".
type fixationEntry struct {
	filePaths []string
	isError   bool
}

type solutionFixationState struct {
	recentCalls  []fixationEntry // sliding window of the last 12 tool calls (any kind)
	failedByFile map[string]int  // failed edit count per file in current window
	warningCount int
	firedFor     map[string]bool // files we already warned about this run
}

func newSolutionFixationState() *solutionFixationState {
	return &solutionFixationState{
		recentCalls:  make([]fixationEntry, 0, fixationWindow+1),
		failedByFile: make(map[string]int),
		firedFor:     make(map[string]bool),
	}
}

func (s *solutionFixationState) reset() {
	s.recentCalls = s.recentCalls[:0]
	s.failedByFile = make(map[string]int)
	s.warningCount = 0
	s.firedFor = make(map[string]bool)
}

// agentMutationEditTools is the single canonical set of file-mutating edit
// tools shared across the behavior detectors (#639). Before it existed,
// solution_fixation, error_rush, and momentum_loss each kept a private edit
// list and the three drifted apart: multi_file_write / batch_replace /
// lsp_rename were mutations for one detector and invisible to another, so
// the same failed edit fed the detectors inconsistently (and multi_file_edit
// was missing from error_rush's mutation list entirely).
//
// strategyFixationIsMutation (strategy_fixation.go) intentionally stays a
// separate predicate: it gates a different recording pipeline whose semantics
// are verified by its own tests.
var agentMutationEditTools = map[string]bool{
	"edit_file":        true,
	"write_file":       true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"notebook_edit":    true,
	"batch_replace":    true,
	"lsp_rename":       true,
}

// isAgentMutationEditTool reports whether the tool mutates files.
func isAgentMutationEditTool(name string) bool {
	return agentMutationEditTools[name]
}

// extractFilePathsFromEditArgs extracts ALL target file paths from edit
// tool arguments. A failed multi_file_edit / multi_file_write touches every
// entry of files[] — attributing only Files[0] meant three failed batches
// with reordered entries scattered one failure across three files and the
// per-file threshold was structurally unreachable (#639).
// Returns nil if no path can be extracted.
func extractFilePathsFromEditArgs(args string) []string {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}

	type editArgs struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
		Notebook string `json:"notebook_path"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}

	var paths []string
	add := func(p string) {
		if np := normalizePathFixation(p); np != "" {
			paths = append(paths, np)
		}
	}

	var parsed editArgs
	if err := json.Unmarshal([]byte(args), &parsed); err == nil {
		add(parsed.FilePath)
		add(parsed.Path)
		add(parsed.Notebook)
		for _, f := range parsed.Files {
			add(f.Path)
		}
		if len(paths) > 0 {
			return dedupePathsFixation(paths)
		}
	}

	// Fallback: lightweight extraction of "file_path":"..." or "path":"..."
	for _, field := range []string{"file_path", "path", "notebook_path"} {
		if v := extractJSONStringFieldFixation(args, field); v != "" {
			add(v)
		}
	}
	return dedupePathsFixation(paths)
}

// dedupePathsFixation removes duplicate paths while preserving order.
func dedupePathsFixation(paths []string) []string {
	if len(paths) <= 1 {
		return paths
	}
	seen := make(map[string]bool, len(paths))
	out := paths[:0]
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
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
	// Normalize separators (Windows paths too) and keep the FULL cleaned
	// path as the counting key. The old base-name reduction merged same-named
	// files across directories (cmd/a/main.go vs internal/b/main.go), so
	// failures against 4 DIFFERENT targets could stack into a false "fixated
	// on the same location" warning (#393).
	p = strings.ReplaceAll(p, "\\", "/")
	// Clean duplicate separators without pulling in path for one join.
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	// A path that normalizes to a bare separator carries no file identity.
	if p == "/" {
		return ""
	}
	return p
}

// recordToolCall records one tool call result. Called after EVERY tool
// execution: the window advances on all tool calls (the documented unit is
// "a sliding window of 12 tool calls"), while only mutation-edit calls with
// an extractable path feed the per-file failure counts.
//
// #639: the window previously advanced only on edit calls, so failures
// separated by dozens of healthy non-edit calls could still stack 3 failures
// on one file and fire a false anchoring warning on long runs.
func (s *solutionFixationState) recordToolCall(toolName, args string, isError bool) {
	var paths []string
	if isAgentMutationEditTool(toolName) {
		paths = extractFilePathsFromEditArgs(args)
	}

	entry := fixationEntry{filePaths: paths, isError: isError}
	s.recentCalls = append(s.recentCalls, entry)

	if isError {
		for _, p := range paths {
			s.failedByFile[p]++
		}
	}

	// Evict oldest entry if window exceeded
	if len(s.recentCalls) > fixationWindow {
		old := s.recentCalls[0]
		s.recentCalls = s.recentCalls[1:]
		if old.isError {
			for _, p := range old.filePaths {
				s.failedByFile[p]--
				if s.failedByFile[p] <= 0 {
					delete(s.failedByFile, p)
				}
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
