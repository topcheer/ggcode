package agent

// target_scatter.go -- Target Scatter Detector (World Model Calibration Gap)
//
// Research basis:
//   - Qwen-AgentWorld (2026): world models for agents should predict action
//     consequences. A well-calibrated agent knows exactly where to look and
//     converges quickly. A poorly calibrated agent scatters across many
//     unrelated targets because each result surprises it.
//   - World2VLM (arXiv:2604.26934): distilling world model imagination for
//     action consequence prediction. Agents that cannot predict outcomes
//     engage in unfocused exploration.
//   - SICA (arXiv:2504.15228, NeurIPS 2025): trajectory waste from unfocused
//     exploration is a primary bottleneck in agent efficiency.
//   - Agent-R self-training: agents that learn to converge faster achieve
//     significantly higher task success rates.
//
// This detector identifies a specific anti-pattern: the agent makes many
// diagnostic tool calls (read, grep, search, lsp) targeting MANY DIFFERENT
// files/directories without converging or producing any mutation. This
// "scatter pattern" indicates the agent's world model is poorly calibrated -
// it doesn't know where things are, so it looks everywhere.
//
// The pattern: read A.go -> grep B.go -> search C.go -> read D.go -> lsp E.go
// (5+ unique targets, no edit/write, no convergence).
//
// The fix: pause broad scanning. Use a focused search (code_search with a
// semantic query, or lsp_workspace_symbols) to identify the right target
// before reading individual files.
//
// Distinct from existing detectors:
//   - futile_cycle: re-reading the SAME files repeatedly
//   - circular_reasoning: using the SAME arguments repeatedly
//   - wasted_explore: any exploration without mutation (broader)
//   - empty_search_tracker: empty search results specifically
//   - tool_diversity_gate: diversity of TOOL TYPES, not target paths
//   - analysis_paralysis: extensive analysis broadly (no target-scatter metric)
//   - THIS detector: measures the SPREAD of investigation targets - how many
//     unique files/dirs the agent touches in diagnostic calls without
//     converging. High spread + no mutation = world model scatter.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// targetScatterState tracks diagnostic tool call targets to detect scatter.
type targetScatterState struct {
	// recentTargets is a sliding window of recent diagnostic call targets.
	recentTargets []string
	// uniqueTargets tracks the set of unique paths in the current window.
	uniqueTargets map[string]bool
	// totalCalls counts all diagnostic calls since last reset.
	totalCalls int
	// warnCount caps warnings per run.
	warnCount int
	// scatterDetectedAt tracks totalCalls value when last warned, to space out re-warnings.
	scatterDetectedAt int
}

const (
	scatterWindowSize      = 8  // sliding window of recent diagnostic calls
	scatterUniqueThreshold = 5  // 5+ unique targets in window = scatter
	scatterMinTotalCalls   = 6  // need at least 6 total diagnostic calls before checking
	scatterMaxWarns        = 2  // cap total warnings per run
	scatterPathNormalizer  = 4  // how many path components to keep for uniqueness
	scatterReFireGap       = 3  // min new diagnostic calls between warnings
	scatterSampleCount     = 4  // max sample targets to show in warning
	scatterPseudoLen       = 40 // max chars of args for pseudo-target
)

func newTargetScatterState() *targetScatterState {
	return &targetScatterState{
		uniqueTargets: make(map[string]bool),
	}
}

func (s *targetScatterState) reset() {
	s.recentTargets = nil
	s.uniqueTargets = make(map[string]bool)
	s.totalCalls = 0
	s.warnCount = 0
	s.scatterDetectedAt = 0
}

// scatterIsDiagnostic returns true for tools that read/analyze (not mutate).
func scatterIsDiagnostic(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "grep", "search_files", "glob",
		"code_search", "list_directory",
		"lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_diagnostics", "lsp_document_highlights", "lsp_workspace_symbols",
		"lsp_implementation", "lsp_code_actions":
		return true
	default:
		return false
	}
}

// scatterIsMutation returns true for tools that modify files or state.
// scatterIsVerification was removed (#488): tool-NAME-level verification
// classification was the exact #350 bug family — run_command carries
// observational (ls/cat) and verify (go test) commands alike, so the
// distinction is now made on command CONTENT in recordToolCall via
// psIsVerifyCommand.
func scatterIsMutation(toolName string) bool {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "multi_file_write",
		"notebook_edit", "batch_replace", "lsp_rename",
		// #488: these mutations were unclassified, so a REAL mutation left
		// the scatter window alive and the detector later fired "without
		// converging or taking action" — contradicting its own contract.
		"file_ops", "undo_edit", "write_command_input", "enter_worktree",
		"git_add", "git_commit", "git_revert", "git_reset", "git_checkout",
		"git_stash":
		return true
	default:
		return false
	}
}

