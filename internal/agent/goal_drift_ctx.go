package agent

// Context-Length Goal Drift Detector
//
// Research basis:
//   - "Evaluating Goal Drift in Language Model Agents" (arXiv:2505.02709,
//     May 2025, Arike et al.): demonstrates that all evaluated LM agents
//     exhibit goal drift when operating autonomously for extended periods.
//     Critically, goal drift CORRELATES WITH CONTEXT LENGTH -- as the context
//     grows, agents become susceptible to pattern-matching behaviors and
//     deviate from the original objective. Goals shift gradually with only
//     subtle behavioral changes, making detection difficult.
//
// Problem: In long coding agent sessions (20+ iterations), the agent's
// recent tool calls increasingly target files, symbols, or concerns that
// have NO connection to the original user request. This happens because:
//   1. As context grows, earlier instructions lose salience
//   2. The agent picks up momentum from intermediate discoveries
//   3. Tangential exploration shifts focus away from the core ask
//   4. Error recovery can lead the agent into unrelated fix cascades
//
// This differs from existing detectors:
//   - criteria_drift_detect.go: detects narrowing/relaxing success criteria
//     in agent TEXT. This detects drift in agent ACTIONS (tool targets).
//   - scope_creep_detect.go: detects EXPANSION beyond request. This detects
//     DRIFT (actions unrelated to original goal, not necessarily expansion).
//   - plan_drift_detection.go: detects deviation from a stated PLAN. This
//     detects deviation from the original user REQUEST (no plan needed).
//   - tunnel_vision.go: detects fixation on one approach. This detects the
//     opposite: attention scattering away from the core goal.
//
// Detection approach:
//   1. Extract content keywords from the first user message (file paths,
//      function names, error messages, distinctive terms)
//   2. For each tool call, extract its target (file path, search query,
//      command, symbol name)
//   3. Track a sliding window of recent tool call targets
//   4. When context is large (high iteration count) AND the recent window
//      has low keyword overlap with the original request, emit a guidance
//      message to re-ground the agent in the user's original goal
// 5. Deterministic, zero LLM cost

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// goalDriftCtxState tracks context-length-induced goal drift by comparing
// recent tool call targets against keywords extracted from the original
// user request.
type goalDriftCtxState struct {
	mu sync.Mutex

	// Keywords extracted from the first user message
	originKeywords map[string]bool
	initialized    bool

	// Sliding window of recent tool call targets (normalized)
	recentTargets []string
	windowSize    int

	// Iteration count when last checked
	lastCheckIter int

	// Whether we've already warned this run
	warned bool
}

const (
	goalDriftMinIter        = 12   // Don't check before 12 iterations (let early exploration happen)
	goalDriftWindowSize     = 8    // Check last 8 tool calls for keyword overlap
	goalDriftMinKeywords    = 3    // Need at least 3 keywords from original request
	goalDriftMaxOverlapWarn = 1    // Warn if <=1 of recent targets match keywords
	goalDriftCheckInterval  = 4    // Check every 4 iterations after minimum
	goalDriftMaxWarnsPerRun = 1    // At most 1 warning per run
	goalDriftMinTargetLen   = 4    // Ignore very short targets
	goalDriftKeywordMinLen  = 4    // Keywords must be at least 4 chars
	goalDriftStopWords      = true // Filter common English stop words
)

// reset clears state for a new user turn (issue #28).
// Without this, keywords from turn 1 persist and cause false positives
// when the user starts a new task in a multi-turn session.
func (s *goalDriftCtxState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = false
	s.originKeywords = make(map[string]bool)
	s.recentTargets = nil
	s.warned = false
	s.lastCheckIter = 0
}

func newGoalDriftCtxState() *goalDriftCtxState {
	return &goalDriftCtxState{
		originKeywords: make(map[string]bool),
		windowSize:     goalDriftWindowSize,
	}
}

// initFromUserMessage extracts keywords from the first user message.
// Keywords are distinctive terms: file paths, function names, identifiers,
// error fragments, and other content-bearing words.
func (s *goalDriftCtxState) initFromUserMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized || strings.TrimSpace(msg) == "" {
		return
	}
	s.initialized = true

	// Split into tokens and filter
	words := strings.Fields(msg)
	for _, word := range words {
		word = cleanToken(word)
		if len(word) < goalDriftKeywordMinLen {
			continue
		}
		if isStopWord(word) || goalDriftIsStopWord(word) {
			continue
		}
		word = strings.ToLower(word)
		s.originKeywords[word] = true
	}

	// Also extract path-like tokens (e.g., "auth/login.go" -> "auth", "login")
	for _, pathField := range strings.Fields(msg) {
		pathField = strings.Trim(pathField, "\"'`,;()[]{}")
		if strings.Contains(pathField, "/") || strings.Contains(pathField, ".") {
			parts := strings.FieldsFunc(pathField, func(r rune) bool {
				return r == '/' || r == '.' || r == '_' || r == '-'
			})
			for _, part := range parts {
				part = strings.ToLower(part)
				if len(part) >= goalDriftKeywordMinLen && !goalDriftIsStopWord(part) {
					s.originKeywords[part] = true
				}
			}
		}
	}

	debug.Log("agent", "goal drift ctx: extracted %d keywords from user message", len(s.originKeywords))
}

