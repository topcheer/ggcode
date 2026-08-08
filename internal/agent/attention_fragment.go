package agent

// attention_fragment.go -- Attention Fragmentation Detector
//
// Research basis:
//   - "Overloaded minds and machines: a cognitive load framework" (Springer
//     2026, s10462-026-11510-z): maps Cognitive Load Theory (CLT) to LLM
//     agents. Human working memory fragments under rapid attention-switching;
//     LLM "working memory" (context window) suffers the same degradation.
//   - "United Minds or Isolated Agents?" (arXiv:2506.06843): proposes CLT as
//     a principled design lens for LLM systems. The bottleneck is bounded
//     working memory, analogous to human cognitive load constraints.
//   - Cognitive Load Theory (Sweller 1988, refined through 2025): distinguishes
//     three load types - intrinsic (task complexity), extraneous (wasted
//     overhead from poor information organization), and germane (productive
//     schema-building). Rapid topic-switching creates EXTRANEOUS load.
//
// This detector identifies a specific anti-pattern: the agent makes consecutive
// tool calls that rapidly switch between unrelated directories/packages without
// dwelling on any single concern. Each switch forces the model to reload
// context about a different part of the codebase, creating extraneous cognitive
// load and reducing effective working memory for the actual task.
//
// The metric: "switch density" = fraction of consecutive tool-call pairs in a
// sliding window that target DIFFERENT directories. High switch density means
// the agent is thrashing between concerns rather than maintaining coherent focus.
//
// Example anti-pattern (switch density = 1.0, every call jumps to new dir):
//   read auth/login.go → grep db/query.go → edit api/handler.go →
//   read config/loader.go → test utils/helpers.go
//
// Healthy pattern (switch density low, dwells in one area):
//   read auth/login.go → read auth/session.go → edit auth/login.go →
//   test auth/login_test.go
//
// Distinct from existing detectors:
//   - target_scatter: measures BREADTH (N unique targets with no mutation)
//   - edit_oscillation: same-file back-and-forth edits
//   - futile_cycle: re-reading the SAME files repeatedly
//   - tool_diversity_gate: diversity of TOOL TYPES, not target paths
//   - THIS detector: measures SWITCH RATE between distinct directories
//     across consecutive calls - the cognitive cost of context-switching,
//     regardless of whether mutations occur.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// attentionFragmentState tracks directory-level context switches.
type attentionFragmentState struct {
	// recentDirs is a sliding window of directories from recent tool calls.
	recentDirs []string
	// switchCount counts how many consecutive pairs had different dirs.
	switchCount int
	// windowSize is the max number of calls to retain.
	windowSize int
	// warnCount caps warnings per run.
	warnCount int
	// firedAt tracks the call count when last warned, to space out re-warnings.
	firedAt int
	// totalCalls counts all recorded calls.
	totalCalls int
}

const (
	// afWindowSize is the sliding window length for switch-density calculation.
	afWindowSize = 10
	// afSwitchThreshold is the minimum switch density to trigger (70%).
	afSwitchThreshold = 0.70
	// afMinUniqueDirs is the minimum number of unique directories in the
	// window before the detector fires (avoids firing on 2-dir alternation).
	afMinUniqueDirs = 4
	// afMaxWarnings caps how many times this detector warns per run.
	afMaxWarnings = 2
	// afRefireGap requires this many new calls before re-warning.
	afRefireGap = 8
)

func newAttentionFragmentState() *attentionFragmentState {
	return &attentionFragmentState{
		windowSize: afWindowSize,
	}
}

// extractDir extracts the top-2 directory components from a file path,
// normalizing to forward slashes. Returns empty string if no path is found.
func extractAFDir(pathStr string) string {
	if pathStr == "" {
		return ""
	}
	// Normalize to forward slashes.
	normalized := strings.ReplaceAll(pathStr, "\\", "/")
	// Try to extract a path-like substring from the argument.
	// For tool calls, paths typically appear as quoted strings or bare values.
	// We look for the first segment that resembles a file path.
	dir := filepath.Dir(normalized)
	parts := strings.Split(dir, "/")
	// Take up to 2 meaningful components to group related files.
	var meaningful []string
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		meaningful = append(meaningful, p)
	}
	if len(meaningful) <= 2 {
		return strings.Join(meaningful, "/")
	}
	// Take last 2 components for deeper paths (e.g., internal/agent).
	return strings.Join(meaningful[len(meaningful)-2:], "/")
}

