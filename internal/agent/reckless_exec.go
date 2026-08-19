package agent

// reckless_exec.go -- Reckless Execution Detector
//
// Research basis:
//   - Agentic Metacognition (arXiv:2509.19783, 2025): agents that act
//     without understanding their environment "encounter unrecoverable
//     failures" -- they modify code they haven't examined, leading to
//     cascading errors and wasted effort.
//   - HyperAgents / DGM-H (Meta, 2026): emergent "persistent memory"
//     and "structured decision pipelines" demonstrate that information
//     gathering BEFORE action is a critical success factor.
//   - Anthropic "Effective Agents" (2025): emphasizes "understanding
//     before action" -- the agent should explore relevant context before
//     modifying files.
//
// Problem: AI coding agents sometimes start editing files within the
// first few iterations of a run WITHOUT having read or searched those
// specific files first. This "reckless execution" pattern leads to:
//
//  1. Edits based on assumed (hallucinated) content rather than actual
//     file state
//  2. Missing critical context (existing patterns, constraints, imports)
//  3. Breaking changes to code the agent hasn't examined
//  4. Wasted iterations fixing problems caused by insufficient context
//
// This is the DUAL of analysis_paralysis.go (too much exploration):
// it catches TOO LITTLE exploration before action. It is also distinct
// from bare_edit_streak.go (which tracks edit-edit streaks without
// verification) and mindless_action.go (which tracks reasoning text
// length per step) -- this detector specifically checks whether the
// agent EXPLORED a file before EDITING it.
//
// Design:
//   - Tracks which files the agent has READ (read_file, multi_file_read,
//     search_files, grep, lsp_*, etc.) before the first EDIT to that
//     same file.
//   - If 2+ files are edited WITHOUT prior exploration in the first
//     `recklessGraceIter` iterations, inject guidance to slow down and
//     explore first.
//   - Non-blocking advisory, max 2 warnings per run.
//   - Zero LLM cost -- pure set membership tracking.

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// recklessGraceIter: only check within this many iterations from run start.
	recklessGraceIter = 8

	// recklessUnexploredThreshold: number of unexplored edits to trigger warning.
	recklessUnexploredThreshold = 2

	// recklessMaxWarnings: max warnings per run.
	recklessMaxWarnings = 2

	// recklessMaxTracked: max files to track (memory bound).
	recklessMaxTracked = 40
)

// recklessExecState tracks explored files vs edited files.
type recklessExecState struct {
	readFiles  map[string]bool // files explored via read/search tools
	editFiles  map[string]bool // files modified via edit/write tools
	unexplored int             // count of edits to unexplored files
	warnings   int             // warnings issued this run
	iteration  int             // current iteration counter
}

func newRecklessExecState() *recklessExecState {
	return &recklessExecState{
		readFiles: make(map[string]bool),
		editFiles: make(map[string]bool),
	}
}

func (s *recklessExecState) reset() {
	s.readFiles = make(map[string]bool)
	s.editFiles = make(map[string]bool)
	s.unexplored = 0
	s.warnings = 0
	s.iteration = 0
}

// recordReadTool marks files as explored when read/search tools are used.
func (s *recklessExecState) recordReadTool(toolName, args string) {
	if !recklessIsExplorationTool(toolName) {
		return
	}
	for _, path := range recklessExtractPaths(toolName, args) {
		if len(s.readFiles) < recklessMaxTracked {
			s.readFiles[path] = true
		}
	}
}

// recordEditTool checks if an edit targets an unexplored file.
// Returns true if a warning should fire.
func (s *recklessExecState) recordEditTool(toolName, args string) bool {
	if !recklessIsEditTool(toolName) {
		return false
	}

	for _, path := range recklessExtractPaths(toolName, args) {
		if s.editFiles[path] {
			continue // already tracked
		}
		s.editFiles[path] = true

		// Check if this file was previously explored
		if !s.readFiles[path] {
			s.unexplored++
		}
	}

	// Only check within grace iterations
	if s.iteration > recklessGraceIter {
		return false
	}

	if s.unexplored >= recklessUnexploredThreshold && s.warnings < recklessMaxWarnings {
		s.warnings++
		return true
	}

	return false
}

// maybeWarn returns guidance text if the detector fires.
func recklessWarning(unexplored int) string {
	return fmt.Sprintf("%s",
		"[reckless-exec] You have edited "+strconv.Itoa(unexplored)+
			" file(s) without reading or searching them first. "+
			"Editing files you haven't examined leads to incorrect "+
			"assumptions about content, missing imports, and breaking "+
			"changes. PAUSE and read the target files (read_file / "+
			"search_files) before making further edits.",
	)
}

// recklessIsExplorationTool returns true for tools that gather information.
func recklessIsExplorationTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"lsp_symbols", "lsp_hover", "lsp_workspace_symbols", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_prepare_call_hierarchy",
		"git_show", "git_diff", "git_blame", "git_log":
		return true
	}
	return false
}

// recklessIsEditTool returns true for tools that modify files.
// Derived from the canonical sourceMutatingTools superset (#738).
func recklessIsEditTool(name string) bool {
	return sourceMutatingTools[name]
}

// recklessExtractPaths extracts file paths from tool arguments.
// Uses simple string scanning to avoid JSON parsing overhead.
func recklessExtractPaths(toolName, args string) []string {
	paths := recklessExtractFieldValues(args, []string{
		"file_path", "path", "notebook_path",
	})

	// multi_edit_file / multi_file_edit have nested paths
	if toolName == "multi_edit_file" || toolName == "multi_file_write" ||
		toolName == "multi_file_read" || toolName == "batch_replace" {
		// Also look for paths in nested "files" array
		paths = append(paths, recklessExtractFieldValues(args, []string{"path"})...)
	}

	// Deduplicate
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		result = append(result, p)
	}
	return result
}

// recklessExtractFieldValues extracts string values for the given field names
// from a JSON-like argument string. Handles both "field": "value" and
// "field": <array> patterns.
func recklessExtractFieldValues(args string, fields []string) []string {
	var result []string
	for _, field := range fields {
		// Search for "field": "value" pattern
		search := "\"" + field + "\":"
		idx := 0
		for {
			pos := strings.Index(args[idx:], search)
			if pos < 0 {
				break
			}
			pos += idx
			// Move past the field name and colon
			valStart := pos + len(search)
			// Skip whitespace
			for valStart < len(args) && (args[valStart] == ' ' || args[valStart] == '\t') {
				valStart++
			}
			if valStart >= len(args) {
				break
			}
			if args[valStart] == '"' {
				// String value
				valStart++
				end := strings.Index(args[valStart:], "\"")
				if end < 0 {
					break
				}
				result = append(result, args[valStart:valStart+end])
				idx = valStart + end + 1
			} else {
				// Non-string value, skip
				idx = valStart + 1
			}
		}
	}
	return result
}