// recordToolCall tracks a tool call target for drift analysis.
// target is extracted from the tool's arguments (file path, search query, etc.)
func (s *goalDriftCtxState) recordToolCall(toolName, rawArgs string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	target := extractTarget(toolName, []byte(rawArgs))
	if len(target) < goalDriftMinTargetLen {
		return
	}

	// Add to sliding window
	s.recentTargets = append(s.recentTargets, strings.ToLower(target))
	if len(s.recentTargets) > s.windowSize {
		s.recentTargets = s.recentTargets[len(s.recentTargets)-s.windowSize:]
	}
}

// checkDrift analyzes the recent tool call window against original keywords.
// Returns a non-empty guidance string if drift is detected.
func (s *goalDriftCtxState) checkDrift(iter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Don't check too early -- let exploration happen
	if iter < goalDriftMinIter {
		return ""
	}

	// Check at intervals to avoid spamming
	if iter-s.lastCheckIter < goalDriftCheckInterval && s.lastCheckIter > 0 {
		return ""
	}
	s.lastCheckIter = iter

	// Already warned this run
	if s.warned {
		return ""
	}

	// Need enough keywords to meaningfully compare
	if len(s.originKeywords) < goalDriftMinKeywords {
		return ""
	}

	// Need enough recent targets
	if len(s.recentTargets) < goalDriftWindowSize/2 {
		return ""
	}

	// Count how many recent targets contain at least one origin keyword
	matches := 0
	for _, recentTarget := range s.recentTargets {
		for kw := range s.originKeywords {
			if strings.Contains(recentTarget, kw) {
				matches++
				break
			}
		}
	}

	// If most recent targets don't match the original request keywords,
	// the agent may be drifting due to context-length goal drift.
	if matches <= goalDriftMaxOverlapWarn {
		s.warned = true
		debug.Log("agent", "goal drift ctx: drift detected at iter %d (%d/%d recent targets match origin keywords)", iter, matches, len(s.recentTargets))
		return "[goal-drift] Recent tool calls target files unrelated to original request. Re-read original request and verify alignment."
	}

	return ""
}

// extractTarget extracts the meaningful target from a tool call's arguments.
// For file tools: the file path. For search tools: the search pattern.
// For command tools: the command itself. For other tools: the raw args.
func extractTarget(toolName string, rawArgs []byte) string {
	// For tools that take a path or query, extract the first string argument
	// from the JSON-like arguments.
	switch toolName {
	case "read_file", "edit_file", "write_file", "multi_edit_file",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols",
		"lsp_diagnostics", "lsp_rename", "lsp_implementation":
		return extractJSONStringField(rawArgs, "path") +
			extractJSONStringField(rawArgs, "file_path")
	case "grep", "search_files":
		return extractJSONStringField(rawArgs, "pattern") +
			extractJSONStringField(rawArgs, "query")
	case "glob":
		return extractJSONStringField(rawArgs, "pattern")
	case "run_command", "start_command":
		return extractJSONStringField(rawArgs, "command")
	case "code_search":
		return extractJSONStringField(rawArgs, "query")
	case "lsp_workspace_symbols":
		return extractJSONStringField(rawArgs, "query")
	default:
		return string(rawArgs)
	}
}

// cleanToken strips punctuation and normalizes a word
func cleanToken(s string) string {
	s = strings.TrimFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.' && r != '/'
	})
	return s
}

var goalDriftStopWordSet = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "have": true,
	"your": true, "you": true, "the": true, "for": true, "are": true,
	"was": true, "but": true, "not": true, "and": true, "can": true,
	"all": true, "will": true, "they": true, "them": true, "then": true,
	"than": true, "been": true, "were": true, "into": true, "over": true,
	"when": true, "what": true, "which": true, "their": true, "there": true,
	"about": true, "would": true, "could": true, "should": true, "these": true,
	"those": true, "need": true, "make": true, "more": true, "such": true,
	"only": true, "some": true, "very": true, "just": true, "also": true,
	"like": true, "want": true, "does": true, "done": true, "task": true,
	"work": true, "help": true, "using": true, "based": true, "please": true,
	"following": true, "before": true, "after": true,
}

func goalDriftIsStopWord(w string) bool {
	if !goalDriftStopWords {
		return false
	}
	return goalDriftStopWordSet[strings.ToLower(w)]
}