// extractPathFromToolCall tries to find a file path in common tool call arguments.
func extractPathFromToolCall(_ string, args map[string]interface{}) string {
	// Tools that carry a primary path argument.
	pathKeys := []string{"file_path", "path", "source", "directory", "glob"}
	for _, key := range pathKeys {
		if val, ok := args[key]; ok {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	// For tools with "files" array (e.g., multi_file_read, multi_file_edit).
	if files, ok := args["files"]; ok {
		switch v := files.(type) {
		case []interface{}:
			if len(v) > 0 {
				if item, ok := v[0].(map[string]interface{}); ok {
					if p, ok := item["path"].(string); ok {
						return p
					}
				}
			}
		case string:
			return v
		}
	}
	// For grep/search with "pattern" but sometimes path in "path" already covered.
	// For edit tools, "old_text" is content not path.
	return ""
}

// isAFRelevantTool returns true if the tool call targets a file/directory
// and thus contributes to attention context-switching.
func isAFRelevantTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "edit_file", "multi_edit_file",
		"write_file", "glob", "list_directory", "grep", "search_files",
		"code_search", "batch_replace",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols",
		"lsp_diagnostics", "lsp_implementation", "lsp_document_highlights",
		"lsp_rename", "lsp_code_actions",
		"file_ops", "git_add", "git_diff", "git_show", "git_blame",
		"test_impact", "code_health", "dep_graph":
		return true
	default:
		return false
	}
}

// recordToolCall records a tool call and its target directory.
func (s *attentionFragmentState) recordToolCall(name string, args map[string]interface{}) {
	if !isAFRelevantTool(name) {
		return
	}
	pathStr := extractPathFromToolCall(name, args)
	dir := extractAFDir(pathStr)
	if dir == "" {
		// Tool call has no extractable path; skip but don't reset.
		return
	}
	s.totalCalls++
	s.recentDirs = append(s.recentDirs, dir)
	if len(s.recentDirs) > s.windowSize {
		s.recentDirs = s.recentDirs[len(s.recentDirs)-s.windowSize:]
	}
}

// analyze computes switch density and fires guidance if fragmented.
func (s *attentionFragmentState) analyze() string {
	if s.warnCount >= afMaxWarnings {
		return ""
	}
	if len(s.recentDirs) < s.windowSize {
		return ""
	}
	// Space out re-warnings.
	if s.firedAt > 0 && s.totalCalls-s.firedAt < afRefireGap {
		return ""
	}

	// Count switches (consecutive pairs with different dirs).
	switches := 0
	uniqueDirs := map[string]bool{}
	for i, d := range s.recentDirs {
		uniqueDirs[d] = true
		if i > 0 && d != s.recentDirs[i-1] {
			switches++
		}
	}
	pairs := len(s.recentDirs) - 1
	if pairs == 0 {
		return ""
	}
	switchDensity := float64(switches) / float64(pairs)

	if switchDensity < afSwitchThreshold {
		return ""
	}
	if len(uniqueDirs) < afMinUniqueDirs {
		return ""
	}

	s.warnCount++
	s.firedAt = s.totalCalls

	debug.Log("agent", "attention fragmentation detected: density=%.2f, unique_dirs=%d", switchDensity, len(uniqueDirs))

	return fmt.Sprintf(
		"[attention-fragment] Switch density %.0f%% across %d unique directories in last %d calls - "+
			"rapid context-switching creates extraneous cognitive load (Cognitive Load Theory for LLM agents, "+
			"arXiv:2506.06843). Each jump forces context reload. "+
			"Consider: group related operations (read+edit+test in one area) before switching to the next concern.",
		switchDensity*100, len(uniqueDirs), len(s.recentDirs),
	)
}

// reset clears state for a new user turn.
func (s *attentionFragmentState) reset() {
	s.recentDirs = s.recentDirs[:0]
	s.switchCount = 0
	// Don't reset warnCount or firedAt - those persist across the run.
}