// We keep the last N components to group related files.
func scatterNormalizePath(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'")
	if raw == "" {
		return ""
	}
	// Extract path-like substring (for JSON args, find "path":"..." or bare path)
	for _, key := range []string{`"path":"`, `"file":"`, `"directory":"`, `"filePath":"`} {
		if idx := strings.Index(raw, key); idx >= 0 {
			start := idx + len(key)
			if end := strings.Index(raw[start:], "\""); end >= 0 {
				raw = raw[start : start+end]
				break
			}
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "/")
	var keep []string
	for i := len(parts) - 1; i >= 0 && len(keep) < scatterPathNormalizer; i-- {
		if parts[i] != "" {
			keep = append([]string{parts[i]}, keep...)
		}
	}
	return strings.Join(keep, "/")
}

// extractTargets pulls target path(s) from tool call arguments.
func extractTargets(args string) []string {
	if args == "" {
		return nil
	}
	path := scatterNormalizePath(args)
	if path != "" {
		return []string{path}
	}
	return nil
}

// recordToolCall processes each tool execution result.
func (s *targetScatterState) recordToolCall(toolName, args string) {
	s.totalCalls++

	if scatterIsMutation(toolName) {
		// A real mutation is a convergence/action signal: reset the window.
		s.recentTargets = nil
		s.uniqueTargets = make(map[string]bool)
		return
	}

	if toolName == "run_command" || toolName == "start_command" {
		// Command-content classification (#488, same lesson as #350/#483):
		// only a genuine build/test/verify command is a convergence signal
		// that clears the window. Pure observational commands (ls / cat /
		// pwd / git log / git status) interleave with diagnostics in the most
		// common investigation flow — clearing on them made the detector
		// systematically blind (unique targets never reached the threshold).
		if psIsVerifyCommand(extractCommandFromArgs(json.RawMessage(args))) {
			s.recentTargets = nil
			s.uniqueTargets = make(map[string]bool)
		}
		return
	}

	if !scatterIsDiagnostic(toolName) {
		return
	}

	targets := extractTargets(args)
	if len(targets) == 0 {
		// No path extractable; use tool name + first 40 chars as pseudo-target
		pseudo := toolName + ":" + scatterTruncate(args, scatterPseudoLen)
		targets = []string{pseudo}
	}

	for _, t := range targets {
		s.recentTargets = append(s.recentTargets, t)
		s.uniqueTargets[t] = true
	}

	// Trim window
	if len(s.recentTargets) > scatterWindowSize {
		s.recentTargets = s.recentTargets[len(s.recentTargets)-scatterWindowSize:]
		s.uniqueTargets = make(map[string]bool)
		for _, t := range s.recentTargets {
			s.uniqueTargets[t] = true
		}
	}
}

func scatterTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// check returns guidance if the scatter pattern is detected.
func (s *targetScatterState) check() string {
	if s.warnCount >= scatterMaxWarns {
		return ""
	}
	if s.totalCalls < scatterMinTotalCalls {
		return ""
	}

	// Require at least 3 more diagnostic calls since last warning before re-firing
	if s.scatterDetectedAt > 0 && s.totalCalls-s.scatterDetectedAt < scatterReFireGap {
		return ""
	}

	uniqueCount := len(s.uniqueTargets)
	if uniqueCount < scatterUniqueThreshold {
		return ""
	}

	s.warnCount++
	s.scatterDetectedAt = s.totalCalls

	var sampleTargets []string
	for t := range s.uniqueTargets {
		sampleTargets = append(sampleTargets, t)
		if len(sampleTargets) >= scatterSampleCount {
			break
		}
	}

	guidance := fmt.Sprintf(
		"[Target Scatter / World Model Miscalibration] You have examined %d unique files/targets "+
			"across %d+ diagnostic calls without converging or taking action. "+
			"Research (Qwen-AgentWorld 2026, SICA NeurIPS 2025) shows this scatter pattern - "+
			"bouncing between unrelated files - indicates your mental model of the codebase "+
			"is poorly calibrated: each result is surprising you into a new direction.\n\n"+
			"STOP scanning individual files. Instead:\n"+
			"  1. Use code_search with a semantic query to find the RIGHT file in one shot\n"+
			"  2. Or use lsp_workspace_symbols to locate the exact symbol\n"+
			"  3. Then read ONLY the most relevant file and act\n\n"+
			"Targets examined so far: %s",
		uniqueCount, s.totalCalls, strings.Join(sampleTargets, ", "),
	)

	return guidance
}
